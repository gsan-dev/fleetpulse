// Package alert envia notificaciones cuando un nodo cambia de estado de
// conectividad. Implementa canales de Telegram y Discord (los que pide la
// spec del proyecto) detras de una interfaz comun, mas un fan-out para tener
// varios activos a la vez y un no-op para cuando no se configura ninguno.
package alert

import (
	"context"
	"log/slog"
	"time"
)

// Alerter satisface heartbeat.Alerter. Vive en su propio paquete (en vez de
// dentro de internal/heartbeat) porque los canales de salida — HTTP a
// Telegram/Discord — son un detalle de infraestructura ajeno a la logica de
// deteccion de caidas.
type Alerter interface {
	NotifyUnreachable(ctx context.Context, agentID, hostname string, at time.Time)
	NotifyRecovered(ctx context.Context, agentID, hostname string, at time.Time)
}

// Channel es un unico canal de salida (Telegram, Discord...). A diferencia de
// Alerter, sus metodos devuelven error: es Multi quien decide que hacer con
// el fallo (loguearlo) para que un canal caido no tumbe a los demas.
type Channel interface {
	Name() string
	Send(ctx context.Context, message string) error
}

// Noop no envia nada. Es el Alerter por defecto cuando el operador no ha
// configurado ningun webhook: el watchdog sigue marcando Unreachable/Online
// en el panel, simplemente no hay notificacion externa.
type Noop struct{}

func (Noop) NotifyUnreachable(context.Context, string, string, time.Time) {}
func (Noop) NotifyRecovered(context.Context, string, string, time.Time)   {}

// Multi reparte cada evento entre todos los canales configurados.
type Multi struct {
	channels []Channel
	log      *slog.Logger
}

// New agrupa los canales activos. Un Channel nil se ignora, para poder
// construir la lista directamente desde configuracion opcional sin `if`s
// repetidos en el llamante.
func New(log *slog.Logger, channels ...Channel) Alerter {
	active := make([]Channel, 0, len(channels))
	for _, c := range channels {
		if c != nil {
			active = append(active, c)
		}
	}
	if len(active) == 0 {
		return Noop{}
	}
	return &Multi{channels: active, log: log}
}

func (m *Multi) NotifyUnreachable(ctx context.Context, agentID, hostname string, at time.Time) {
	m.broadcast(ctx, formatUnreachable(hostname, agentID, at))
}

func (m *Multi) NotifyRecovered(ctx context.Context, agentID, hostname string, at time.Time) {
	m.broadcast(ctx, formatRecovered(hostname, agentID, at))
}

func (m *Multi) broadcast(ctx context.Context, message string) {
	for _, c := range m.channels {
		sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := c.Send(sendCtx, message); err != nil {
			m.log.Warn("no se pudo enviar la alerta", "canal", c.Name(), "error", err)
		}
		cancel()
	}
}

func formatUnreachable(hostname, agentID string, at time.Time) string {
	return "🔴 FleetPulse: " + hostname + " (" + agentID[:min(8, len(agentID))] + ") no responde desde " +
		at.Format(time.RFC3339) + ". Marcado como Unreachable."
}

func formatRecovered(hostname, agentID string, at time.Time) string {
	return "🟢 FleetPulse: " + hostname + " (" + agentID[:min(8, len(agentID))] + ") ha vuelto a reportar metricas a las " +
		at.Format(time.RFC3339) + "."
}
