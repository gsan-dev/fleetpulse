// Package api expone la telemetria persistida y el estado de la flota como
// una API HTTP (REST + Server-Sent Events) para el dashboard de Next.js.
package api

import (
	"time"

	fleetpulsev1 "github.com/gdev/fleetpulse/gen/fleetpulse/v1"
	"github.com/gdev/fleetpulse/internal/heartbeat"
	"github.com/gdev/fleetpulse/internal/store"
)

// Health es el color de la Fleet Grid: Healthy (verde), Warning (amarillo,
// recursos altos) o Unreachable (rojo, heartbeat vencido).
type Health string

const (
	HealthHealthy     Health = "healthy"
	HealthWarning     Health = "warning"
	HealthUnreachable Health = "unreachable"
	HealthUnknown     Health = "unknown" // registrado pero sin metricas todavia
)

// Umbrales de "Alerta de Recursos". Fijos por ahora; configurables seria una
// mejora natural de una fase posterior si hace falta ajustarlos por nodo.
const (
	cpuWarningPercent  = 85.0
	diskWarningPercent = 90.0
	memWarningPercent  = 90.0
)

// NodeSummary es la fila de la Fleet Grid.
type NodeSummary struct {
	AgentID      string     `json:"agent_id"`
	Hostname     string     `json:"hostname"`
	OS           string     `json:"os"`
	Platform     string     `json:"platform"`
	Arch         string     `json:"arch"`
	LocalIP      string     `json:"local_ip"`
	PublicIP     string     `json:"public_ip"`
	AgentVersion string     `json:"agent_version"`
	CPUCores     uint32     `json:"cpu_cores"`
	MemoryTotal  uint64     `json:"memory_total_bytes"`
	RegisteredAt time.Time  `json:"registered_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	Health       Health     `json:"health"`
	Metric       *MetricDTO `json:"latest_metric,omitempty"`
}

// NodeDetail amplia NodeSummary con lo que solo hace falta en la vista de un
// nodo concreto.
type NodeDetail struct {
	NodeSummary
	KernelVersion   string `json:"kernel_version"`
	PlatformVersion string `json:"platform_version"`
}

// MetricDTO es una muestra de sistema en el formato que consume el dashboard
// (Recharts espera numeros simples y una marca de tiempo serializable).
type MetricDTO struct {
	Timestamp        time.Time `json:"timestamp"`
	CPUUsagePercent  float64   `json:"cpu_usage_percent"`
	MemoryUsedBytes  uint64    `json:"memory_used_bytes"`
	MemoryTotalBytes uint64    `json:"memory_total_bytes"`
	DiskUsagePercent float64   `json:"disk_usage_percent"`
	RxBytesPerSecond uint64    `json:"rx_bytes_per_second"`
	TxBytesPerSecond uint64    `json:"tx_bytes_per_second"`
	Load1            float64   `json:"load1"`
}

// ContainerDTO es un contenedor en el formato que consume el dashboard.
type ContainerDTO struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Image        string    `json:"image"`
	Status       string    `json:"status"`
	State        string    `json:"state"`
	CPUPercent   float64   `json:"cpu_percent"`
	MemoryBytes  uint64    `json:"memory_bytes"`
	MemoryLimit  uint64    `json:"memory_limit_bytes"`
	StartedAt    time.Time `json:"started_at"`
	RestartCount int       `json:"restart_count"`
	RxBytes      uint64    `json:"rx_bytes"`
	TxBytes      uint64    `json:"tx_bytes"`
}

func metricDTOFromPoint(p store.MetricPoint) MetricDTO {
	return MetricDTO{
		Timestamp:        p.Timestamp,
		CPUUsagePercent:  p.CPUUsagePercent,
		MemoryUsedBytes:  p.MemoryUsedBytes,
		MemoryTotalBytes: p.MemoryTotalBytes,
		DiskUsagePercent: p.DiskUsagePercent,
		RxBytesPerSecond: p.RxBytesPerSecond,
		TxBytesPerSecond: p.TxBytesPerSecond,
		Load1:            p.Load1,
	}
}

func metricDTOFromProto(m *fleetpulsev1.MetricPayload) MetricDTO {
	sys := m.GetSystem()
	dto := MetricDTO{
		Timestamp:        time.UnixMilli(m.GetTimestamp()).UTC(),
		CPUUsagePercent:  float64(sys.GetCpuUsagePercent()),
		MemoryUsedBytes:  sys.GetMemoryUsedBytes(),
		MemoryTotalBytes: sys.GetMemoryTotalBytes(),
		DiskUsagePercent: float64(sys.GetDiskUsagePercent()),
	}
	if n := sys.GetNetwork(); n != nil {
		dto.RxBytesPerSecond = n.GetRxBytesPerSecond()
		dto.TxBytesPerSecond = n.GetTxBytesPerSecond()
	}
	if l := sys.GetLoad(); l != nil {
		dto.Load1 = float64(l.GetLoad1())
	}
	return dto
}

func containerDTOFromStore(c store.Container) ContainerDTO {
	return ContainerDTO{
		ID:           c.ID,
		Name:         c.Name,
		Image:        c.Image,
		Status:       c.Status,
		State:        c.State,
		CPUPercent:   c.CPUPercent,
		MemoryBytes:  c.MemoryBytes,
		MemoryLimit:  c.MemoryLimit,
		StartedAt:    c.StartedAt,
		RestartCount: c.RestartCount,
		RxBytes:      c.RxBytes,
		TxBytes:      c.TxBytes,
	}
}

func containerDTOFromProto(c *fleetpulsev1.ContainerMetrics) ContainerDTO {
	return ContainerDTO{
		ID:           c.GetId(),
		Name:         c.GetName(),
		Image:        c.GetImage(),
		Status:       c.GetStatus(),
		State:        c.GetState(),
		CPUPercent:   float64(c.GetCpuPercent()),
		MemoryBytes:  c.GetMemoryBytes(),
		MemoryLimit:  c.GetMemoryLimitBytes(),
		StartedAt:    time.Unix(c.GetStartedAt(), 0).UTC(),
		RestartCount: int(c.GetRestartCount()),
		RxBytes:      c.GetNetwork().GetRxBytes(),
		TxBytes:      c.GetNetwork().GetTxBytes(),
	}
}

// computeHealth deriva el color de la Fleet Grid a partir del estado de
// heartbeat y, si el nodo esta online, de lo ajustada que va su ultima
// muestra de recursos.
func computeHealth(state heartbeat.State, metric *MetricDTO) Health {
	if state == heartbeat.StateUnreachable {
		return HealthUnreachable
	}
	if metric == nil {
		return HealthUnknown
	}

	memPercent := 0.0
	if metric.MemoryTotalBytes > 0 {
		memPercent = float64(metric.MemoryUsedBytes) / float64(metric.MemoryTotalBytes) * 100
	}

	if metric.CPUUsagePercent >= cpuWarningPercent || metric.DiskUsagePercent >= diskWarningPercent || memPercent >= memWarningPercent {
		return HealthWarning
	}
	return HealthHealthy
}
