package heartbeat

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/gdev/fleetpulse/internal/heartbeat/heartbeattest"
)

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWatchdogTouchDisparaRecuperacion(t *testing.T) {
	backend := NewMemoryBackend()
	alerter := &heartbeattest.RecordingAlerter{}
	lookup := func(context.Context, string) (string, error) { return "web-01", nil }

	w := NewWatchdog(backend, alerter, lookup, 45*time.Second, noopLogger())
	ctx := context.Background()

	w.Touch(ctx, "a1")
	if events := alerter.Events(); len(events) != 0 {
		t.Fatalf("primer Touch no deberia alertar, eventos = %+v", events)
	}

	// Forzar Unreachable manipulando el backend directamente (simula que el
	// watchdog de barrido ya lo detecto).
	_, _ = backend.Sweep(ctx, 0, time.Now().UTC().Add(time.Hour))

	w.Touch(ctx, "a1")
	events := alerter.Events()
	if len(events) != 1 || events[0].Kind != "recovered" || events[0].Hostname != "web-01" {
		t.Errorf("events = %+v, se esperaba una recuperacion de web-01", events)
	}
}

func TestWatchdogRunDisparaUnreachable(t *testing.T) {
	// Acelera el ticker de barrido (5s en produccion) para no alargar el test.
	original := sweepInterval
	sweepInterval = 10 * time.Millisecond
	defer func() { sweepInterval = original }()

	backend := NewMemoryBackend()
	alerter := &heartbeattest.RecordingAlerter{}
	lookup := func(context.Context, string) (string, error) { return "db-01", nil }

	// Timeout minimo para que el test no dependa de esperar 45s reales.
	w := NewWatchdog(backend, alerter, lookup, 50*time.Millisecond, noopLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Touch(ctx, "a1")
	go w.Run(ctx)

	deadline := time.After(2 * time.Second)
	for {
		if events := alerter.Events(); len(events) > 0 {
			if events[0].Kind != "unreachable" || events[0].Hostname != "db-01" {
				t.Fatalf("events = %+v, se esperaba un unreachable de db-01", events)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("el watchdog no disparo la alerta de Unreachable a tiempo")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestWatchdogHostnameOfCaeAlAgentID(t *testing.T) {
	backend := NewMemoryBackend()
	alerter := &heartbeattest.RecordingAlerter{}
	lookup := func(context.Context, string) (string, error) { return "", context.DeadlineExceeded }

	w := NewWatchdog(backend, alerter, lookup, 45*time.Second, noopLogger())
	if got := w.hostnameOf(context.Background(), "a1"); got != "a1" {
		t.Errorf("hostnameOf() = %q, se esperaba el agent_id como fallback", got)
	}
}
