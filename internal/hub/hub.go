// Package hub difunde eventos en tiempo real a los suscriptores del panel
// (Server-Sent Events) sin pasar por la base de datos: cuando llega una
// rafaga de metricas, el servidor la persiste Y la publica aqui, y las
// conexiones SSE abiertas la reciben de inmediato.
package hub

import "sync"

// bufferSize es la capacidad del canal de cada suscriptor. Un valor pequeno
// basta: si un cliente SSE se queda atras, es preferible descartarle algunas
// muestras intermedias (Publish no bloquea) a frenar la ingesta por su culpa.
const bufferSize = 8

// Hub reparte eventos de tipo T por tema (topic). En este proyecto el tema es
// el agent_id y T es *fleetpulsev1.MetricPayload, pero se mantiene generico
// para poder reutilizarlo si en el futuro se necesita otro stream (p.ej.
// eventos de contenedor).
type Hub[T any] struct {
	mu   sync.Mutex
	subs map[string]map[chan T]struct{}
}

// New crea un hub vacio.
func New[T any]() *Hub[T] {
	return &Hub[T]{subs: make(map[string]map[chan T]struct{})}
}

// Subscribe registra un nuevo oyente para `topic`. `cancel` debe llamarse
// siempre (tipicamente en un defer) para liberar el canal cuando el cliente
// se desconecta.
func (h *Hub[T]) Subscribe(topic string) (ch <-chan T, cancel func()) {
	c := make(chan T, bufferSize)

	h.mu.Lock()
	if h.subs[topic] == nil {
		h.subs[topic] = make(map[chan T]struct{})
	}
	h.subs[topic][c] = struct{}{}
	h.mu.Unlock()

	return c, func() {
		h.mu.Lock()
		delete(h.subs[topic], c)
		if len(h.subs[topic]) == 0 {
			delete(h.subs, topic)
		}
		h.mu.Unlock()
		close(c)
	}
}

// Publish entrega `value` a todos los oyentes de `topic`. No bloquea: un
// suscriptor lento simplemente pierde el evento en vez de ralentizar la
// ingesta de metricas de todos los demas nodos.
func (h *Hub[T]) Publish(topic string, value T) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c := range h.subs[topic] {
		select {
		case c <- value:
		default:
		}
	}
}
