// Package parallel ofrece un unico ayudante para repartir trabajo entre un
// numero acotado de goroutines. Antes de este paquete, el mismo patron
// canal+WaitGroup estaba duplicado a mano en el inspector de Docker y en la
// precarga de heartbeat del servidor; un fix (p.ej. soporte de cancelacion)
// aplicado a una copia era facil de olvidar en la otra.
package parallel

import (
	"context"
	"sync"
)

// ForEach ejecuta fn(item) para cada elemento de items, repartido entre como
// mucho `workers` goroutines a la vez. Deja de alimentar trabajo nuevo en
// cuanto ctx se cancela (lo ya lanzado termina); bloquea hasta que todo el
// trabajo en marcha acaba.
func ForEach[T any](ctx context.Context, workers int, items []T, fn func(T)) {
	if len(items) == 0 {
		return
	}
	if workers < 1 {
		workers = 1
	}
	if workers > len(items) {
		workers = len(items)
	}

	jobs := make(chan T)
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				fn(item)
			}
		}()
	}

feed:
	for _, item := range items {
		select {
		case jobs <- item:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
}
