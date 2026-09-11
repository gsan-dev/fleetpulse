package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	fleetpulsev1 "github.com/gdev/fleetpulse/gen/fleetpulse/v1"
	"github.com/gdev/fleetpulse/internal/commandbus"
	"github.com/gdev/fleetpulse/internal/heartbeat"
	"github.com/gdev/fleetpulse/internal/hub"
	"github.com/gdev/fleetpulse/internal/store"
	"github.com/gdev/fleetpulse/internal/store/memstore"
)

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestServer(t *testing.T, token string) (*Server, store.Store, heartbeat.Backend) {
	t.Helper()
	st := memstore.New()
	hb := heartbeat.NewMemoryBackend()
	commands := commandbus.New()
	metricsHub := hub.New[*fleetpulsev1.MetricPayload]()
	return New(st, hb, commands, metricsHub, token, "*", noopLogger()), st, hb
}

func TestHandleListNodesCalculaHealth(t *testing.T) {
	srv, st, hb := newTestServer(t, "")
	ctx := context.Background()

	// "down" se marca Unreachable primero, con un barrido que solo a el
	// afecta: Sweep recorre TODO el backend, asi que si "healthy" y "warning"
	// ya estuvieran registrados con este mismo barrido de timeout 0 tambien
	// caerian. Se registran despues, ya sin mas barridos de por medio.
	_ = st.UpsertNode(ctx, store.Node{AgentID: "down", Hostname: "cache-01"})
	_, _ = hb.Touch(ctx, "down", time.Now().Add(-time.Hour))
	_, _ = hb.Sweep(ctx, time.Minute, time.Now())

	_ = st.UpsertNode(ctx, store.Node{AgentID: "healthy", Hostname: "web-01"})
	_ = st.InsertMetric(ctx, store.MetricPoint{AgentID: "healthy", Timestamp: time.Now(), CPUUsagePercent: 10, MemoryTotalBytes: 100, MemoryUsedBytes: 10})
	_, _ = hb.Touch(ctx, "healthy", time.Now())

	_ = st.UpsertNode(ctx, store.Node{AgentID: "warning", Hostname: "db-01"})
	_ = st.InsertMetric(ctx, store.MetricPoint{AgentID: "warning", Timestamp: time.Now(), CPUUsagePercent: 95, MemoryTotalBytes: 100, MemoryUsedBytes: 10})
	_, _ = hb.Touch(ctx, "warning", time.Now())

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var nodes []NodeSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("len(nodes) = %d", len(nodes))
	}

	health := map[string]Health{}
	for _, n := range nodes {
		health[n.AgentID] = n.Health
	}
	if health["healthy"] != HealthHealthy {
		t.Errorf("healthy -> %v", health["healthy"])
	}
	if health["warning"] != HealthWarning {
		t.Errorf("warning -> %v", health["warning"])
	}
	if health["down"] != HealthUnreachable {
		t.Errorf("down -> %v", health["down"])
	}
}

func TestHandleGetNodeNoEncontrado(t *testing.T) {
	srv, _, _ := newTestServer(t, "")

	req := httptest.NewRequest(http.MethodGet, "/api/nodes/no-existe", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, se esperaba 404", rec.Code)
	}
}

func TestHandleRestartContainerSinCanalDevuelve409(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	_ = st.UpsertNode(context.Background(), store.Node{AgentID: "a1", Hostname: "web-01"})

	req := httptest.NewRequest(http.MethodPost, "/api/nodes/a1/containers/c1/restart", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuthRechazaSinToken(t *testing.T) {
	srv, _, _ := newTestServer(t, "secreto")

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, se esperaba 401", rec.Code)
	}
}

func TestAuthAceptaTokenPorQuery(t *testing.T) {
	srv, _, _ := newTestServer(t, "secreto")

	req := httptest.NewRequest(http.MethodGet, "/api/nodes?token=secreto", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuthAceptaTokenPorCabecera(t *testing.T) {
	srv, _, _ := newTestServer(t, "secreto")

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	req.Header.Set("Authorization", "Bearer secreto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHealthzNoRequiereToken(t *testing.T) {
	srv, _, _ := newTestServer(t, "secreto")

	req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestHandleGetMetricsFiltraPorSince(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	ctx := context.Background()
	_ = st.UpsertNode(ctx, store.Node{AgentID: "a1", Hostname: "web-01"})

	now := time.Now().UTC()
	_ = st.InsertMetric(ctx, store.MetricPoint{AgentID: "a1", Timestamp: now.Add(-2 * time.Hour)})
	_ = st.InsertMetric(ctx, store.MetricPoint{AgentID: "a1", Timestamp: now.Add(-10 * time.Minute)})

	req := httptest.NewRequest(http.MethodGet, "/api/nodes/a1/metrics?since=30m", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var points []MetricDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &points); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(points) != 1 {
		t.Errorf("len(points) = %d, se esperaba 1 (solo la muestra de hace 10m)", len(points))
	}
}
