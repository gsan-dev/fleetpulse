// Package memstore implementa store.Store enteramente en memoria del
// proceso. Sirve para dos cosas: tests unitarios rapidos de todo lo que
// depende de store.Store (sin levantar Postgres) y un modo de demostracion
// (--storage=memory) para probar el panel completo sin infraestructura,
// asumiendo que perder el historico al reiniciar el servidor es aceptable.
package memstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/gdev/fleetpulse/internal/store"
)

// maxPointsPerNode acota cuantas muestras se guardan por nodo. Sin este
// limite un agente enviando cada 15s durante dias agotaria la memoria del
// proceso; el equivalente en pgstore es la politica de retencion de Timescale.
const maxPointsPerNode = 10_000

// Store es la implementacion en memoria. Segura para uso concurrente.
type Store struct {
	mu         sync.RWMutex
	nodes      map[string]store.Node
	metrics    map[string][]store.MetricPoint // ordenadas por Timestamp ascendente
	containers map[string][]store.Container
}

// New crea un almacen en memoria vacio.
func New() *Store {
	return &Store{
		nodes:      make(map[string]store.Node),
		metrics:    make(map[string][]store.MetricPoint),
		containers: make(map[string][]store.Container),
	}
}

func (s *Store) UpsertNode(_ context.Context, node store.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.nodes[node.AgentID]
	if ok {
		// Conserva el momento del primer registro y el ultimo heartbeat
		// conocido: un Register de reconexion no debe resetear ninguno.
		node.RegisteredAt = existing.RegisteredAt
		node.LastSeenAt = existing.LastSeenAt
	} else {
		node.RegisteredAt = time.Now().UTC()
	}
	s.nodes[node.AgentID] = node
	return nil
}

func (s *Store) GetNode(_ context.Context, agentID string) (store.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	node, ok := s.nodes[agentID]
	if !ok {
		return store.Node{}, store.ErrNotFound
	}
	return node, nil
}

func (s *Store) ListNodes(_ context.Context) ([]store.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodes := make([]store.Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Hostname < nodes[j].Hostname })
	return nodes, nil
}

func (s *Store) TouchNode(_ context.Context, agentID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[agentID]
	if !ok {
		return store.ErrNotFound
	}
	node.LastSeenAt = at
	s.nodes[agentID] = node
	return nil
}

func (s *Store) InsertMetric(_ context.Context, point store.MetricPoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	points := append(s.metrics[point.AgentID], point)
	if len(points) > maxPointsPerNode {
		points = points[len(points)-maxPointsPerNode:]
	}
	s.metrics[point.AgentID] = points
	return nil
}

func (s *Store) LatestMetric(_ context.Context, agentID string) (store.MetricPoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	points := s.metrics[agentID]
	if len(points) == 0 {
		return store.MetricPoint{}, store.ErrNotFound
	}
	return points[len(points)-1], nil
}

func (s *Store) QueryRange(_ context.Context, agentID string, from, to time.Time) ([]store.MetricPoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := s.metrics[agentID]
	// Los puntos ya estan ordenados por insercion (ascendente); una busqueda
	// binaria del primer indice dentro de rango basta.
	start := sort.Search(len(all), func(i int) bool { return !all[i].Timestamp.Before(from) })

	out := make([]store.MetricPoint, 0)
	for i := start; i < len(all) && !all[i].Timestamp.After(to); i++ {
		out = append(out, all[i])
	}
	return out, nil
}

func (s *Store) PruneMetrics(_ context.Context, before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for agentID, points := range s.metrics {
		cut := sort.Search(len(points), func(i int) bool { return !points[i].Timestamp.Before(before) })
		if cut > 0 {
			s.metrics[agentID] = append([]store.MetricPoint(nil), points[cut:]...)
		}
	}
	return nil
}

func (s *Store) SetContainers(_ context.Context, agentID string, containers []store.Container) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.containers[agentID] = containers
	return nil
}

func (s *Store) ListContainers(_ context.Context, agentID string) ([]store.Container, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return append([]store.Container(nil), s.containers[agentID]...), nil
}

func (s *Store) Close() error { return nil }
