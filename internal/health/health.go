// Package health turns what `docker compose ps` prints into the node's
// services and their statuses (decision #155).
package health

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
)

// Status is a service's health as the controller reports it.
type Status string

// The statuses a service can have.
const (
	// Healthy: running, and its healthcheck passes.
	Healthy Status = "healthy"
	// Running: running, and it has no healthcheck.
	Running Status = "running"
	// Starting: running, and its healthcheck has not passed yet.
	Starting Status = "starting"
	// Unhealthy: running, and its healthcheck fails.
	Unhealthy Status = "unhealthy"
	// Done: a one-off service, such as migrate, that exited with code 0.
	Done Status = "done"
	// Down: exited with an error, restarting, dead, created, paused or
	// being removed.
	Down Status = "down"
	// Unknown: a state or health Docker reported that the controller does
	// not know.
	Unknown Status = "unknown"
)

// Service is one service of the node's stack and its status.
type Service struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
}

// psLine is one line of "docker compose ps --format json".
type psLine struct {
	Service  string `json:"Service"`
	State    string `json:"State"`
	Health   string `json:"Health"`
	ExitCode int    `json:"ExitCode"`
}

// Parse reads the output of `docker compose ps --all --format json` and
// returns each service with its status, in the order given.
func Parse(output []byte) ([]Service, error) {
	services := []Service{}

	scanner := bufio.NewScanner(bytes.NewReader(output))
	n := 0
	for scanner.Scan() {
		n++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var p psLine
		if err := json.Unmarshal(line, &p); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if p.Service == "" {
			return nil, fmt.Errorf("line %d: no Service", n)
		}
		services = append(services, Service{Name: p.Service, Status: status(p)})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read compose ps: %w", err)
	}
	return services, nil
}

// status picks a service's status from its state, health and exit code.
func status(p psLine) Status {
	switch {
	case p.State == "running":
		switch p.Health {
		case "healthy":
			return Healthy
		case "unhealthy":
			return Unhealthy
		case "starting":
			return Starting
		case "":
			return Running
		default:
			return Unknown
		}
	case p.State == "exited" && p.ExitCode == 0:
		return Done
	case p.State == "exited",
		p.State == "restarting",
		p.State == "dead",
		p.State == "created",
		p.State == "paused",
		p.State == "removing":
		return Down
	}

	return Unknown
}
