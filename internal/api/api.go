package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	fleetpulsev1 "github.com/gdev/fleetpulse/gen/fleetpulse/v1"
	"github.com/gdev/fleetpulse/internal/commandbus"
	"github.com/gdev/fleetpulse/internal/heartbeat"
	"github.com/gdev/fleetpulse/internal/hub"
	"github.com/gdev/fleetpulse/internal/store"
	"github.com/google/uuid"
)

// commandTimeout limita cuanto espera una peticion HTTP sincrona (logs) a
// que el agente responda por el canal de comandos.
const commandTimeout = 8 * time.Second

// Server sirve la API REST + SSE que consume el dashboard.
type Server struct {
	store          store.Store
	heartbeats     heartbeat.Backend
	commands       *commandbus.Bus
	metricsHub     *hub.Hub[*fleetpulsev1.MetricPayload]
	dashboardToken string
	corsOrigin     string
	log            *slog.Logger
}

// New construye el servidor HTTP. `dashboardToken` vacio desactiva la
// autenticacion (uso previsto: LAN de confianza).
func New(
	st store.Store,
	heartbeats heartbeat.Backend,
	commands *commandbus.Bus,
	metricsHub *hub.Hub[*fleetpulsev1.MetricPayload],
	dashboardToken, corsOrigin string,
	log *slog.Logger,
) *Server {
	return &Server{
		store:          st,
		heartbeats:     heartbeats,
		commands:       commands,
		metricsHub:     metricsHub,
		dashboardToken: dashboardToken,
		corsOrigin:     corsOrigin,
		log:            log,
	}
}

// Handler construye el router HTTP completo, ya envuelto en CORS y auth.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/nodes", s.handleListNodes)
	mux.HandleFunc("GET /api/nodes/{id}", s.handleGetNode)
	mux.HandleFunc("GET /api/nodes/{id}/metrics", s.handleGetMetrics)
	mux.HandleFunc("GET /api/nodes/{id}/containers", s.handleListContainers)
	mux.HandleFunc("POST /api/nodes/{id}/containers/{cid}/restart", s.handleRestartContainer)
	mux.HandleFunc("GET /api/nodes/{id}/containers/{cid}/logs", s.handleContainerLogs)
	mux.HandleFunc("GET /api/nodes/{id}/stream", s.handleStream)

	return s.withCORS(s.withAuth(mux))
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	summaries := make([]NodeSummary, 0, len(nodes))
	for _, n := range nodes {
		summaries = append(summaries, s.summarize(r.Context(), n))
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	node, err := s.store.GetNode(r.Context(), agentID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	summary := s.summarize(r.Context(), node)
	writeJSON(w, http.StatusOK, NodeDetail{
		NodeSummary:     summary,
		KernelVersion:   node.KernelVersion,
		PlatformVersion: node.PlatformVersion,
	})
}

func (s *Server) summarize(ctx context.Context, node store.Node) NodeSummary {
	summary := NodeSummary{
		AgentID:      node.AgentID,
		Hostname:     node.Hostname,
		OS:           node.OS,
		Platform:     node.Platform,
		Arch:         node.Arch,
		LocalIP:      node.LocalIP,
		PublicIP:     node.PublicIP,
		AgentVersion: node.AgentVersion,
		CPUCores:     node.CPUCores,
		MemoryTotal:  node.MemoryTotal,
		RegisteredAt: node.RegisteredAt,
		LastSeenAt:   node.LastSeenAt,
	}

	if latest, err := s.store.LatestMetric(ctx, node.AgentID); err == nil {
		dto := metricDTOFromPoint(latest)
		summary.Metric = &dto
	} else if !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("no se pudo leer la ultima metrica", "agent_id", node.AgentID, "error", err)
	}

	state, err := s.heartbeats.State(ctx, node.AgentID)
	if err != nil {
		s.log.Warn("no se pudo leer el estado de heartbeat", "agent_id", node.AgentID, "error", err)
	}
	summary.Health = computeHealth(state, summary.Metric)
	return summary
}

// parseSince interpreta ?since=1h,6h,24h,7d... con 1h por defecto y un tope
// de 30 dias para no dejar que una consulta arbitraria escanee el historico
// entero.
func parseSince(r *http.Request) time.Duration {
	raw := r.URL.Query().Get("since")
	if raw == "" {
		return time.Hour
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return time.Hour
	}
	if max := 30 * 24 * time.Hour; d > max {
		return max
	}
	return d
}

func (s *Server) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	since := parseSince(r)
	to := time.Now().UTC()

	points, err := s.store.QueryRange(r.Context(), agentID, to.Add(-since), to)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	dtos := make([]MetricDTO, 0, len(points))
	for _, p := range points {
		dtos = append(dtos, metricDTOFromPoint(p))
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleListContainers(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	containers, err := s.store.ListContainers(r.Context(), agentID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	dtos := make([]ContainerDTO, 0, len(containers))
	for _, c := range containers {
		dtos = append(dtos, containerDTOFromStore(c))
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleRestartContainer(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	containerID := r.PathValue("cid")

	cmd := &fleetpulsev1.Command{
		CommandId: uuid.NewString(),
		Action: &fleetpulsev1.Command_RestartContainer{
			RestartContainer: &fleetpulsev1.RestartContainer{ContainerId: containerID},
		},
	}

	// Dispara y no espera: un reinicio puede tardar varios segundos y el
	// dashboard vera el contenedor actualizarse solo con la proxima rafaga.
	if err := s.commands.Dispatch(r.Context(), agentID, cmd); err != nil {
		if errors.Is(err, commandbus.ErrAgentOffline) {
			writeError(w, http.StatusConflict, "el agente no tiene un canal de comandos abierto")
			return
		}
		s.log.Error("no se pudo despachar el comando de reinicio", "agent_id", agentID, "error", err)
		writeError(w, http.StatusInternalServerError, "no se pudo enviar el comando")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "requested", "command_id": cmd.CommandId})
}

func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	containerID := r.PathValue("cid")

	tail := uint32(200)
	if raw := r.URL.Query().Get("tail"); raw != "" {
		if parsed, err := strconv.ParseUint(raw, 10, 32); err == nil && parsed > 0 {
			tail = uint32(parsed)
		}
	}

	cmd := &fleetpulsev1.Command{
		CommandId: uuid.NewString(),
		Action: &fleetpulsev1.Command_FetchContainerLogs{
			FetchContainerLogs: &fleetpulsev1.FetchContainerLogs{ContainerId: containerID, TailLines: tail},
		},
	}

	ctx, cancel := context.WithTimeout(r.Context(), commandTimeout)
	defer cancel()

	result, err := s.commands.DispatchAndWait(ctx, agentID, cmd)
	switch {
	case errors.Is(err, commandbus.ErrAgentOffline):
		writeError(w, http.StatusConflict, "el agente no tiene un canal de comandos abierto")
		return
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "el agente no respondio a tiempo")
		return
	case err != nil:
		s.log.Error("no se pudo pedir los logs", "agent_id", agentID, "error", err)
		writeError(w, http.StatusInternalServerError, "no se pudieron obtener los logs")
		return
	}

	if !result.GetSuccess() {
		writeError(w, http.StatusBadGateway, result.GetError())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": result.GetLogLines()})
}

func (s *Server) writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "nodo no encontrado")
		return
	}
	s.log.Error("error de almacenamiento", "error", err)
	writeError(w, http.StatusInternalServerError, "error interno")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
