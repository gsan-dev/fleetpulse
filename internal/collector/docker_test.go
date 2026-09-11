package collector

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestCPUPercent(t *testing.T) {
	tests := []struct {
		name  string
		stats container.StatsResponse
		want  float64
	}{
		{
			name: "dos nucleos al cincuenta por ciento",
			stats: container.StatsResponse{
				CPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 200},
					SystemUsage: 400,
					OnlineCPUs:  2,
				},
				PreCPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 100},
					SystemUsage: 200,
				},
			},
			want: 100,
		},
		{
			name: "nucleos deducidos de percpu",
			stats: container.StatsResponse{
				CPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 150, PercpuUsage: []uint64{75, 75}},
					SystemUsage: 1000,
				},
				PreCPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 100},
					SystemUsage: 500,
				},
			},
			want: 20,
		},
		{
			// Primera trama de un stream: precpu a cero deja el delta de
			// sistema en su valor absoluto y el resultado no es fiable, pero
			// nunca debe ser negativo ni infinito.
			name: "sin muestra previa",
			stats: container.StatsResponse{
				CPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 100},
					SystemUsage: 0,
					OnlineCPUs:  4,
				},
			},
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cpuPercent(tc.stats); got != tc.want {
				t.Errorf("cpuPercent() = %v, se esperaba %v", got, tc.want)
			}
		})
	}
}

func TestMemoryUsageDescuentaCache(t *testing.T) {
	tests := []struct {
		name  string
		stats container.MemoryStats
		want  uint64
	}{
		{
			name:  "cgroup v2",
			stats: container.MemoryStats{Usage: 1000, Stats: map[string]uint64{"inactive_file": 300}},
			want:  700,
		},
		{
			name:  "cgroup v1",
			stats: container.MemoryStats{Usage: 1000, Stats: map[string]uint64{"cache": 250}},
			want:  750,
		},
		{
			name:  "sin desglose",
			stats: container.MemoryStats{Usage: 1000},
			want:  1000,
		},
		{
			name:  "cache mayor que el uso",
			stats: container.MemoryStats{Usage: 100, Stats: map[string]uint64{"inactive_file": 500}},
			want:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := memoryUsage(container.StatsResponse{MemoryStats: tc.stats})
			if got != tc.want {
				t.Errorf("memoryUsage() = %d, se esperaba %d", got, tc.want)
			}
		})
	}
}

func TestContainerName(t *testing.T) {
	if got := containerName([]string{"/nginx", "/otro"}); got != "nginx" {
		t.Errorf("containerName() = %q, se esperaba \"nginx\"", got)
	}
	if got := containerName(nil); got != "" {
		t.Errorf("containerName(nil) = %q, se esperaba cadena vacia", got)
	}
}

func TestShortID(t *testing.T) {
	if got := shortID("0123456789abcdef0123"); got != "0123456789ab" {
		t.Errorf("shortID() = %q", got)
	}
	if got := shortID("abc"); got != "abc" {
		t.Errorf("shortID() = %q", got)
	}
}
