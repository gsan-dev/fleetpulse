// Package store define el contrato de persistencia del servidor y los tipos
// de dominio que cruzan esa frontera. Dos implementaciones lo satisfacen:
// memstore (todo en el proceso, para pruebas y demos sin infraestructura) y
// pgstore (PostgreSQL/TimescaleDB, para produccion).
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound se devuelve cuando el recurso pedido no existe.
var ErrNotFound = errors.New("store: recurso no encontrado")

// Node es la identidad y ultima huella conocida de un nodo registrado.
type Node struct {
	AgentID         string
	Hostname        string
	OS              string
	Platform        string
	PlatformVersion string
	KernelVersion   string
	Arch            string
	LocalIP         string
	PublicIP        string
	AgentVersion    string
	CPUCores        uint32
	MemoryTotal     uint64
	BootTime        time.Time
	RegisteredAt    time.Time
	LastSeenAt      time.Time
}

// MetricPoint es una muestra de sistema ya aplanada para series temporales.
type MetricPoint struct {
	AgentID          string
	Timestamp        time.Time
	CPUUsagePercent  float64
	MemoryUsedBytes  uint64
	MemoryTotalBytes uint64
	DiskUsagePercent float64
	RxBytesPerSecond uint64
	TxBytesPerSecond uint64
	Load1            float64
}

// Container es el ultimo estado conocido de un contenedor en un nodo.
type Container struct {
	AgentID      string
	ID           string
	Name         string
	Image        string
	Status       string
	State        string
	CPUPercent   float64
	MemoryBytes  uint64
	MemoryLimit  uint64
	StartedAt    time.Time
	RestartCount int
	RxBytes      uint64
	TxBytes      uint64
	UpdatedAt    time.Time
}

// Store agrega toda la persistencia que necesita el servidor. Mantenerla
// como una sola interfaz (en vez de una por tabla) simplifica la inyeccion en
// el resto de paquetes; las implementaciones internamente pueden repartir el
// trabajo entre varios ficheros.
type Store interface {
	// UpsertNode crea o actualiza la ficha de un nodo. No toca LastSeenAt:
	// eso lo gobierna el heartbeat, que vive en un almacen aparte (memoria o
	// Redis) por ser de escritura mucho mas frecuente.
	UpsertNode(ctx context.Context, node Node) error
	GetNode(ctx context.Context, agentID string) (Node, error)
	ListNodes(ctx context.Context) ([]Node, error)
	// TouchNode actualiza solo LastSeenAt, para persistir el heartbeat sin
	// reescribir toda la ficha del nodo en cada rafaga de metricas.
	TouchNode(ctx context.Context, agentID string, at time.Time) error

	InsertMetric(ctx context.Context, point MetricPoint) error
	// LatestMetric es la muestra mas reciente de un nodo: alimenta el color
	// de "Alerta de Recursos" en la Fleet Grid sin consultar el historico.
	LatestMetric(ctx context.Context, agentID string) (MetricPoint, error)
	// QueryRange devuelve las muestras de un nodo entre dos instantes,
	// ordenadas cronologicamente.
	QueryRange(ctx context.Context, agentID string, from, to time.Time) ([]MetricPoint, error)
	// PruneMetrics borra muestras anteriores a `before`. En memstore evita un
	// crecimiento sin limite; en pgstore es un TODO delegado a la politica de
	// retencion nativa de TimescaleDB (ver internal/store/pgstore).
	PruneMetrics(ctx context.Context, before time.Time) error

	// SetContainers reemplaza el snapshot de contenedores de un nodo por el
	// recibido en la ultima rafaga.
	SetContainers(ctx context.Context, agentID string, containers []Container) error
	ListContainers(ctx context.Context, agentID string) ([]Container, error)

	Close() error
}
