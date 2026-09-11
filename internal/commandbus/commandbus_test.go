package commandbus

import (
	"context"
	"errors"
	"testing"
	"time"

	fleetpulsev1 "github.com/gdev/fleetpulse/gen/fleetpulse/v1"
)

func TestDispatchFallaSiElAgenteNoEstaConectado(t *testing.T) {
	b := New()
	err := b.Dispatch(context.Background(), "a1", &fleetpulsev1.Command{CommandId: "c1"})
	if !errors.Is(err, ErrAgentOffline) {
		t.Errorf("err = %v, se esperaba ErrAgentOffline", err)
	}
}

func TestDispatchEntregaAlAgenteRegistrado(t *testing.T) {
	b := New()
	incoming, cancel := b.Register("a1")
	defer cancel()

	cmd := &fleetpulsev1.Command{CommandId: "c1"}
	if err := b.Dispatch(context.Background(), "a1", cmd); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	select {
	case got := <-incoming:
		if got.GetCommandId() != "c1" {
			t.Errorf("command_id = %q", got.GetCommandId())
		}
	case <-time.After(time.Second):
		t.Fatal("el comando no llego al canal del agente")
	}
}

func TestDispatchAndWaitDesbloqueaConElResultado(t *testing.T) {
	b := New()
	incoming, cancel := b.Register("a1")
	defer cancel()

	go func() {
		cmd := <-incoming
		b.Complete(&fleetpulsev1.CommandResult{
			AgentId:   "a1",
			CommandId: cmd.GetCommandId(),
			Success:   true,
			LogLines:  []string{"linea 1", "linea 2"},
		})
	}()

	ctx, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()

	result, err := b.DispatchAndWait(ctx, "a1", &fleetpulsev1.Command{CommandId: "c1"})
	if err != nil {
		t.Fatalf("DispatchAndWait: %v", err)
	}
	if !result.GetSuccess() || len(result.GetLogLines()) != 2 {
		t.Errorf("result = %+v", result)
	}
}

func TestDispatchAndWaitRespetaElTimeout(t *testing.T) {
	b := New()
	_, cancel := b.Register("a1")
	defer cancel()

	ctx, done := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer done()

	_, err := b.DispatchAndWait(ctx, "a1", &fleetpulsev1.Command{CommandId: "c1"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, se esperaba DeadlineExceeded", err)
	}
}

func TestCompleteSinReceptorNoBloqueaYDevuelveFalse(t *testing.T) {
	b := New()
	delivered := b.Complete(&fleetpulsev1.CommandResult{CommandId: "huerfano"})
	if delivered {
		t.Error("delivered = true, se esperaba false: nadie esperaba ese comando")
	}
}

func TestRegisterSustituyeCanalPrevioDelMismoAgente(t *testing.T) {
	b := New()
	first, cancelFirst := b.Register("a1")
	defer cancelFirst()

	second, cancelSecond := b.Register("a1")
	defer cancelSecond()

	if err := b.Dispatch(context.Background(), "a1", &fleetpulsev1.Command{CommandId: "c1"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	select {
	case <-first:
		t.Fatal("el canal viejo no deberia recibir nada tras un nuevo Register")
	case <-second:
		// esperado
	case <-time.After(time.Second):
		t.Fatal("el canal nuevo no recibio el comando")
	}
}

func TestConnected(t *testing.T) {
	b := New()
	if b.Connected("a1") {
		t.Error("Connected(a1) = true antes de registrarse")
	}

	_, cancel := b.Register("a1")
	if !b.Connected("a1") {
		t.Error("Connected(a1) = false tras Register")
	}

	cancel()
	if b.Connected("a1") {
		t.Error("Connected(a1) = true tras cancelar el registro")
	}
}
