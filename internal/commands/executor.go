// Package commands consume el canal de comandos que el servidor abre hacia
// cada agente (StreamCommands) y ejecuta las acciones que el panel dispara
// sobre los contenedores: reinicios y peticiones de logs bajo demanda.
package commands

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	fleetpulsev1 "github.com/gdev/fleetpulse/gen/fleetpulse/v1"
	"github.com/gdev/fleetpulse/internal/rpcauth"
)

// ContainerController es lo minimo que el ejecutor necesita del runtime de
// contenedores. collector.DockerInspector la satisface; un nodo sin Docker
// pasa nil y los comandos se responden con error sin tocar nada.
type ContainerController interface {
	Restart(ctx context.Context, id string) error
	Logs(ctx context.Context, id string, tail int) ([]string, error)
}

const (
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
)

// Executor mantiene abierto el canal de comandos y despacha cada uno a
// medida que llega.
type Executor struct {
	rpc        fleetpulsev1.CollectorServiceClient
	agentID    string
	token      string
	containers ContainerController
	log        *slog.Logger
}

// New construye el ejecutor. `containers` puede ser nil (nodo sin runtime de
// contenedores detectado).
func New(rpc fleetpulsev1.CollectorServiceClient, agentID, token string, containers ContainerController, log *slog.Logger) *Executor {
	return &Executor{rpc: rpc, agentID: agentID, token: token, containers: containers, log: log}
}

// Run mantiene el canal de comandos abierto hasta que ctx se cancele,
// reconectando con backoff exponencial ante cualquier corte.
func (e *Executor) Run(ctx context.Context) {
	backoff := minBackoff
	for ctx.Err() == nil {
		err := e.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			e.log.Warn("canal de comandos interrumpido, reintentando", "error", err, "espera", backoff)
		}

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, maxBackoff)
		if err == nil {
			backoff = minBackoff
		}
	}
}

func (e *Executor) runOnce(ctx context.Context) error {
	callCtx := rpcauth.OutgoingContext(ctx, e.token)
	stream, err := e.rpc.StreamCommands(callCtx, &fleetpulsev1.CommandSubscribe{AgentId: e.agentID})
	if err != nil {
		return err
	}
	e.log.Info("canal de comandos conectado")

	for {
		cmd, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		go e.handle(ctx, cmd)
	}
}

// handle se ejecuta en su propia goroutine: una peticion de logs lenta no
// debe retrasar un reinicio que llegue justo despues por el mismo stream.
func (e *Executor) handle(ctx context.Context, cmd *fleetpulsev1.Command) {
	result := &fleetpulsev1.CommandResult{AgentId: e.agentID, CommandId: cmd.GetCommandId()}

	switch {
	case e.containers == nil:
		result.Error = "docker no esta disponible en este nodo"
	case cmd.GetRestartContainer() != nil:
		e.applyRestart(ctx, cmd.GetRestartContainer(), result)
	case cmd.GetFetchContainerLogs() != nil:
		e.applyFetchLogs(ctx, cmd.GetFetchContainerLogs(), result)
	default:
		result.Error = "comando desconocido"
	}

	reportCtx, cancel := context.WithTimeout(rpcauth.OutgoingContext(ctx, e.token), 10*time.Second)
	defer cancel()
	if _, err := e.rpc.ReportCommandResult(reportCtx, result); err != nil {
		e.log.Warn("no se pudo reportar el resultado del comando", "command_id", cmd.GetCommandId(), "error", err)
	}
}

func (e *Executor) applyRestart(ctx context.Context, action *fleetpulsev1.RestartContainer, result *fleetpulsev1.CommandResult) {
	if err := e.containers.Restart(ctx, action.GetContainerId()); err != nil {
		result.Error = err.Error()
		return
	}
	result.Success = true
}

func (e *Executor) applyFetchLogs(ctx context.Context, action *fleetpulsev1.FetchContainerLogs, result *fleetpulsev1.CommandResult) {
	lines, err := e.containers.Logs(ctx, action.GetContainerId(), int(action.GetTailLines()))
	if err != nil {
		result.Error = err.Error()
		return
	}
	result.Success = true
	result.LogLines = lines
}
