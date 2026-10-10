// Package server holds the HTTP handlers the controller serves on its Unix
// socket (decisions #78, #154).
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/titan-home/titan-node/internal/compose"
	"github.com/titan-home/titan-node/internal/health"
)

// Health answers GET /health with every service of the node's stack and its
// status, read from `docker compose ps` through runner.
func Health(runner compose.Runner) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		output, err := runner.Run(r.Context(), "ps", "--all", "--format", "json")
		if err != nil {
			badGateway(w, "docker compose ps failed", err)
			return
		}
		services, err := health.Parse(output)
		if err != nil {
			badGateway(w, "docker compose ps printed what the controller cannot read", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string][]health.Service{"services": services})
	})
}

// badGateway logs err and answers 502 with a fixed message: the details may
// carry paths and Docker's own words, which belong in the log only.
func badGateway(w http.ResponseWriter, message string, err error) {
	slog.Error(message, "error", err.Error())
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "cannot read the services' health"})
}

// writeJSON answers status with body as JSON.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("writing the answer failed", "error", err.Error())
	}
}
