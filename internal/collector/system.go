package collector

import (
	"context"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// pseudoFilesystems son sistemas de ficheros virtuales o de solo lectura que
// no aportan nada al panel: ocupan siempre el 100% o reportan tamano cero.
var pseudoFilesystems = map[string]bool{
	"autofs":          true,
	"binfmt_misc":     true,
	"bpf":             true,
	"cgroup":          true,
	"cgroup2":         true,
	"configfs":        true,
	"debugfs":         true,
	"devpts":          true,
	"devtmpfs":        true,
	"fuse.gvfsd-fuse": true,
	"fusectl":         true,
	"hugetlbfs":       true,
	"mqueue":          true,
	"overlay":         true,
	"proc":            true,
	"pstore":          true,
	"ramfs":           true,
	"securityfs":      true,
	"squashfs":        true,
	"sysfs":           true,
	"tmpfs":           true,
	"tracefs":         true,
}

// ioCounters guarda la lectura previa de contadores acumulados para poder
// derivar tasas por segundo.
type ioCounters struct {
	at        time.Time
	rxBytes   uint64
	txBytes   uint64
	diskRead  map[string]uint64
	diskWrite map[string]uint64
}

// SystemCollector lee metricas del sistema operativo. No es seguro usarlo
// desde varias goroutines a la vez: mantiene el estado de la lectura anterior
// para calcular tasas.
type SystemCollector struct {
	prev *ioCounters
}

// NewSystemCollector crea el colector de sistema.
func NewSystemCollector() *SystemCollector {
	return &SystemCollector{}
}

// Collect toma una muestra del sistema operativo. La primera llamada devuelve
// tasas de red y disco a cero porque aun no hay lectura previa con la que
// comparar; el uso de CPU tambien es mas impreciso en esa primera muestra.
func (c *SystemCollector) Collect(ctx context.Context) (SystemSnapshot, error) {
	var snap SystemSnapshot

	// Intervalo 0: gopsutil compara contra la lectura anterior del proceso en
	// lugar de bloquear. Encaja con un bucle periodico de metricas.
	if percents, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(percents) > 0 {
		snap.CPUUsagePercent = percents[0]
	}

	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return snap, err
	}
	snap.MemoryUsed = vm.Used
	snap.MemoryTotal = vm.Total
	snap.MemoryAvailable = vm.Available

	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		snap.SwapUsed = sw.Used
		snap.SwapTotal = sw.Total
	}

	if avg, err := load.AvgWithContext(ctx); err == nil {
		snap.Load = LoadSnapshot{Load1: avg.Load1, Load5: avg.Load5, Load15: avg.Load15}
	}

	if uptime, err := host.UptimeWithContext(ctx); err == nil {
		snap.Uptime = time.Duration(uptime) * time.Second
	}

	now := time.Now()
	current := &ioCounters{at: now, diskRead: map[string]uint64{}, diskWrite: map[string]uint64{}}

	if counters, err := net.IOCountersWithContext(ctx, false); err == nil && len(counters) > 0 {
		current.rxBytes = counters[0].BytesRecv
		current.txBytes = counters[0].BytesSent
		snap.Network.RxBytesTotal = counters[0].BytesRecv
		snap.Network.TxBytesTotal = counters[0].BytesSent
	}

	if io, err := disk.IOCountersWithContext(ctx); err == nil {
		for name, stat := range io {
			current.diskRead[name] = stat.ReadBytes
			current.diskWrite[name] = stat.WriteBytes
		}
	}

	elapsed := 0.0
	if c.prev != nil {
		elapsed = now.Sub(c.prev.at).Seconds()
	}
	if elapsed > 0 {
		snap.Network.RxBytesPerSecond = perSecond(current.rxBytes, c.prev.rxBytes, elapsed)
		snap.Network.TxBytesPerSecond = perSecond(current.txBytes, c.prev.txBytes, elapsed)
	}

	snap.Disks = c.collectDisks(ctx, current, elapsed)
	snap.RootDiskPercent = rootUsage(snap.Disks)

	c.prev = current
	return snap, nil
}

func (c *SystemCollector) collectDisks(ctx context.Context, current *ioCounters, elapsed float64) []DiskSnapshot {
	partitions, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool, len(partitions))
	disks := make([]DiskSnapshot, 0, len(partitions))
	for _, p := range partitions {
		if pseudoFilesystems[p.Fstype] {
			continue
		}
		// Un mismo dispositivo puede aparecer en varios montajes (bind mounts,
		// subvolumenes btrfs); contarlo una vez basta para el panel.
		if seen[p.Device] {
			continue
		}

		usage, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || usage.Total == 0 {
			continue
		}
		seen[p.Device] = true

		d := DiskSnapshot{
			Device:       p.Device,
			Mountpoint:   p.Mountpoint,
			FSType:       p.Fstype,
			TotalBytes:   usage.Total,
			UsedBytes:    usage.Used,
			UsagePercent: usage.UsedPercent,
		}

		if elapsed > 0 && c.prev != nil {
			key := deviceKey(p.Device)
			if read, ok := current.diskRead[key]; ok {
				d.ReadBPS = perSecond(read, c.prev.diskRead[key], elapsed)
			}
			if write, ok := current.diskWrite[key]; ok {
				d.WriteBPS = perSecond(write, c.prev.diskWrite[key], elapsed)
			}
		}

		disks = append(disks, d)
	}
	return disks
}

// deviceKey normaliza "/dev/sda1" al nombre que usa disk.IOCounters ("sda1").
func deviceKey(device string) string {
	return strings.TrimPrefix(device, "/dev/")
}

// perSecond calcula una tasa protegiendose de contadores que se reinician
// (reboot, reset de interfaz), donde el acumulado actual queda por debajo del
// anterior y la resta sin signo desbordaria.
func perSecond(current, previous uint64, seconds float64) uint64 {
	if seconds <= 0 || current < previous {
		return 0
	}
	return uint64(float64(current-previous) / seconds)
}

// rootUsage extrae el uso del volumen raiz, que es el que decide el color del
// nodo en la Fleet Grid. Si no se identifica, cae al montaje mas lleno.
func rootUsage(disks []DiskSnapshot) float64 {
	worst := 0.0
	for _, d := range disks {
		if isRootMount(d.Mountpoint) {
			return d.UsagePercent
		}
		if d.UsagePercent > worst {
			worst = d.UsagePercent
		}
	}
	return worst
}

// isRootMount reconoce el volumen de sistema. En Windows el punto de montaje
// puede llegar como "C:" o como "C:\" segun la version de gopsutil, asi que se
// comparan solo las dos primeras letras.
func isRootMount(mountpoint string) bool {
	if runtime.GOOS != "windows" {
		return mountpoint == "/"
	}
	return len(mountpoint) >= 2 && strings.EqualFold(mountpoint[:2], "C:")
}
