// Package heartbeattest ofrece dobles de prueba para heartbeat.Alerter,
// compartidos entre los tests del propio paquete heartbeat y los de
// cmd/fleetpulse-server: antes de este paquete cada uno tenia su propia
// copia manuscrita, y una no estaba protegida con mutex.
package heartbeattest

import (
	"context"
	"sync"
	"time"
)

// RecordedEvent es una llamada a NotifyUnreachable/NotifyRecovered capturada.
type RecordedEvent struct {
	Kind     string // "unreachable" o "recovered"
	AgentID  string
	Hostname string
}

// RecordingAlerter implementa heartbeat.Alerter (y alert.Alerter, con la
// misma forma) y solo recuerda lo que le llega. Segura para uso concurrente.
type RecordingAlerter struct {
	mu     sync.Mutex
	events []RecordedEvent
}

func (r *RecordingAlerter) NotifyUnreachable(_ context.Context, agentID, hostname string, _ time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, RecordedEvent{Kind: "unreachable", AgentID: agentID, Hostname: hostname})
}

func (r *RecordingAlerter) NotifyRecovered(_ context.Context, agentID, hostname string, _ time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, RecordedEvent{Kind: "recovered", AgentID: agentID, Hostname: hostname})
}

// Events devuelve una copia de lo capturado hasta ahora.
func (r *RecordingAlerter) Events() []RecordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RecordedEvent(nil), r.events...)
}
