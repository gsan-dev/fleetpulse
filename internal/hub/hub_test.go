package hub

import (
	"testing"
	"time"
)

func TestPublishEntregaATodosLosSuscriptoresDelTema(t *testing.T) {
	h := New[string]()

	ch1, cancel1 := h.Subscribe("a1")
	defer cancel1()
	ch2, cancel2 := h.Subscribe("a1")
	defer cancel2()
	other, cancelOther := h.Subscribe("a2")
	defer cancelOther()

	h.Publish("a1", "hola")

	for _, ch := range []<-chan string{ch1, ch2} {
		select {
		case got := <-ch:
			if got != "hola" {
				t.Errorf("got = %q", got)
			}
		case <-time.After(time.Second):
			t.Fatal("no llego el evento a un suscriptor de a1")
		}
	}

	select {
	case got := <-other:
		t.Fatalf("el suscriptor de a2 no deberia recibir nada, recibio %q", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCancelDejaDeEntregarEventos(t *testing.T) {
	h := New[int]()
	ch, cancel := h.Subscribe("a1")

	h.Publish("a1", 1)
	<-ch

	cancel()
	h.Publish("a1", 2) // no debe bloquear ni entrar en panico

	if _, ok := <-ch; ok {
		t.Error("el canal deberia estar cerrado tras cancel()")
	}
}

func TestPublishNoBloqueaConSuscriptorLento(t *testing.T) {
	h := New[int]()
	_, cancel := h.Subscribe("a1")
	defer cancel()

	done := make(chan struct{})
	go func() {
		// Publicar muchas mas veces que el buffer no debe bloquearse nunca:
		// Publish descarta en vez de esperar a un lector.
		for i := 0; i < bufferSize*4; i++ {
			h.Publish("a1", i)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish se bloqueo con un suscriptor que no lee")
	}
}

func TestPublishSinSuscriptoresNoFalla(t *testing.T) {
	h := New[int]()
	h.Publish("nadie-escucha", 42) // no debe entrar en panico
}
