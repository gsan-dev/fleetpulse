package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/gdev/fleetpulse/internal/parallel"
)

// statsWorkers limita cuantos contenedores se consultan a la vez. Cada lectura
// de stats mantiene abierta una conexion contra el daemon durante ~1s, asi que
// un nodo con 200 contenedores no debe abrirlas todas de golpe.
const statsWorkers = 8

// DockerInspector habla con el engine local a traves de /var/run/docker.sock
// (o de DOCKER_HOST) para listar contenedores y medir su consumo.
type DockerInspector struct {
	cli *client.Client
}

// NewDockerInspector conecta con el daemon y negocia la version de la API, de
// forma que el mismo binario sirva para engines antiguos y recientes. Devuelve
// error si el socket no existe o el daemon no responde al ping.
func NewDockerInspector(ctx context.Context) (*DockerInspector, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("cliente docker: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := cli.Ping(pingCtx); err != nil {
		cli.Close()
		return nil, fmt.Errorf("ping al daemon docker: %w", err)
	}

	return &DockerInspector{cli: cli}, nil
}

// Close libera la conexion con el daemon.
func (d *DockerInspector) Close() error {
	if d == nil || d.cli == nil {
		return nil
	}
	return d.cli.Close()
}

// Containers lista todos los contenedores (incluidos los parados) y completa
// los que estan corriendo con sus metricas de CPU, memoria y red.
func (d *DockerInspector) Containers(ctx context.Context) ([]ContainerSnapshot, error) {
	list, err := d.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("listar contenedores: %w", err)
	}

	snapshots := make([]ContainerSnapshot, len(list))
	for i, c := range list {
		snapshots[i] = ContainerSnapshot{
			ID:     c.ID,
			Name:   containerName(c.Names),
			Image:  c.Image,
			Status: c.Status,
			State:  c.State,
			// El listado solo expone la fecha de creacion; para los que estan
			// corriendo la sustituye el arranque real leido en fillStats.
			StartedAt: time.Unix(c.Created, 0),
			Labels:    c.Labels,
		}
	}

	d.fillStats(ctx, snapshots)

	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Name < snapshots[j].Name })
	return snapshots, nil
}

// fillStats consulta en paralelo las metricas de los contenedores en marcha.
// Un contenedor que falle se queda con sus metricas a cero en lugar de tumbar
// la muestra entera: el panel prefiere datos parciales a un hueco.
func (d *DockerInspector) fillStats(ctx context.Context, snapshots []ContainerSnapshot) {
	running := make([]int, 0, len(snapshots))
	for i := range snapshots {
		if strings.EqualFold(snapshots[i].State, "running") {
			running = append(running, i)
		}
	}

	parallel.ForEach(ctx, statsWorkers, running, func(i int) {
		if info, err := d.cli.ContainerInspect(ctx, snapshots[i].ID); err == nil {
			snapshots[i].RestartCount = info.RestartCount
			if started, err := time.Parse(time.RFC3339Nano, info.State.StartedAt); err == nil {
				snapshots[i].StartedAt = started
			}
		}

		stats, err := d.containerStats(ctx, snapshots[i].ID)
		if err != nil {
			return
		}
		snapshots[i].CPUPercent = stats.cpuPercent
		snapshots[i].MemoryBytes = stats.memoryBytes
		snapshots[i].MemoryLimit = stats.memoryLimit
		snapshots[i].RxBytes = stats.rxBytes
		snapshots[i].TxBytes = stats.txBytes
	})
}

type containerStats struct {
	cpuPercent  float64
	memoryBytes uint64
	memoryLimit uint64
	rxBytes     uint64
	txBytes     uint64
}

// containerStats lee dos muestras consecutivas del stream de stats. Hace falta
// la segunda porque el porcentaje de CPU es un delta contra la muestra previa,
// y en la primera trama el bloque precpu llega a cero.
func (d *DockerInspector) containerStats(ctx context.Context, id string) (containerStats, error) {
	var out containerStats

	resp, err := d.cli.ContainerStats(ctx, id, true)
	if err != nil {
		return out, fmt.Errorf("stats de %s: %w", shortID(id), err)
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	var frame container.StatsResponse
	for i := 0; i < 2; i++ {
		if err := decoder.Decode(&frame); err != nil {
			if i == 0 {
				return out, fmt.Errorf("decodificar stats de %s: %w", shortID(id), err)
			}
			break // el contenedor murio a mitad: nos quedamos con la trama previa
		}
	}

	out.cpuPercent = cpuPercent(frame)
	out.memoryBytes = memoryUsage(frame)
	out.memoryLimit = frame.MemoryStats.Limit
	out.rxBytes, out.txBytes = networkTotals(frame)
	return out, nil
}

// Logs devuelve las ultimas `tail` lineas del contenedor, ya demultiplexadas
// del formato de stream de Docker (stdout y stderr van entrelazados).
func (d *DockerInspector) Logs(ctx context.Context, id string, tail int) ([]string, error) {
	if tail <= 0 {
		tail = 100
	}

	reader, err := d.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return nil, fmt.Errorf("logs de %s: %w", shortID(id), err)
	}
	defer reader.Close()

	var plain strings.Builder
	if _, err := stdcopy.StdCopy(&plain, &plain, reader); err != nil && err != io.EOF {
		return nil, fmt.Errorf("demultiplexar logs de %s: %w", shortID(id), err)
	}

	lines := strings.Split(strings.TrimRight(plain.String(), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	return lines, nil
}

// defaultRestartTimeout es cuanto espera Docker a que el contenedor pare por
// las buenas (SIGTERM) antes de matarlo, igual que `docker restart` sin -t.
const defaultRestartTimeout = 10 * time.Second

// Restart reinicia un contenedor. Lo dispara el comando RestartContainer que
// llega por el canal de comandos del servidor (ver internal/commands).
func (d *DockerInspector) Restart(ctx context.Context, id string) error {
	timeout := int(defaultRestartTimeout.Seconds())
	if err := d.cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("reiniciar %s: %w", shortID(id), err)
	}
	return nil
}

// cpuPercent replica el calculo de `docker stats`: el delta de CPU del
// contenedor sobre el delta de CPU de todo el sistema, escalado por el numero
// de nucleos visibles.
func cpuPercent(s container.StatsResponse) float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	if cpuDelta <= 0 || systemDelta <= 0 {
		return 0
	}

	cores := float64(s.CPUStats.OnlineCPUs)
	if cores == 0 {
		cores = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	if cores == 0 {
		cores = 1
	}

	return (cpuDelta / systemDelta) * cores * 100.0
}

// memoryUsage descuenta la cache de pagina del uso reportado, igual que hace
// `docker stats`. La clave cambia entre cgroup v1 ("cache") y v2
// ("inactive_file"), y sin ese descuento todo contenedor que lea ficheros
// parece estar al limite de memoria.
func memoryUsage(s container.StatsResponse) uint64 {
	usage := s.MemoryStats.Usage
	for _, key := range []string{"inactive_file", "total_inactive_file", "cache"} {
		if cache, ok := s.MemoryStats.Stats[key]; ok {
			if cache > usage {
				return 0
			}
			return usage - cache
		}
	}
	return usage
}

func networkTotals(s container.StatsResponse) (rx, tx uint64) {
	for _, iface := range s.Networks {
		rx += iface.RxBytes
		tx += iface.TxBytes
	}
	return rx, tx
}

// containerName toma el primer alias del contenedor y le quita la barra
// inicial que anade el engine ("/nginx" -> "nginx").
func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
