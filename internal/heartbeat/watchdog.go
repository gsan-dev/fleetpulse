package heartbeat

import (
	"context"
	"log/slog"
	"time"
)

// sweepInterval es cada cuanto se revisa si algun agente ha superado el
// timeout. Va bastante mas fino que el timeout tipico (45s) para que la
// alerta no tarde en dispararse mucho mas que el propio umbral. Variable (no
// const) para que los tests puedan acelerarlo sin esperar segundos reales.
var sweepInterval = 5 * time.Second

// NodeLookup resuelve el hostname de un agente para que las alertas sean
// legibles ("web-01 desconectado" en vez de solo el agent_id).
type NodeLookup func(ctx context.Context, agentID string) (hostname string, err error)

// Alerter recibe las notificaciones de cambio de estado. Definida aqui (en
// vez de importar internal/alert) para que heartbeat no dependa del paquete
// de canales de salida; internal/alert.Alerter la satisface tal cual.
type Alerter interface {
	NotifyUnreachable(ctx context.Context, agentID, hostname string, at time.Time)
	NotifyRecovered(ctx context.Context, agentID, hostname string, at time.Time)
}

// Watchdog vigila el backend de heartbeat y dispara alertas en las
// transiciones Online<->Unreachable.
type Watchdog struct {
	backend Backend
	alerter Alerter
	lookup  NodeLookup
	timeout time.Duration
	log     *slog.Logger
}

// NewWatchdog construye el vigilante. `timeout` es cuanto puede pasar un
// nodo sin metricas antes de considerarse Unreachable (45s en la spec original).
func NewWatchdog(backend Backend, alerter Alerter, lookup NodeLookup, timeout time.Duration, log *slog.Logger) *Watchdog {
	return &Watchdog{backend: backend, alerter: alerter, lookup: lookup, timeout: timeout, log: log}
}

// Touch registra un heartbeat y dispara la alerta de recuperacion si el nodo
// venia de Unreachable. Lo llama el servidor gRPC en cada MetricPayload recibido.
func (w *Watchdog) Touch(ctx context.Context, agentID string) {
	now := time.Now().UTC()
	previous, err := w.backend.Touch(ctx, agentID, now)
	if err != nil {
		w.log.Warn("no se pudo registrar el heartbeat", "agent_id", agentID, "error", err)
		return
	}
	if previous == StateUnreachable {
		hostname := w.hostnameOf(ctx, agentID)
		w.log.Info("nodo recuperado", "agent_id", agentID, "hostname", hostname)
		w.alerter.NotifyRecovered(ctx, agentID, hostname, now)
	}
}

// Run lanza el bucle de barrido periodico hasta que ctx se cancele.
func (w *Watchdog) Run(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweepOnce(ctx)
		}
	}
}

func (w *Watchdog) sweepOnce(ctx context.Context) {
	newlyUnreachable, err := w.backend.Sweep(ctx, w.timeout, time.Now().UTC())
	if err != nil {
		w.log.Warn("fallo el barrido de heartbeat", "error", err)
		return
	}

	for _, agentID := range newlyUnreachable {
		hostname := w.hostnameOf(ctx, agentID)
		w.log.Warn("nodo marcado Unreachable", "agent_id", agentID, "hostname", hostname, "timeout", w.timeout)
		w.alerter.NotifyUnreachable(ctx, agentID, hostname, time.Now().UTC())
	}
}

func (w *Watchdog) hostnameOf(ctx context.Context, agentID string) string {
	hostname, err := w.lookup(ctx, agentID)
	if err != nil || hostname == "" {
		return agentID
	}
	return hostname
}
