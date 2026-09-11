package heartbeat

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type recordedAlert struct {
	kind     string
	agentID  string
	hostname string
}

type fakeAlerter struct {
	mu     sync.Mutex
	events []recordedAlert
}

func (f *fakeAlerter) NotifyUnreachable(_ context.Context, agentID, hostname string, _ time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, recordedAlert{kind: "unreachable", agentID: agentID, hostname: hostname})
}

func (f *fakeAlerter) NotifyRecovered(_ context.Context, agentID, hostname string, _ time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, recordedAlert{kind: "recovered", agentID: agentID, hostname: hostname})
}

func (f *fakeAlerter) snapshot() []recordedAlert {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedAlert(nil), f.events...)
}

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWatchdogTouchDisparaRecuperacion(t *testing.T) {
	backend := NewMemoryBackend()
	alerter := &fakeAlerter{}
	lookup := func(context.Context, string) (string, error) { return "web-01", nil }

	w := NewWatchdog(backend, alerter, lookup, 45*time.Second, noopLogger())
	ctx := context.Background()

	w.Touch(ctx, "a1")
	if events := alerter.snapshot(); len(events) != 0 {
		t.Fatalf("primer Touch no deberia alertar, eventos = %+v", events)
	}

	// Forzar Unreachable manipulando el backend directamente (simula que el
	// watchdog de barrido ya lo detecto).
	_, _ = backend.Sweep(ctx, 0, time.Now().UTC().Add(time.Hour))

	w.Touch(ctx, "a1")
	events := alerter.snapshot()
	if len(events) != 1 || events[0].kind != "recovered" || events[0].hostname != "web-01" {
		t.Errorf("events = %+v, se esperaba una recuperacion de web-01", events)
	}
}

func TestWatchdogRunDisparaUnreachable(t *testing.T) {
	// Acelera el ticker de barrido (5s en produccion) para no alargar el test.
	original := sweepInterval
	sweepInterval = 10 * time.Millisecond
	defer func() { sweepInterval = original }()

	backend := NewMemoryBackend()
	alerter := &fakeAlerter{}
	lookup := func(context.Context, string) (string, error) { return "db-01", nil }

	// Timeout minimo para que el test no dependa de esperar 45s reales.
	w := NewWatchdog(backend, alerter, lookup, 50*time.Millisecond, noopLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Touch(ctx, "a1")
	go w.Run(ctx)

	deadline := time.After(2 * time.Second)
	for {
		if events := alerter.snapshot(); len(events) > 0 {
			if events[0].kind != "unreachable" || events[0].hostname != "db-01" {
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
	alerter := &fakeAlerter{}
	lookup := func(context.Context, string) (string, error) { return "", context.DeadlineExceeded }

	w := NewWatchdog(backend, alerter, lookup, 45*time.Second, noopLogger())
	if got := w.hostnameOf(context.Background(), "a1"); got != "a1" {
		t.Errorf("hostnameOf() = %q, se esperaba el agent_id como fallback", got)
	}
}
