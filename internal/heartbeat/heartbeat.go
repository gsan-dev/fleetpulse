// Package heartbeat vigila cuando un nodo deja de emitir metricas. La
// llegada de cada MetricPayload cuenta como heartbeat (no hay un mensaje
// separado): si no llega ninguno en `timeout` segundos, el nodo pasa a
// Unreachable y se dispara una alerta; si vuelve a emitir, se marca de
// nuevo Online y se dispara la alerta de recuperacion.
package heartbeat

import (
	"context"
	"time"
)

// State es el estado de conectividad de un nodo. No confundir con el color
// de "Alerta de Recursos" de la Fleet Grid (Amarillo), que se deriva de la
// ultima metrica (CPU/disco altos) y no de este paquete.
type State int

const (
	// StateUnknown: nunca ha llegado un heartbeat para este agent_id.
	StateUnknown State = iota
	StateOnline
	StateUnreachable
)

func (s State) String() string {
	switch s {
	case StateOnline:
		return "online"
	case StateUnreachable:
		return "unreachable"
	default:
		return "unknown"
	}
}

// Backend persiste el ultimo heartbeat conocido de cada agente. La
// implementacion en memoria basta para una sola instancia del servidor; la de
// Redis permite compartir el estado entre varias replicas.
type Backend interface {
	// Touch registra un heartbeat en `at` y devuelve el estado que tenia el
	// agente inmediatamente antes (para detectar la transicion Unreachable -> Online).
	Touch(ctx context.Context, agentID string, at time.Time) (previous State, err error)
	// Sweep recorre los agentes con heartbeat conocido y devuelve los que
	// acaban de superar `timeout` sin dar senales, marcandolos Unreachable
	// atomicamente para que una unica llamada a Sweep dispare su alerta.
	Sweep(ctx context.Context, timeout time.Duration, now time.Time) ([]string, error)
	// State devuelve el estado actual de un agente (para la API del panel).
	State(ctx context.Context, agentID string) (State, error)
}
