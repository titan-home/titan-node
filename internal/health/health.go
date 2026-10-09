// Package health turns what `docker compose ps` prints into the node's
// services and their statuses (decision #155).
package health

import "errors"

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
	// Down: anything else: exited with an error, restarting, dead, created
	// or paused.
	Down Status = "down"
)

// Service is one service of the node's stack and its status.
type Service struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
}

// Parse reads the output of `docker compose ps --all --format json` and
// returns each service with its status, in the order given.
//
// The owner writes this function (decision #157); docs/controller.md,
// "Statuses", says which status each container gets.
func Parse(output []byte) ([]Service, error) {
	_ = output
	return nil, errors.New("not implemented")
}
