package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.corsOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withAuth exige el token del dashboard cuando esta configurado. Se acepta
// tanto por cabecera ("Authorization: Bearer <token>") como por query param
// (?token=<token>) porque EventSource, que usa el visor de logs y la Fleet
// Grid en vivo, no puede fijar cabeceras personalizadas.
func (s *Server) withAuth(next http.Handler) http.Handler {
	if s.dashboardToken == "" {
		return next
	}

	expected := []byte(s.dashboardToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/api/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		token := r.URL.Query().Get("token")
		if token == "" {
			token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}

		if subtle.ConstantTimeCompare([]byte(token), expected) != 1 {
			writeError(w, http.StatusUnauthorized, "token invalido")
			return
		}
		next.ServeHTTP(w, r)
	})
}
