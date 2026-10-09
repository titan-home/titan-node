// Package compose runs `docker compose` against the node's compose file
// (decisions #142, #156).
package compose

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner runs one `docker compose` command and returns what it printed on
// standard output. Everything that drives Docker goes through it, so tests
// pass a fake instead (decision #156).
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// Exec is the Runner that runs the real `docker compose` through the mounted
// Docker socket.
type Exec struct {
	// Dir is the node folder, which holds compose.yaml and .env.
	Dir string
	// Project is the compose project name, `titan` on a real node.
	Project string
}

// Run runs `docker compose` with the node's folder and project name, then
// args, and returns its standard output. When the command fails, the error
// holds its standard error.
func (e Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	// The arguments come from the controller's own code, never from a request.
	cmd := exec.CommandContext(ctx, "docker", e.arguments(args)...) //nolint:gosec // see above
	// When ctx ends, the docker process is killed; this bounds the wait
	// for anything it started that still holds its output open.
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker compose %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return output, nil
}

// arguments builds the docker command line after `docker`.
func (e Exec) arguments(args []string) []string {
	return append([]string{
		"compose", "--project-directory", e.Dir, "--project-name", e.Project,
	}, args...)
}
