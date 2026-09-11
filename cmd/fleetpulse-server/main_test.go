package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/gdev/fleetpulse/internal/heartbeat"
	"github.com/gdev/fleetpulse/internal/store"
	"github.com/gdev/fleetpulse/internal/store/memstore"
)

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestSeedHeartbeatsDetectaNodosRealmenteCaidos reproduce el bug real
// observado en produccion: tras reiniciar el servidor (o Redis), un nodo que
// lleva horas sin reportar se quedaba marcado "Saludable" para siempre,
// porque el backend de heartbeat arrancaba vacio y heartbeat.Sweep solo
// puede marcar Unreachable a un agente que tiene entrada.
func TestSeedHeartbeatsDetectaNodosRealmenteCaidos(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()

	now := time.Now().UTC()
	if err := st.UpsertNode(ctx, store.Node{AgentID: "vivo", Hostname: "web-01"}); err != nil {
		t.Fatalf("UpsertNode vivo: %v", err)
	}
	if err := st.TouchNode(ctx, "vivo", now); err != nil {
		t.Fatalf("TouchNode vivo: %v", err)
	}
	if err := st.UpsertNode(ctx, store.Node{AgentID: "caido", Hostname: "db-01"}); err != nil {
		t.Fatalf("UpsertNode caido: %v", err)
	}
	if err := st.TouchNode(ctx, "caido", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("TouchNode caido: %v", err)
	}

	// Backend nuevo y vacio, como tras un reinicio del proceso.
	backend := heartbeat.NewMemoryBackend()
	seedHeartbeats(ctx, st, backend, noopLogger())

	newlyUnreachable, err := backend.Sweep(ctx, 45*time.Second, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(newlyUnreachable) != 1 || newlyUnreachable[0] != "caido" {
		t.Fatalf("Sweep = %v, se esperaba solo [caido]", newlyUnreachable)
	}

	vivoState, _ := backend.State(ctx, "vivo")
	if vivoState != heartbeat.StateOnline {
		t.Errorf("estado de 'vivo' = %v, se esperaba Online", vivoState)
	}
	caidoState, _ := backend.State(ctx, "caido")
	if caidoState != heartbeat.StateUnreachable {
		t.Errorf("estado de 'caido' = %v, se esperaba Unreachable", caidoState)
	}
}

func TestSeedHeartbeatsSinNodosNoFalla(t *testing.T) {
	seedHeartbeats(context.Background(), memstore.New(), heartbeat.NewMemoryBackend(), noopLogger())
}
