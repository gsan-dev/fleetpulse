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

	// Seed inicializa el estado de un agente a partir de un last_seen_at ya
	// conocido (el persistido en el almacen), sin pasar por la logica de
	// transicion de Touch/Sweep: es solo para precargar el backend al
	// arrancar el servidor, nunca para un heartbeat real.
	//
	// El estado resultante se deriva directamente de `now - lastSeen` frente
	// a `timeout`: si ya esta vencido, el agente queda Unreachable desde el
	// primer instante (no hace falta esperar al primer Sweep, y ese Sweep no
	// lo reporta como una transicion nueva, asi que no se duplica ninguna
	// alerta para un nodo que ya se sabia caido antes del reinicio).
	//
	// Nunca hace retroceder un last_seen_at mas reciente que ya estuviera
	// registrado: en un backend compartido entre varias replicas (Redis),
	// una replica reiniciandose no debe poder marcar Unreachable a un nodo
	// que otra replica, todavia viva, sigue viendo sano.
	Seed(ctx context.Context, agentID string, lastSeen, now time.Time, timeout time.Duration) error
}
