// Package server holds the HTTP handlers the controller serves on its Unix
// socket (decisions #78, #154).
package server

import (
	"net/http"

	"github.com/titan-home/titan-node/internal/compose"
)

// Health answers GET /health with every service of the node's stack and its
// status, read from `docker compose ps` through runner.
//
// The owner writes this handler (decision #157); docs/controller.md, "API",
// says what it answers.
func Health(runner compose.Runner) http.Handler {
	_ = runner
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	})
}
