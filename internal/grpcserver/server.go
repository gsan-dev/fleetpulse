// Package grpcserver implementa fleetpulsev1.CollectorServiceServer: el lado
// servidor del contrato definido en proto/fleetpulse/v1/fleetpulse.proto.
// Traduce entre mensajes protobuf y los tipos de dominio de internal/store,
// y conecta cada rafaga con el watchdog de heartbeat y el hub de eventos en
// vivo que alimenta el SSE del dashboard.
package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	fleetpulsev1 "github.com/gdev/fleetpulse/gen/fleetpulse/v1"
	"github.com/gdev/fleetpulse/internal/commandbus"
	"github.com/gdev/fleetpulse/internal/heartbeat"
	"github.com/gdev/fleetpulse/internal/hub"
	"github.com/gdev/fleetpulse/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

// DefaultHeartbeatIntervalSeconds es la cadencia que el servidor sugiere al
// agente en la respuesta de Register.
const DefaultHeartbeatIntervalSeconds = 15

// Server implementa CollectorServiceServer.
type Server struct {
	fleetpulsev1.UnimplementedCollectorServiceServer

	store    store.Store
	watchdog *heartbeat.Watchdog
	metrics  *hub.Hub[*fleetpulsev1.MetricPayload]
	commands *commandbus.Bus
	log      *slog.Logger
}

// New construye el servidor gRPC de ingesta.
func New(st store.Store, watchdog *heartbeat.Watchdog, metricsHub *hub.Hub[*fleetpulsev1.MetricPayload], commands *commandbus.Bus, log *slog.Logger) *Server {
	return &Server{store: st, watchdog: watchdog, metrics: metricsHub, commands: commands, log: log}
}

func (s *Server) Register(ctx context.Context, req *fleetpulsev1.RegisterRequest) (*fleetpulsev1.RegisterResponse, error) {
	if req.GetAgentId() == "" {
		return &fleetpulsev1.RegisterResponse{Accepted: false, Message: "falta agent_id"}, nil
	}

	node := store.Node{
		AgentID:      req.GetAgentId(),
		PublicIP:     publicIPFromPeer(ctx),
		RegisteredAt: time.Now().UTC(),
	}
	if info := req.GetNode(); info != nil {
		node.Hostname = info.GetHostname()
		node.OS = info.GetOs()
		node.Platform = info.GetPlatform()
		node.PlatformVersion = info.GetPlatformVersion()
		node.KernelVersion = info.GetKernelVersion()
		node.Arch = info.GetArch()
		node.LocalIP = info.GetLocalIp()
		node.AgentVersion = info.GetAgentVersion()
		node.CPUCores = info.GetCpuCores()
		node.MemoryTotal = info.GetMemoryTotalBytes()
		node.BootTime = time.Unix(info.GetBootTime(), 0).UTC()
	}

	if err := s.store.UpsertNode(ctx, node); err != nil {
		s.log.Error("no se pudo registrar el nodo", "agent_id", node.AgentID, "error", err)
		return nil, fmt.Errorf("registrar nodo: %w", err)
	}

	s.log.Info("nodo registrado", "agent_id", node.AgentID, "hostname", node.Hostname, "os", node.OS, "arch", node.Arch)

	return &fleetpulsev1.RegisterResponse{
		AgentId:                  node.AgentID,
		HeartbeatIntervalSeconds: DefaultHeartbeatIntervalSeconds,
		Accepted:                 true,
		Message:                  "registrado",
	}, nil
}

func (s *Server) PushMetrics(stream grpc.ClientStreamingServer[fleetpulsev1.MetricPayload, fleetpulsev1.PushMetricsAck]) error {
	ctx := stream.Context()
	var accepted uint64

	for {
		payload, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&fleetpulsev1.PushMetricsAck{
				AcceptedPayloads: accepted,
				ServerTime:       time.Now().UnixMilli(),
			})
		}
		if err != nil {
			return err
		}

		if err := s.ingest(ctx, payload); err != nil {
			s.log.Warn("no se pudo ingerir una rafaga de metricas", "agent_id", payload.GetAgentId(), "error", err)
			continue // una rafaga corrupta no debe tumbar el stream entero
		}
		accepted++
	}
}

func (s *Server) ingest(ctx context.Context, payload *fleetpulsev1.MetricPayload) error {
	agentID := payload.GetAgentId()
	if agentID == "" {
		return errors.New("payload sin agent_id")
	}

	ts := time.UnixMilli(payload.GetTimestamp()).UTC()
	if err := s.store.InsertMetric(ctx, metricPointFromProto(agentID, ts, payload.GetSystem())); err != nil {
		return fmt.Errorf("guardar metrica: %w", err)
	}
	if err := s.store.SetContainers(ctx, agentID, containersFromProto(agentID, payload.GetContainers())); err != nil {
		return fmt.Errorf("guardar contenedores: %w", err)
	}
	if err := s.store.TouchNode(ctx, agentID, ts); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("no se pudo actualizar last_seen_at", "agent_id", agentID, "error", err)
	}

	s.watchdog.Touch(ctx, agentID)
	s.metrics.Publish(agentID, payload)
	return nil
}

func (s *Server) StreamCommands(req *fleetpulsev1.CommandSubscribe, stream grpc.ServerStreamingServer[fleetpulsev1.Command]) error {
	agentID := req.GetAgentId()
	if agentID == "" {
		return fmt.Errorf("StreamCommands: falta agent_id")
	}

	incoming, cancel := s.commands.Register(agentID)
	defer cancel()

	s.log.Info("canal de comandos abierto", "agent_id", agentID)
	defer s.log.Info("canal de comandos cerrado", "agent_id", agentID)

	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case cmd, ok := <-incoming:
			if !ok {
				return nil
			}
			if err := stream.Send(cmd); err != nil {
				return err
			}
		}
	}
}

func (s *Server) ReportCommandResult(_ context.Context, result *fleetpulsev1.CommandResult) (*fleetpulsev1.CommandResultAck, error) {
	delivered := s.commands.Complete(result)
	if !result.GetSuccess() {
		s.log.Warn("comando fallido reportado por el agente",
			"agent_id", result.GetAgentId(), "command_id", result.GetCommandId(), "error", result.GetError())
	} else if !delivered {
		// Resultado de un comando "dispara y olvida" (p.ej. RestartContainer):
		// nadie esperaba la respuesta sincrona, se registra y ya.
		s.log.Debug("resultado de comando sin receptor", "agent_id", result.GetAgentId(), "command_id", result.GetCommandId())
	}
	return &fleetpulsev1.CommandResultAck{}, nil
}

func metricPointFromProto(agentID string, ts time.Time, m *fleetpulsev1.SystemMetrics) store.MetricPoint {
	point := store.MetricPoint{
		AgentID:          agentID,
		Timestamp:        ts,
		CPUUsagePercent:  float64(m.GetCpuUsagePercent()),
		MemoryUsedBytes:  m.GetMemoryUsedBytes(),
		MemoryTotalBytes: m.GetMemoryTotalBytes(),
		DiskUsagePercent: float64(m.GetDiskUsagePercent()),
	}
	if net := m.GetNetwork(); net != nil {
		point.RxBytesPerSecond = net.GetRxBytesPerSecond()
		point.TxBytesPerSecond = net.GetTxBytesPerSecond()
	}
	if load := m.GetLoad(); load != nil {
		point.Load1 = float64(load.GetLoad1())
	}
	return point
}

func containersFromProto(agentID string, containers []*fleetpulsev1.ContainerMetrics) []store.Container {
	out := make([]store.Container, 0, len(containers))
	for _, c := range containers {
		container := store.Container{
			AgentID:      agentID,
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
		}
		if netStats := c.GetNetwork(); netStats != nil {
			container.RxBytes = netStats.GetRxBytes()
			container.TxBytes = netStats.GetTxBytes()
		}
		out = append(out, container)
	}
	return out
}

// publicIPFromPeer deriva la IP publica del agente de la conexion gRPC en
// curso, no de lo que el propio agente reporte: as no puede falsearla.
func publicIPFromPeer(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return p.Addr.String()
	}
	return host
}
