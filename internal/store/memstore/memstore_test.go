package memstore

import (
	"context"
	"testing"
	"time"

	"github.com/gdev/fleetpulse/internal/store"
)

func TestUpsertNodePreservaRegistroYHeartbeat(t *testing.T) {
	s := New()
	ctx := context.Background()

	if err := s.UpsertNode(ctx, store.Node{AgentID: "a1", Hostname: "web-01"}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	first, err := s.GetNode(ctx, "a1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if first.RegisteredAt.IsZero() {
		t.Error("RegisteredAt no se establecio en el primer registro")
	}

	touchedAt := time.Now().UTC()
	if err := s.TouchNode(ctx, "a1", touchedAt); err != nil {
		t.Fatalf("TouchNode: %v", err)
	}

	// Un segundo registro (reconexion del agente) no debe resetear
	// RegisteredAt ni LastSeenAt.
	if err := s.UpsertNode(ctx, store.Node{AgentID: "a1", Hostname: "web-01-renamed"}); err != nil {
		t.Fatalf("UpsertNode (reconexion): %v", err)
	}
	second, err := s.GetNode(ctx, "a1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if !second.RegisteredAt.Equal(first.RegisteredAt) {
		t.Errorf("RegisteredAt cambio: %v -> %v", first.RegisteredAt, second.RegisteredAt)
	}
	if !second.LastSeenAt.Equal(touchedAt) {
		t.Errorf("LastSeenAt = %v, se esperaba %v", second.LastSeenAt, touchedAt)
	}
	if second.Hostname != "web-01-renamed" {
		t.Errorf("Hostname no se actualizo: %q", second.Hostname)
	}
}

func TestGetNodeNoEncontrado(t *testing.T) {
	s := New()
	if _, err := s.GetNode(context.Background(), "no-existe"); err != store.ErrNotFound {
		t.Errorf("err = %v, se esperaba ErrNotFound", err)
	}
}

func TestQueryRangeFiltraPorTiempo(t *testing.T) {
	s := New()
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	for i := range 10 {
		_ = s.InsertMetric(ctx, store.MetricPoint{
			AgentID:   "a1",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}

	points, err := s.QueryRange(ctx, "a1", base.Add(3*time.Minute), base.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(points) != 4 { // minutos 3,4,5,6
		t.Errorf("len(points) = %d, se esperaban 4", len(points))
	}
	if !points[0].Timestamp.Equal(base.Add(3 * time.Minute)) {
		t.Errorf("primer punto = %v", points[0].Timestamp)
	}
}

func TestLatestMetricSinMuestras(t *testing.T) {
	s := New()
	if _, err := s.LatestMetric(context.Background(), "a1"); err != store.ErrNotFound {
		t.Errorf("err = %v, se esperaba ErrNotFound", err)
	}
}

func TestPruneMetricsBorraAnteriores(t *testing.T) {
	s := New()
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	for i := range 5 {
		_ = s.InsertMetric(ctx, store.MetricPoint{AgentID: "a1", Timestamp: base.Add(time.Duration(i) * time.Hour)})
	}

	if err := s.PruneMetrics(ctx, base.Add(2*time.Hour)); err != nil {
		t.Fatalf("PruneMetrics: %v", err)
	}

	points, _ := s.QueryRange(ctx, "a1", base.Add(-time.Hour), base.Add(10*time.Hour))
	if len(points) != 3 { // horas 2,3,4 sobreviven
		t.Errorf("len(points) = %d, se esperaban 3", len(points))
	}
}

func TestSetContainersReemplazaElSnapshot(t *testing.T) {
	s := New()
	ctx := context.Background()

	_ = s.SetContainers(ctx, "a1", []store.Container{{ID: "c1", Name: "web"}, {ID: "c2", Name: "db"}})
	first, _ := s.ListContainers(ctx, "a1")
	if len(first) != 2 {
		t.Fatalf("len(first) = %d, se esperaban 2", len(first))
	}

	// La siguiente rafaga solo trae "web": "db" debe desaparecer del snapshot.
	_ = s.SetContainers(ctx, "a1", []store.Container{{ID: "c1", Name: "web"}})
	second, _ := s.ListContainers(ctx, "a1")
	if len(second) != 1 || second[0].ID != "c1" {
		t.Errorf("second = %+v, se esperaba solo c1", second)
	}
}
