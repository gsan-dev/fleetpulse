package heartbeat

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// newTestRedisBackend levanta un Redis en memoria (miniredis ejecuta los
// scripts Lua de verdad, via gopher-lua) para que RedisBackend deje de ser
// el unico backend sin ninguna prueba automatizada: antes de esto, un fallo
// en touchScript/sweepScript/seedScript solo se habria visto contra un
// Redis real en produccion.
func newTestRedisBackend(t *testing.T) *RedisBackend {
	t.Helper()

	server := miniredis.RunT(t)
	backend, err := NewRedisBackend(context.Background(), "redis://"+server.Addr())
	if err != nil {
		t.Fatalf("NewRedisBackend: %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	return backend
}

func TestRedisTouchDevuelveEstadoPrevio(t *testing.T) {
	b := newTestRedisBackend(t)
	ctx := context.Background()
	now := time.Now().UTC()

	previous, err := b.Touch(ctx, "a1", now)
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if previous != StateUnknown {
		t.Errorf("previous = %v, se esperaba StateUnknown", previous)
	}

	previous, err = b.Touch(ctx, "a1", now.Add(time.Second))
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if previous != StateOnline {
		t.Errorf("previous = %v, se esperaba StateOnline", previous)
	}
}

func TestRedisSweepMarcaUnreachableUnaSolaVez(t *testing.T) {
	b := newTestRedisBackend(t)
	ctx := context.Background()
	start := time.Now().UTC()

	if _, err := b.Touch(ctx, "a1", start); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	afterTimeout := start.Add(time.Minute)
	stale, err := b.Sweep(ctx, 45*time.Second, afterTimeout)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(stale) != 1 || stale[0] != "a1" {
		t.Fatalf("stale = %v, se esperaba [a1]", stale)
	}

	state, _ := b.State(ctx, "a1")
	if state != StateUnreachable {
		t.Errorf("state = %v, se esperaba StateUnreachable", state)
	}

	stale, err = b.Sweep(ctx, 45*time.Second, afterTimeout.Add(time.Minute))
	if err != nil {
		t.Fatalf("Sweep (segunda vez): %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("stale = %v, se esperaba vacio (no repetir la transicion)", stale)
	}
}

func TestRedisSweepIgnoraAgentesDentroDelTimeout(t *testing.T) {
	b := newTestRedisBackend(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := b.Touch(ctx, "a1", now); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	stale, err := b.Sweep(ctx, 45*time.Second, now.Add(10*time.Second))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("stale = %v, se esperaba vacio (dentro del timeout)", stale)
	}
}

func TestRedisStateDeAgenteDesconocido(t *testing.T) {
	b := newTestRedisBackend(t)
	state, err := b.State(context.Background(), "fantasma")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state != StateUnknown {
		t.Errorf("state = %v, se esperaba StateUnknown", state)
	}
}

func TestRedisSeedNodoYaCaidoQuedaUnreachableDeInmediato(t *testing.T) {
	b := newTestRedisBackend(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := b.Seed(ctx, "a1", now.Add(-2*time.Hour), now, 45*time.Second); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if state, _ := b.State(ctx, "a1"); state != StateUnreachable {
		t.Errorf("state = %v, se esperaba Unreachable sin esperar a un Sweep", state)
	}

	stale, err := b.Sweep(ctx, 45*time.Second, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("Sweep tras Seed = %v, se esperaba vacio (no duplicar la alerta)", stale)
	}
}

func TestRedisSeedNodoRecienVistoQuedaOnline(t *testing.T) {
	b := newTestRedisBackend(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := b.Seed(ctx, "a1", now.Add(-5*time.Second), now, 45*time.Second); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if state, _ := b.State(ctx, "a1"); state != StateOnline {
		t.Errorf("state = %v, se esperaba Online", state)
	}
}

func TestRedisSeedNuncaRetrocedeUnHeartbeatMasReciente(t *testing.T) {
	b := newTestRedisBackend(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Simula dos replicas compartiendo este Redis: una sigue viendo
	// heartbeats de verdad (Touch) mientras la otra arranca y precarga desde
	// un last_seen_at de la base de datos que quedo rezagado.
	if _, err := b.Touch(ctx, "a1", now); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if err := b.Seed(ctx, "a1", now.Add(-2*time.Hour), now, 45*time.Second); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if state, _ := b.State(ctx, "a1"); state != StateOnline {
		t.Errorf("state = %v, se esperaba que Seed no pisara un heartbeat mas reciente", state)
	}
}

func TestRedisSeedLimiteCoincideConSweep(t *testing.T) {
	// El limite de Seed (>=) debe coincidir con el de Sweep (rango
	// inclusivo de ZRANGEBYSCORE): un heartbeat exactamente en el borde del
	// timeout debe clasificarse igual sea cual sea el camino que lo evalue.
	ctx := context.Background()
	const timeout = 45 * time.Second

	seeded := newTestRedisBackend(t)
	now := time.Now().UTC()
	lastSeen := now.Add(-timeout) // exactamente en el limite

	if err := seeded.Seed(ctx, "a1", lastSeen, now, timeout); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	seededState, _ := seeded.State(ctx, "a1")

	swept := newTestRedisBackend(t)
	if _, err := swept.Touch(ctx, "a1", lastSeen); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if _, err := swept.Sweep(ctx, timeout, now); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	sweptState, _ := swept.State(ctx, "a1")

	if seededState != sweptState {
		t.Errorf("Seed clasifico el limite como %v pero Sweep lo clasifica como %v", seededState, sweptState)
	}
}
