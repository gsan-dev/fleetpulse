package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// keepAliveInterval evita que proxies intermedios (nginx, Cloudflare) den
// por muerta una conexion SSE sin trafico visible.
const keepAliveInterval = 20 * time.Second

// streamEvent es lo que recibe el dashboard por cada rafaga en vivo: la
// metrica de sistema y el snapshot completo de contenedores de esa rafaga,
// para que la vista de detalle no tenga que hacer una peticion aparte.
type streamEvent struct {
	Metric     MetricDTO      `json:"metric"`
	Containers []ContainerDTO `json:"containers"`
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming no soportado")
		return
	}

	events, cancel := s.metricsHub.Subscribe(agentID)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case payload, ok := <-events:
			if !ok {
				return
			}
			containers := make([]ContainerDTO, 0, len(payload.GetContainers()))
			for _, c := range payload.GetContainers() {
				containers = append(containers, containerDTOFromProto(c))
			}
			event := streamEvent{Metric: metricDTOFromProto(payload), Containers: containers}

			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case <-ticker.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}
