// Package collector reune la telemetria del nodo: metricas del sistema
// operativo via gopsutil y metricas de contenedores via el SDK de Docker.
//
// Los tipos de este paquete son estructuras de dominio deliberadamente libres
// de protobuf: asi se pueden testear sin generar codigo y el mapeo a la API
// vive aislado en internal/telemetry.
package collector

import "time"

// Snapshot es una fotografia completa del nodo en un instante dado.
type Snapshot struct {
	Timestamp  time.Time
	System     SystemSnapshot
	Containers []ContainerSnapshot
}

// SystemSnapshot agrupa las metricas del sistema operativo anfitrion.
type SystemSnapshot struct {
	CPUUsagePercent float64
	MemoryUsed      uint64
	MemoryTotal     uint64
	MemoryAvailable uint64
	SwapUsed        uint64
	SwapTotal       uint64
	// RootDiskPercent es el uso del volumen raiz ("/" o "C:\"), el valor que
	// alimenta el semaforo de la Fleet Grid.
	RootDiskPercent float64
	Disks           []DiskSnapshot
	Network         NetworkSnapshot
	Load            LoadSnapshot
	Uptime          time.Duration
}

// DiskSnapshot describe un punto de montaje real (se descartan los pseudo-fs).
type DiskSnapshot struct {
	Device       string
	Mountpoint   string
	FSType       string
	TotalBytes   uint64
	UsedBytes    uint64
	UsagePercent float64
	ReadBPS      uint64
	WriteBPS     uint64
}

// NetworkSnapshot lleva contadores acumulados y la tasa calculada respecto a
// la lectura anterior del mismo colector.
type NetworkSnapshot struct {
	RxBytesPerSecond uint64
	TxBytesPerSecond uint64
	RxBytesTotal     uint64
	TxBytesTotal     uint64
}

// LoadSnapshot es la carga media del sistema. En Windows queda a cero.
type LoadSnapshot struct {
	Load1  float64
	Load5  float64
	Load15 float64
}

// ContainerSnapshot son las metricas de un contenedor individual.
type ContainerSnapshot struct {
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
	Labels       map[string]string
}
