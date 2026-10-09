// Command titan-controller is the node's controller: it serves its API on a
// Unix socket only, never on the network (decision #78), and drives the
// node's stack through `docker compose` (decision #142).
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/titan-home/titan-node/internal/compose"
	"github.com/titan-home/titan-node/internal/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := run(ctx, stop); err != nil {
		slog.Error("the controller stopped", "error", err.Error())
		stop()
		os.Exit(1)
	}
}

// settings are the controller's settings, read from the environment.
type settings struct {
	nodeDir string // TITAN_NODE_DIR: the node folder, mounted at the same path
	project string // TITAN_COMPOSE_PROJECT: the compose project name
	socket  string // TITAN_CONTROLLER_SOCKET: the path of the socket to serve on
}

// readSettings reads the settings and fails if one is missing.
func readSettings() (settings, error) {
	s := settings{
		nodeDir: os.Getenv("TITAN_NODE_DIR"),
		project: os.Getenv("TITAN_COMPOSE_PROJECT"),
		socket:  os.Getenv("TITAN_CONTROLLER_SOCKET"),
	}
	if s.nodeDir == "" || s.project == "" || s.socket == "" {
		return settings{}, errors.New("TITAN_NODE_DIR, TITAN_COMPOSE_PROJECT and TITAN_CONTROLLER_SOCKET must all be set")
	}
	return s, nil
}

// run serves the controller's API on its socket until ctx is cancelled, then
// shuts down, letting requests in progress finish. stop ends the signal
// handling, so a second signal ends the process at once.
func run(ctx context.Context, stop context.CancelFunc) error {
	s, err := readSettings()
	if err != nil {
		return err
	}
	listener, err := listen(ctx, s.socket)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("GET /health", server.Health(compose.Exec{Dir: s.nodeDir, Project: s.project}))
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	served := make(chan error, 1)
	go func() { served <- httpServer.Serve(listener) }()
	slog.Info("serving", "socket", s.socket, "node_dir", s.nodeDir, "project", s.project)

	select {
	case err := <-served:
		return fmt.Errorf("serving on %s: %w", s.socket, err)
	case <-ctx.Done():
	}
	stop()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Shutdown closes the listener but leaves the socket file, which the
	// next start removes (see listen).
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}
	return nil
}

// listen creates the Unix socket at path, readable and writable by the
// controller's user and group only. The group is the controller's primary
// group, which is the api's group, so the api and nothing else can call it.
func listen(ctx context.Context, path string) (net.Listener, error) {
	// A socket file left over from the previous controller would make
	// listening fail.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("removing the old socket %s: %w", path, err)
	}
	var config net.ListenConfig
	listener, err := config.Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", path, err)
	}
	// By default closing the listener deletes the socket file. During a
	// restart, an old controller shutting down could then delete the file
	// of the new one, which already listens at the same path. Listening on
	// "unix" always gives a *net.UnixListener.
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	// Created under the umask, the socket is not writable by the group, so
	// no one but the controller can connect until this widens it.
	if err := os.Chmod(path, 0o660); err != nil { //nolint:gosec // the api, in the group, must connect
		_ = listener.Close()
		return nil, fmt.Errorf("setting the mode of %s: %w", path, err)
	}
	return listener, nil
}
