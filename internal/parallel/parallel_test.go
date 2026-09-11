package parallel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestForEachProcesaTodosLosElementos(t *testing.T) {
	items := make([]int, 100)
	for i := range items {
		items[i] = i
	}

	var sum atomic.Int64
	ForEach(context.Background(), 8, items, func(i int) {
		sum.Add(int64(i))
	})

	want := int64(100 * 99 / 2)
	if got := sum.Load(); got != want {
		t.Errorf("sum = %d, se esperaba %d", got, want)
	}
}

func TestForEachRespetaElLimiteDeWorkers(t *testing.T) {
	items := make([]int, 20)
	var concurrent, maxConcurrent atomic.Int64

	ForEach(context.Background(), 3, items, func(int) {
		n := concurrent.Add(1)
		defer concurrent.Add(-1)
		for {
			m := maxConcurrent.Load()
			if n <= m || maxConcurrent.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(time.Millisecond)
	})

	if got := maxConcurrent.Load(); got > 3 {
		t.Errorf("concurrencia maxima observada = %d, se esperaba <= 3", got)
	}
}

func TestForEachSinElementosNoFalla(t *testing.T) {
	called := false
	ForEach(context.Background(), 4, []int{}, func(int) { called = true })
	if called {
		t.Error("fn no deberia haberse llamado sin elementos")
	}
}

func TestForEachSeParaAlCancelarElContexto(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	items := make([]int, 1000)

	var processed atomic.Int64
	done := make(chan struct{})
	go func() {
		ForEach(ctx, 2, items, func(int) {
			processed.Add(1)
			time.Sleep(time.Millisecond)
		})
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ForEach no termino tras cancelar el contexto")
	}

	if got := processed.Load(); got >= 1000 {
		t.Errorf("processed = %d, se esperaba que la cancelacion cortase antes de procesar todo", got)
	}
}

func TestForEachMasWorkersQueElementos(t *testing.T) {
	var count atomic.Int64
	ForEach(context.Background(), 100, []int{1, 2, 3}, func(int) { count.Add(1) })
	if got := count.Load(); got != 3 {
		t.Errorf("count = %d, se esperaba 3", got)
	}
}
