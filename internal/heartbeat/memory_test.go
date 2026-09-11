package heartbeat

import (
	"context"
	"testing"
	"time"
)

func TestTouchDevuelveEstadoPrevio(t *testing.T) {
	b := NewMemoryBackend()
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

func TestSweepMarcaUnreachableUnaSolaVez(t *testing.T) {
	b := NewMemoryBackend()
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

	// Un segundo barrido no debe volver a reportar el mismo agente: ya esta
	// Unreachable, no es una transicion nueva.
	stale, err = b.Sweep(ctx, 45*time.Second, afterTimeout.Add(time.Minute))
	if err != nil {
		t.Fatalf("Sweep (segunda vez): %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("stale = %v, se esperaba vacio", stale)
	}
}

func TestSweepIgnoraAgentesDentroDelTimeout(t *testing.T) {
	b := NewMemoryBackend()
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

func TestTouchTrasUnreachableVuelveAOnline(t *testing.T) {
	b := NewMemoryBackend()
	ctx := context.Background()
	now := time.Now().UTC()

	_, _ = b.Touch(ctx, "a1", now)
	_, _ = b.Sweep(ctx, 45*time.Second, now.Add(time.Minute))

	previous, err := b.Touch(ctx, "a1", now.Add(90*time.Second))
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if previous != StateUnreachable {
		t.Errorf("previous = %v, se esperaba StateUnreachable", previous)
	}

	state, _ := b.State(ctx, "a1")
	if state != StateOnline {
		t.Errorf("state = %v, se esperaba StateOnline tras el nuevo heartbeat", state)
	}
}

func TestStateDeAgenteDesconocido(t *testing.T) {
	b := NewMemoryBackend()
	state, err := b.State(context.Background(), "fantasma")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state != StateUnknown {
		t.Errorf("state = %v, se esperaba StateUnknown", state)
	}
}
