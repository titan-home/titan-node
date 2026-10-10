package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/titan-home/titan-node/internal/health"
)

// fakeRunner stands in for `docker compose`: it records every call and
// answers each command, its first argument, with outputs and errs.
type fakeRunner struct {
	outputs map[string]string
	errs    map[string]error
	calls   [][]string
	ctx     context.Context
}

func (f *fakeRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	f.ctx = ctx
	return []byte(f.outputs[args[0]]), f.errs[args[0]]
}

// The node's config, cut to two services, and their containers: real
// `docker compose ps --all --format json` lines, trimmed.
const (
	sampleConfig = `{"name":"titan","services":{"api":{"restart":"unless-stopped"},"migrate":{}}}`
	samplePs     = `{"ExitCode":0,"Health":"healthy","Name":"titan-api-1","Service":"api","State":"running","Status":"Up 9 seconds (healthy)"}
{"ExitCode":0,"Health":"","Name":"titan-migrate-1","Service":"migrate","State":"exited","Status":"Exited (0) 8 seconds ago"}
`
)

// sample answers config and ps like a node whose api is healthy and whose
// migrate is done.
func sample() *fakeRunner {
	return &fakeRunner{outputs: map[string]string{"config": sampleConfig, "ps": samplePs}}
}

// get sends GET /health to the Health handler and returns the response.
func get(ctx context.Context, t *testing.T, runner *fakeRunner) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	Health(runner).ServeHTTP(response, request)
	return response
}

func TestHealthAnswersTheServices(t *testing.T) {
	response := get(context.Background(), t, sample())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", response.Code, response.Body)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var body struct {
		Services []health.Service `json:"services"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", response.Body, err)
	}
	want := []health.Service{{Name: "api", Status: health.Healthy}, {Name: "migrate", Status: health.Done}}
	if !slices.Equal(body.Services, want) {
		t.Errorf("services = %v, want %v", body.Services, want)
	}
}

// wantBadGateway checks for a 502 with a short, fixed JSON error that
// carries none of details, which belong in the log.
func wantBadGateway(t *testing.T, response *httptest.ResponseRecorder, details ...string) {
	t.Helper()
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body %q", response.Code, response.Body)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", response.Body, err)
	}
	if body.Error == "" {
		t.Errorf("body %q has no error message", response.Body)
	}
	for _, detail := range details {
		if strings.Contains(body.Error, detail) {
			t.Errorf("error message %q carries the detail %q; details go to the log", body.Error, detail)
		}
	}
}

func TestHealthAnswers502WhenComposeFails(t *testing.T) {
	for _, command := range []string{"config", "ps"} {
		t.Run(command, func(t *testing.T) {
			detail := "permission denied while trying to connect to the docker API"
			runner := sample()
			runner.errs = map[string]error{command: errors.New(detail)}
			wantBadGateway(t, get(context.Background(), t, runner), detail)
		})
	}
}

func TestHealthAnswers502WhenTheOutputDoesNotParse(t *testing.T) {
	runner := sample()
	runner.outputs["ps"] = "not json\n"
	response := get(context.Background(), t, runner)
	details := []string{"not json", "line 1"}
	// Whatever Parse says about the output is detail too.
	if _, err := health.Parse([]byte(sampleConfig), []byte("not json\n")); err != nil {
		details = append(details, err.Error())
	}
	wantBadGateway(t, response, details...)
}

func TestHealthAnswersAnEmptyListWhenTheStackHasNoServices(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{"config": `{"services":{}}`}}
	response := get(context.Background(), t, runner)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", response.Code, response.Body)
	}
	if got := strings.TrimSpace(response.Body.String()); got != `{"services":[]}` {
		t.Errorf("body = %q, want {\"services\":[]}", got)
	}
}

func TestHealthRunsComposeConfigAndPs(t *testing.T) {
	runner := sample()
	get(context.Background(), t, runner)
	want := [][]string{{"config", "--format", "json"}, {"ps", "--all", "--format", "json"}}
	if !slices.EqualFunc(runner.calls, want, slices.Equal) {
		t.Errorf("runner calls = %q, want %q", runner.calls, want)
	}
}

func TestHealthPassesTheRequestsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := sample()
	get(ctx, t, runner)
	if len(runner.calls) == 0 {
		t.Fatal("the runner was not called")
	}
	if !errors.Is(runner.ctx.Err(), context.Canceled) {
		t.Errorf("the runner's context is not the request's: Err() = %v", runner.ctx.Err())
	}
}
