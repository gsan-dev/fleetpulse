// Command fleetpulse-agent es el demonio que se instala en cada nodo de la
// flota: recoge metricas del sistema y de los contenedores, las transmite al
// recolector central por gRPC y ejecuta las acciones que el panel dispara
// sobre los contenedores (reinicio, logs bajo demanda).
//
// Funciona igual en Linux, Windows y macOS: gopsutil y el SDK de Docker ya
// son multiplataforma, y en Windows el binario ademas sabe instalarse como
// servicio nativo (ver internal/winservice y `fleetpulse-agent service`).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/gdev/fleetpulse/internal/collector"
	"github.com/gdev/fleetpulse/internal/commands"
	"github.com/gdev/fleetpulse/internal/config"
	"github.com/gdev/fleetpulse/internal/identity"
	"github.com/gdev/fleetpulse/internal/telemetry"
	"github.com/gdev/fleetpulse/internal/transport"
	"google.golang.org/protobuf/encoding/protojson"
)

// version la inyecta el linker en los builds de release:
// go build -ldflags "-X main.version=v0.1.0"
var version = "dev"

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "fleetpulse-agent: %v\n", err)
		os.Exit(1)
	}
}

// dispatch separa el subcomando `service` (gestion del servicio nativo de
// Windows) del arranque normal del agente. En Linux/macOS `service` no
// existe: el ciclo de vida lo gobierna systemd (ver install/install.sh).
func dispatch(args []string) error {
	if len(args) > 0 && args[0] == "service" {
		return runServiceCommand(args[1:])
	}
	if isWindowsService() {
		// Arrancado por el Service Control Manager: el bucle de eventos de
		// svc.Handler es quien controla el contexto (ver service_windows.go).
		return runAsWindowsService(args)
	}

	// Ejecucion interactiva (o unidad systemd en Linux/macOS): el contexto se
	// cancela con SIGINT/SIGTERM para que systemd pueda parar el servicio sin
	// dejar streams colgando contra el daemon de Docker o el servidor.
	ctx, stop := notifyShutdown()
	defer stop()
	return runWithContext(ctx, args)
}

func runWithContext(ctx context.Context, args []string) error {
	cfg, err := config.Load(args, os.Stderr)
	if err != nil {
		return err
	}

	log := newLogger(cfg.LogLevel)

	node, err := identity.Load(ctx, cfg.StateDir, version, cfg.Hostname)
	if err != nil {
		return err
	}
	log.Info("agente iniciado",
		"agent_id", node.AgentID,
		"hostname", node.Hostname,
		"os", node.OS,
		"arch", node.Arch,
		"version", version,
	)

	containerSource, containerController, err := openContainerSource(ctx, cfg, node, log)
	if err != nil {
		return err
	}

	c := collector.New(containerSource, log)
	defer c.Close()

	if cfg.Once {
		return emitOnce(ctx, c, node.AgentID, log)
	}

	return runDaemon(ctx, cfg, node, c, containerController, log)
}

// runDaemon es el bucle principal: registra el nodo, abre el canal de
// comandos y transmite metricas hasta que el contexto se cancele.
func runDaemon(
	ctx context.Context,
	cfg *config.Config,
	node *identity.Node,
	c *collector.Collector,
	containerController commands.ContainerController,
	log *slog.Logger,
) error {
	tlsConfig, err := transport.BuildTLSConfig(transport.TLSFiles{
		CAFile:   cfg.TLSCAFile,
		CertFile: cfg.TLSCertFile,
		KeyFile:  cfg.TLSKeyFile,
	})
	if err != nil {
		return err
	}

	client, err := transport.Dial(cfg.ServerAddr, cfg.Token, node.AgentID, tlsConfig, log)
	if err != nil {
		return err
	}
	defer client.Close()

	interval := registerWithRetry(ctx, client, node, cfg.Interval, log)
	if ctx.Err() != nil {
		return nil // se pidio parar mientras se intentaba registrar
	}

	executor := commands.New(client.RPC(), node.AgentID, cfg.Token, containerController, log)
	go executor.Run(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := pushOnce(ctx, c, client, node.AgentID, log); err != nil && ctx.Err() == nil {
			log.Warn("no se pudo enviar la rafaga de metricas", "error", err)
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			log.Info("parando el agente")
			client.CloseStream()
			return nil
		}
	}
}

// registerWithRetry bloquea hasta registrar el nodo con exito o hasta que
// ctx se cancele. Es necesario esperar aqui (en vez de dejar que el primer
// PushMetrics falle en silencio): el servidor exige que el nodo exista antes
// de aceptar metricas suyas.
func registerWithRetry(ctx context.Context, client *transport.Client, node *identity.Node, fallbackInterval time.Duration, log *slog.Logger) time.Duration {
	const maxBackoff = 30 * time.Second
	backoff := time.Second

	for {
		resp, err := client.Register(ctx, telemetry.NodeInfo(node))
		if err == nil {
			log.Info("nodo registrado en el servidor", "mensaje", resp.GetMessage())
			if secs := resp.GetHeartbeatIntervalSeconds(); secs > 0 {
				return time.Duration(secs) * time.Second
			}
			return fallbackInterval
		}

		log.Warn("no se pudo registrar el nodo, reintentando", "error", err, "espera", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return fallbackInterval
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// pushOnce recoge una muestra y la envia por el stream gRPC.
func pushOnce(ctx context.Context, c *collector.Collector, client *transport.Client, agentID string, log *slog.Logger) error {
	snap, err := c.Collect(ctx)
	if err != nil {
		return fmt.Errorf("recoger metricas: %w", err)
	}

	payload := telemetry.MetricPayload(agentID, snap)
	if err := client.Send(ctx, payload); err != nil {
		return err
	}

	log.Debug("rafaga enviada",
		"cpu_percent", snap.System.CPUUsagePercent,
		"contenedores", len(snap.Containers),
	)
	return nil
}

// emitOnce recoge una unica muestra y la imprime por stdout en JSON canonico
// de protobuf, sin necesidad de servidor ni token. Pensado para depurar un
// nodo recien instalado: `fleetpulse-agent --once --log-level debug`.
func emitOnce(ctx context.Context, c *collector.Collector, agentID string, log *slog.Logger) error {
	snap, err := c.Collect(ctx)
	if err != nil {
		return err
	}

	payload := telemetry.MetricPayload(agentID, snap)
	encoded, err := protojson.MarshalOptions{Multiline: false}.Marshal(payload)
	if err != nil {
		return fmt.Errorf("serializar payload: %w", err)
	}

	fmt.Println(string(encoded))
	log.Debug("muestra recogida",
		"cpu_percent", snap.System.CPUUsagePercent,
		"contenedores", len(snap.Containers),
	)
	return nil
}

// openContainerSource elige y abre la fuente de contenedores: Kubelet dentro
// de un Pod de Kubernetes, Docker en cualquier otro nodo. Devuelve por
// separado la interfaz de lectura (collector.ContainerSource) y la de
// comandos (commands.ContainerController) porque solo Docker soporta hoy
// reinicios y logs bajo demanda; Kubelet siempre devuelve un controller nil
// y el ejecutor de comandos responde "no disponible" sin caerse (ver
// internal/commands).
//
// En `auto` (por defecto) la falta de runtime es una condicion normal —
// nodos sin contenedores en una flota heterogenea — y solo se reporta el
// sistema; en `--docker=on` es un error de arranque.
func openContainerSource(ctx context.Context, cfg *config.Config, node *identity.Node, log *slog.Logger) (collector.ContainerSource, commands.ContainerController, error) {
	if cfg.Docker == config.DockerOff {
		return nil, nil, nil
	}

	if useKubelet(cfg) {
		inspector, err := collector.NewKubeletInspector(os.Getenv("NODE_IP"), os.Getenv("NODE_NAME"), float64(node.CPUCores))
		if err != nil {
			log.Warn("no se pudo iniciar el inspector de kubelet, se reportara solo el sistema", "motivo", err)
			return nil, nil, nil
		}
		log.Info("inspector de kubelet conectado", "node", os.Getenv("NODE_NAME"))
		return inspector, nil, nil
	}

	inspector, err := collector.NewDockerInspector(ctx)
	if err != nil {
		if cfg.Docker == config.DockerOn {
			return nil, nil, fmt.Errorf("docker exigido con --docker=on: %w", err)
		}
		log.Info("sin runtime de contenedores, se reportara solo el sistema", "motivo", err)
		return nil, nil, nil
	}

	log.Info("inspector de docker conectado")
	return inspector, inspector, nil
}

// useKubelet decide si el nodo debe leer contenedores via la API del
// kubelet en vez de Docker. En modo auto se detecta la ejecucion dentro de
// un Pod por KUBERNETES_SERVICE_HOST, variable que Kubernetes inyecta
// siempre en todo contenedor del cluster.
func useKubelet(cfg *config.Config) bool {
	switch cfg.Runtime {
	case "kubernetes":
		return true
	case "docker":
		return false
	default:
		return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
	}
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}

	// Los logs van a stderr para no mezclarse con las muestras de --once por
	// stdout, y en texto plano porque es lo que journalctl (Linux) y el
	// visor de eventos (Windows, via el redirector del propio servicio)
	// muestran mejor.
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}
