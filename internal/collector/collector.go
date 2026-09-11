package collector

import (
	"context"
	"log/slog"
	"time"
)

// ContainerSource es la parte del inspector de contenedores de la que depende
// el agregador. Tenerlo como interfaz permite probar el Collector sin daemon y
// deja sitio al inspector de Kubelet de la Fase 4.
type ContainerSource interface {
	Containers(ctx context.Context) ([]ContainerSnapshot, error)
	Close() error
}

// Collector agrega las fuentes de telemetria del nodo en una sola muestra.
type Collector struct {
	system     *SystemCollector
	containers ContainerSource
	log        *slog.Logger
}

// New construye el agregador. `containers` puede ser nil cuando el nodo no
// expone un runtime de contenedores: el agente sigue reportando el sistema.
func New(containers ContainerSource, log *slog.Logger) *Collector {
	return &Collector{
		system:     NewSystemCollector(),
		containers: containers,
		log:        log,
	}
}

// Collect toma una muestra completa. Un fallo del runtime de contenedores se
// registra pero no invalida las metricas de sistema, que son las que deciden
// si el nodo sigue vivo.
func (c *Collector) Collect(ctx context.Context) (Snapshot, error) {
	snap := Snapshot{Timestamp: time.Now().UTC()}

	system, err := c.system.Collect(ctx)
	if err != nil {
		return snap, err
	}
	snap.System = system

	if c.containers != nil {
		containers, err := c.containers.Containers(ctx)
		if err != nil {
			c.log.Warn("no se pudieron leer los contenedores", "error", err)
		} else {
			snap.Containers = containers
		}
	}

	return snap, nil
}

// Close libera las fuentes subyacentes.
func (c *Collector) Close() error {
	if c.containers == nil {
		return nil
	}
	return c.containers.Close()
}
