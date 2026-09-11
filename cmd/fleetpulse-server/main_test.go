package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/gdev/fleetpulse/internal/heartbeat"
	"github.com/gdev/fleetpulse/internal/heartbeat/heartbeattest"
	"github.com/gdev/fleetpulse/internal/store"
	"github.com/gdev/fleetpulse/internal/store/memstore"
)

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const testTimeout = 45 * time.Second

// TestSeedHeartbeatsDetectaNodosRealmenteCaidos reproduce el bug real
// observado en produccion: tras reiniciar el servidor (o Redis), un nodo que
// lleva horas sin reportar se quedaba marcado "Saludable" para siempre,
// porque el backend de heartbeat arrancaba vacio y heartbeat.Sweep solo
// puede marcar Unreachable a un agente que tiene entrada.
//
// El "ahora" del sembrado se pasa como parametro (no se usa el reloj real)
// precisamente para poder expresar aqui "esto pasa mucho despues del
// registro" sin pelear con RegisteredAt, que UpsertNode fija siempre al
// reloj real de cuando corre el test.
func TestSeedHeartbeatsDetectaNodosRealmenteCaidos(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()

	if err := st.UpsertNode(ctx, store.Node{AgentID: "vivo", Hostname: "web-01"}); err != nil {
		t.Fatalf("UpsertNode vivo: %v", err)
	}
	if err := st.UpsertNode(ctx, store.Node{AgentID: "caido", Hostname: "db-01"}); err != nil {
		t.Fatalf("UpsertNode caido: %v", err)
	}

	seedNow := time.Now().UTC().Add(3 * time.Hour)
	if err := st.TouchNode(ctx, "vivo", seedNow.Add(-5*time.Second)); err != nil {
		t.Fatalf("TouchNode vivo: %v", err)
	}
	if err := st.TouchNode(ctx, "caido", seedNow.Add(-2*time.Hour)); err != nil {
		t.Fatalf("TouchNode caido: %v", err)
	}

	// Backend nuevo y vacio, como tras un reinicio del proceso.
	backend := heartbeat.NewMemoryBackend()
	seedHeartbeats(ctx, st, backend, seedNow, testTimeout, noopLogger())

	// El nodo caido debe quedar Unreachable desde el primer instante, sin
	// necesidad de que corra ningun Sweep todavia.
	caidoState, _ := backend.State(ctx, "caido")
	if caidoState != heartbeat.StateUnreachable {
		t.Errorf("estado de 'caido' tras Seed = %v, se esperaba Unreachable de inmediato", caidoState)
	}
	vivoState, _ := backend.State(ctx, "vivo")
	if vivoState != heartbeat.StateOnline {
		t.Errorf("estado de 'vivo' tras Seed = %v, se esperaba Online", vivoState)
	}

	// Y un Sweep posterior no debe volver a reportarlo: ya estaba
	// Unreachable antes de este barrido, no es una transicion nueva (si lo
	// fuera, dispararia una alerta duplicada para algo que ya se sabia).
	newlyUnreachable, err := backend.Sweep(ctx, testTimeout, seedNow)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(newlyUnreachable) != 0 {
		t.Errorf("Sweep tras Seed = %v, se esperaba vacio (no es una transicion nueva)", newlyUnreachable)
	}
}

func TestSeedHeartbeatsSinNodosNoFalla(t *testing.T) {
	seedHeartbeats(context.Background(), memstore.New(), heartbeat.NewMemoryBackend(), time.Now().UTC(), testTimeout, noopLogger())
}

// TestSeedHeartbeatsIgnoraNodoSinHeartbeatPrevio cubre un nodo recien
// registrado (Register ya se llamo, UpsertNode existe) pero que todavia no
// mando su primera rafaga de metricas: LastSeenAt == RegisteredAt (memstore y
// pgstore arrancan igual los dos en el registro; ver memstore.UpsertNode).
// Sembrarlo igualmente lo marcaria Unreachable de inmediato, y su primer
// heartbeat de verdad dispararia una alerta de "recuperado" para un nodo que
// nunca estuvo caido. Una version anterior de este fix comprobaba
// LastSeenAt.IsZero(), una condicion que solo se da en memstore -en pgstore
// la columna es NOT NULL DEFAULT now() y nunca queda vacia- asi que este
// test es la regresion real de ese bug, no solo un caso de memstore.
func TestSeedHeartbeatsIgnoraNodoSinHeartbeatPrevio(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if err := st.UpsertNode(ctx, store.Node{AgentID: "nuevo", Hostname: "recien-instalado"}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	backend := heartbeat.NewMemoryBackend()
	seedHeartbeats(ctx, st, backend, time.Now().UTC(), testTimeout, noopLogger())

	state, _ := backend.State(ctx, "nuevo")
	if state != heartbeat.StateUnknown {
		t.Errorf("state = %v, se esperaba StateUnknown (sin heartbeat previo que precargar)", state)
	}

	// Su primer heartbeat real no debe leerse como una "recuperacion".
	watchdogAlerter := &heartbeattest.RecordingAlerter{}
	w := heartbeat.NewWatchdog(backend, watchdogAlerter, func(context.Context, string) (string, error) {
		return "recien-instalado", nil
	}, testTimeout, noopLogger())
	w.Touch(ctx, "nuevo")

	if events := watchdogAlerter.Events(); len(events) != 0 {
		t.Errorf("eventos tras el primer heartbeat = %+v, se esperaba ninguno", events)
	}
}

// TestSeedHeartbeatsIgnoraDesajusteDeRelojDelAgente cubre el caso que
// motivo cambiar de "LastSeenAt <= RegisteredAt" a un campo HasHeartbeat
// explicito: un agente cuyo reloj va detras del servidor produce
// LastSeenAt anterior a RegisteredAt en cada heartbeat real, para siempre.
// Con la version anterior (basada en timestamps) este nodo nunca se
// sembraria, y si de verdad se cae quedaria en StateUnknown en vez de
// Unreachable.
func TestSeedHeartbeatsIgnoraDesajusteDeRelojDelAgente(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()

	if err := st.UpsertNode(ctx, store.Node{AgentID: "reloj-atrasado", Hostname: "vm-vieja"}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	node, err := st.GetNode(ctx, "reloj-atrasado")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}

	// El agente reporta un timestamp muy anterior a RegisteredAt (reloj
	// atrasado), pero es un heartbeat real: TouchNode debe marcarlo como tal
	// pase lo que pase con la comparacion de timestamps.
	if err := st.TouchNode(ctx, "reloj-atrasado", node.RegisteredAt.Add(-24*time.Hour)); err != nil {
		t.Fatalf("TouchNode: %v", err)
	}

	backend := heartbeat.NewMemoryBackend()
	seedHeartbeats(ctx, st, backend, time.Now().UTC(), testTimeout, noopLogger())

	// Al llevar mas de 24h sin un heartbeat reciente (segun el propio reloj
	// desajustado del agente), lo correcto es Unreachable -no StateUnknown,
	// que es lo que saldria si se hubiera saltado el sembrado por error-.
	state, _ := backend.State(ctx, "reloj-atrasado")
	if state != heartbeat.StateUnreachable {
		t.Errorf("state = %v, se esperaba Unreachable (el nodo si tuvo un heartbeat real)", state)
	}
}
