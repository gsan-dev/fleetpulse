package collector

import (
	"context"
	"runtime"
	"testing"
)

func TestPerSecond(t *testing.T) {
	tests := []struct {
		name              string
		current, previous uint64
		seconds           float64
		want              uint64
	}{
		{name: "tasa normal", current: 2000, previous: 1000, seconds: 2, want: 500},
		{name: "contador reiniciado", current: 10, previous: 1000, seconds: 2, want: 0},
		{name: "sin tiempo transcurrido", current: 2000, previous: 1000, seconds: 0, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := perSecond(tc.current, tc.previous, tc.seconds); got != tc.want {
				t.Errorf("perSecond() = %d, se esperaba %d", got, tc.want)
			}
		})
	}
}

func TestDeviceKey(t *testing.T) {
	if got := deviceKey("/dev/sda1"); got != "sda1" {
		t.Errorf("deviceKey() = %q", got)
	}
	if got := deviceKey("C:"); got != "C:" {
		t.Errorf("deviceKey() = %q", got)
	}
}

func TestRootUsageCaeAlMasLleno(t *testing.T) {
	disks := []DiskSnapshot{
		{Mountpoint: "/data", UsagePercent: 91},
		{Mountpoint: "/boot", UsagePercent: 40},
	}
	if got := rootUsage(disks); got != 91 {
		t.Errorf("rootUsage() = %v, se esperaba 91", got)
	}
	if got := rootUsage(nil); got != 0 {
		t.Errorf("rootUsage(nil) = %v, se esperaba 0", got)
	}
}

func TestRootUsagePrefiereElVolumenDeSistema(t *testing.T) {
	if runtime.GOOS == "windows" {
		disks := []DiskSnapshot{
			{Mountpoint: "D:", UsagePercent: 95},
			{Mountpoint: "C:", UsagePercent: 40},
		}
		if got := rootUsage(disks); got != 40 {
			t.Errorf("rootUsage() = %v, se esperaba el volumen C: (40)", got)
		}
		return
	}

	disks := []DiskSnapshot{
		{Mountpoint: "/data", UsagePercent: 95},
		{Mountpoint: "/", UsagePercent: 40},
	}
	if got := rootUsage(disks); got != 40 {
		t.Errorf("rootUsage() = %v, se esperaba la raiz (40)", got)
	}
}

// TestSystemCollectorCollect comprueba contra la maquina real que la lectura
// devuelve valores coherentes: es la garantia de que el cableado con gopsutil
// funciona en el sistema operativo donde se compila.
func TestSystemCollectorCollect(t *testing.T) {
	snap, err := NewSystemCollector().Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if snap.MemoryTotal == 0 {
		t.Error("MemoryTotal = 0")
	}
	if snap.MemoryUsed > snap.MemoryTotal {
		t.Errorf("MemoryUsed (%d) > MemoryTotal (%d)", snap.MemoryUsed, snap.MemoryTotal)
	}
	if snap.CPUUsagePercent < 0 || snap.CPUUsagePercent > 100 {
		t.Errorf("CPUUsagePercent fuera de rango: %v", snap.CPUUsagePercent)
	}
	if len(snap.Disks) == 0 {
		t.Error("no se detecto ningun disco")
	}
}
