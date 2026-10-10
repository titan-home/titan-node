// Package health turns what `docker compose config` and `docker compose ps`
// print into the node's services and their statuses (decision #155).
package health

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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
	// Down: missing, stopped while it should run, exited with an error,
	// restarting, dead, created, paused or being removed.
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

// config is what "docker compose config --format json" prints, cut to what
// the controller reads: the services the stack should have.
type config struct {
	Services map[string]struct {
		Restart string `json:"restart"`
	} `json:"services"`
}

// psLine is one line of "docker compose ps --format json".
type psLine struct {
	Service  string `json:"Service"`
	State    string `json:"State"`
	Health   string `json:"Health"`
	ExitCode int    `json:"ExitCode"`
}

// Parse reads the output of `docker compose config --format json`, the
// services the stack should have, and of `docker compose ps --all --format
// json`, their containers, and returns each service with its status, sorted
// by name. A container of a service that is not in the config is left out.
func Parse(configOutput, psOutput []byte) ([]Service, error) {
	var c config
	if err := json.Unmarshal(configOutput, &c); err != nil {
		return nil, fmt.Errorf("read compose config: %w", err)
	}
	containers, err := parsePs(psOutput)
	if err != nil {
		return nil, err
	}

	services := []Service{}
	for name, service := range c.Services {
		container, found := containers[name]
		if !found {
			services = append(services, Service{Name: name, Status: Down})
			continue
		}
		services = append(services, Service{Name: name, Status: status(container, service.Restart)})
	}
	slices.SortFunc(services, func(a, b Service) int { return strings.Compare(a.Name, b.Name) })
	return services, nil
}

// parsePs reads `docker compose ps --all --format json` into each service's
// container.
//
// NOTE: keeps the first container of a service, enough while no service is
// scaled; report every container when one is.
func parsePs(output []byte) (map[string]psLine, error) {
	containers := map[string]psLine{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	// A line carries every label of the image, which may pass the
	// scanner's default limit of 64 KiB.
	scanner.Buffer(nil, 1<<20)
	n := 0
	for scanner.Scan() {
		n++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var p psLine
		if err := json.Unmarshal(line, &p); err != nil {
			return nil, fmt.Errorf("compose ps line %d: %w", n, err)
		}
		if p.Service == "" {
			return nil, fmt.Errorf("compose ps line %d: no Service", n)
		}
		if _, seen := containers[p.Service]; !seen {
			containers[p.Service] = p
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read compose ps: %w", err)
	}
	return containers, nil
}

// status picks a service's status from its container's state, health and
// exit code, and its restart policy.
func status(p psLine, restart string) Status {
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
	// A service Docker restarts whatever its exit code should never stop;
	// any other, such as migrate, is done when it exits with 0.
	case p.State == "exited" && p.ExitCode == 0 && restart != "always" && restart != "unless-stopped":
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
