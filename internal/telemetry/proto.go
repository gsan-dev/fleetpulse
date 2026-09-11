// Package telemetry traduce las estructuras de dominio del colector a los
// mensajes protobuf que viajan por gRPC. Es la unica frontera donde el codigo
// del agente toca el paquete generado.
package telemetry

import (
	"fmt"
	"sort"

	fleetpulsev1 "github.com/gdev/fleetpulse/gen/fleetpulse/v1"
	"github.com/gdev/fleetpulse/internal/collector"
	"github.com/gdev/fleetpulse/internal/identity"
)

// NodeInfo traduce la huella del nodo para la llamada Register.
func NodeInfo(node *identity.Node) *fleetpulsev1.NodeInfo {
	if node == nil {
		return nil
	}
	return &fleetpulsev1.NodeInfo{
		Hostname:         node.Hostname,
		Os:               node.OS,
		Platform:         node.Platform,
		PlatformVersion:  node.PlatformVersion,
		KernelVersion:    node.KernelVersion,
		Arch:             node.Arch,
		LocalIp:          node.LocalIP,
		AgentVersion:     node.AgentVersion,
		CpuCores:         node.CPUCores,
		MemoryTotalBytes: node.MemoryTotal,
		BootTime:         node.BootTime.Unix(),
	}
}

// MetricPayload traduce una muestra del colector. El timestamp viaja en
// milisegundos unix, que es la unidad que espera TimescaleDB en la ingesta.
func MetricPayload(agentID string, snap collector.Snapshot) *fleetpulsev1.MetricPayload {
	return &fleetpulsev1.MetricPayload{
		AgentId:    agentID,
		Timestamp:  snap.Timestamp.UnixMilli(),
		System:     systemMetrics(snap.System),
		Containers: containerMetrics(snap.Containers),
	}
}

func systemMetrics(s collector.SystemSnapshot) *fleetpulsev1.SystemMetrics {
	metrics := &fleetpulsev1.SystemMetrics{
		CpuUsagePercent:      float32(s.CPUUsagePercent),
		MemoryUsedBytes:      s.MemoryUsed,
		MemoryTotalBytes:     s.MemoryTotal,
		DiskUsagePercent:     float32(s.RootDiskPercent),
		MemoryAvailableBytes: s.MemoryAvailable,
		SwapUsedBytes:        s.SwapUsed,
		SwapTotalBytes:       s.SwapTotal,
		UptimeSeconds:        uint64(s.Uptime.Seconds()),
		Network: &fleetpulsev1.NetworkMetrics{
			RxBytesPerSecond: s.Network.RxBytesPerSecond,
			TxBytesPerSecond: s.Network.TxBytesPerSecond,
			RxBytesTotal:     s.Network.RxBytesTotal,
			TxBytesTotal:     s.Network.TxBytesTotal,
		},
		Load: &fleetpulsev1.LoadAverage{
			Load1:  float32(s.Load.Load1),
			Load5:  float32(s.Load.Load5),
			Load15: float32(s.Load.Load15),
		},
	}

	metrics.Disks = make([]*fleetpulsev1.DiskMetrics, 0, len(s.Disks))
	for _, d := range s.Disks {
		metrics.Disks = append(metrics.Disks, &fleetpulsev1.DiskMetrics{
			Device:              d.Device,
			Mountpoint:          d.Mountpoint,
			Fstype:              d.FSType,
			TotalBytes:          d.TotalBytes,
			UsedBytes:           d.UsedBytes,
			UsagePercent:        float32(d.UsagePercent),
			ReadBytesPerSecond:  d.ReadBPS,
			WriteBytesPerSecond: d.WriteBPS,
		})
	}

	return metrics
}

func containerMetrics(containers []collector.ContainerSnapshot) []*fleetpulsev1.ContainerMetrics {
	out := make([]*fleetpulsev1.ContainerMetrics, 0, len(containers))
	for _, c := range containers {
		out = append(out, &fleetpulsev1.ContainerMetrics{
			Id:               c.ID,
			Name:             c.Name,
			Image:            c.Image,
			Status:           c.Status,
			State:            c.State,
			CpuPercent:       float32(c.CPUPercent),
			MemoryBytes:      c.MemoryBytes,
			MemoryLimitBytes: c.MemoryLimit,
			StartedAt:        c.StartedAt.Unix(),
			RestartCount:     uint32(c.RestartCount),
			Network: &fleetpulsev1.ContainerNetwork{
				RxBytes: c.RxBytes,
				TxBytes: c.TxBytes,
			},
			Labels: flattenLabels(c.Labels),
		})
	}
	return out
}

// flattenLabels aplana el mapa de etiquetas a pares "clave=valor" ordenados,
// para que dos muestras identicas produzcan el mismo payload y el servidor
// pueda deduplicar sin normalizar nada.
func flattenLabels(labels map[string]string) []string {
	if len(labels) == 0 {
		return nil
	}
	out := make([]string, 0, len(labels))
	for k, v := range labels {
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(out)
	return out
}
