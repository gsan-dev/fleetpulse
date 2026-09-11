// Package commandbus enruta comandos del servidor hacia el agente correcto y
// correlaciona sus resultados. El agente es quien abre la conexion
// (StreamCommands), asi que el servidor no puede "llamarle": en vez de eso,
// cuando el agente se conecta se registra un canal de salida aqui, y la API
// HTTP publica en el escribiendo directamente en ese canal.
package commandbus

import (
	"context"
	"errors"
	"sync"

	fleetpulsev1 "github.com/gdev/fleetpulse/gen/fleetpulse/v1"
)

// ErrAgentOffline: no hay ningun stream de comandos abierto para ese agente
// (nunca se conecto, o su conexion gRPC cayo).
var ErrAgentOffline = errors.New("commandbus: el agente no tiene un canal de comandos abierto")

const outgoingBuffer = 16

// Bus enruta comandos y correlaciona resultados.
type Bus struct {
	mu       sync.Mutex
	outgoing map[string]chan *fleetpulsev1.Command       // agent_id -> canal de salida
	pending  map[string]chan *fleetpulsev1.CommandResult // command_id -> quien espera el resultado
}

// New crea un bus de comandos vacio.
func New() *Bus {
	return &Bus{
		outgoing: make(map[string]chan *fleetpulsev1.Command),
		pending:  make(map[string]chan *fleetpulsev1.CommandResult),
	}
}

// Register abre el canal de salida de un agente. Lo llama el handler gRPC de
// StreamCommands en cuanto el agente se suscribe; `cancel` debe invocarse
// cuando el stream se cierra (agente desconectado).
//
// Si el agente ya tenia un canal registrado (reconexion sin que el servidor
// notara la caida previa), el registro nuevo sustituye al viejo: solo puede
// haber una conexion de comandos activa por agente.
func (b *Bus) Register(agentID string) (ch <-chan *fleetpulsev1.Command, cancel func()) {
	c := make(chan *fleetpulsev1.Command, outgoingBuffer)

	b.mu.Lock()
	b.outgoing[agentID] = c
	b.mu.Unlock()

	return c, func() {
		b.mu.Lock()
		if b.outgoing[agentID] == c {
			delete(b.outgoing, agentID)
		}
		b.mu.Unlock()
	}
}

// Dispatch entrega un comando al agente si esta conectado. No espera resultado.
func (b *Bus) Dispatch(ctx context.Context, agentID string, cmd *fleetpulsev1.Command) error {
	b.mu.Lock()
	c, ok := b.outgoing[agentID]
	b.mu.Unlock()
	if !ok {
		return ErrAgentOffline
	}

	select {
	case c <- cmd:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DispatchAndWait envia el comando y bloquea hasta que el agente reporte el
// resultado via ReportCommandResult o venza `ctx` (p.ej. el timeout de la
// peticion HTTP que pidio unos logs). Pensado solo para comandos que de
// verdad necesitan respuesta sincrona (FetchContainerLogs); para acciones de
// "dispara y olvida" (RestartContainer) basta con Dispatch.
func (b *Bus) DispatchAndWait(ctx context.Context, agentID string, cmd *fleetpulsev1.Command) (*fleetpulsev1.CommandResult, error) {
	resultCh := make(chan *fleetpulsev1.CommandResult, 1)

	b.mu.Lock()
	b.pending[cmd.GetCommandId()] = resultCh
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, cmd.GetCommandId())
		b.mu.Unlock()
	}()

	if err := b.Dispatch(ctx, agentID, cmd); err != nil {
		return nil, err
	}

	select {
	case result := <-resultCh:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Complete entrega el resultado de un comando a quien lo esta esperando (si
// alguien lo espera: un RestartContainer disparado con Dispatch no tiene
// receptor y el resultado simplemente se descarta tras loguearse).
func (b *Bus) Complete(result *fleetpulsev1.CommandResult) (delivered bool) {
	b.mu.Lock()
	ch, ok := b.pending[result.GetCommandId()]
	b.mu.Unlock()
	if !ok {
		return false
	}

	select {
	case ch <- result:
		return true
	default:
		return false
	}
}

// Connected informa si el agente tiene un canal de comandos abierto ahora
// mismo, para que la API pueda devolver un 409 claro en vez de colgarse.
func (b *Bus) Connected(agentID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.outgoing[agentID]
	return ok
}
