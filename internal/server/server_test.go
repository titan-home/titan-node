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

// fakeRunner stands in for `docker compose`: it records what it was called
// with and answers with output and err.
type fakeRunner struct {
	output []byte
	err    error
	called bool
	ctx    context.Context
	args   []string
}

func (f *fakeRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	f.called = true
	f.ctx = ctx
	f.args = args
	return f.output, f.err
}

// Real `docker compose ps --all --format json` lines, trimmed.
const sample = `{"ExitCode":0,"Health":"healthy","Name":"titan-api-1","Service":"api","State":"running","Status":"Up 9 seconds (healthy)"}
{"ExitCode":0,"Health":"","Name":"titan-migrate-1","Service":"migrate","State":"exited","Status":"Exited (0) 8 seconds ago"}
`

// get sends GET /health to the Health handler and returns the response.
func get(ctx context.Context, t *testing.T, runner *fakeRunner) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	Health(runner).ServeHTTP(response, request)
	return response
}

func TestHealthAnswersTheServices(t *testing.T) {
	response := get(context.Background(), t, &fakeRunner{output: []byte(sample)})
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
	detail := "permission denied while trying to connect to the docker API"
	response := get(context.Background(), t, &fakeRunner{err: errors.New(detail)})
	wantBadGateway(t, response, detail)
}

func TestHealthAnswers502WhenTheOutputDoesNotParse(t *testing.T) {
	output := []byte("not json\n")
	response := get(context.Background(), t, &fakeRunner{output: output})
	details := []string{"not json", "line 1"}
	// Whatever Parse says about the output is detail too.
	if _, err := health.Parse(output); err != nil {
		details = append(details, err.Error())
	}
	wantBadGateway(t, response, details...)
}

func TestHealthAnswersAnEmptyListWhenNothingRuns(t *testing.T) {
	response := get(context.Background(), t, &fakeRunner{output: nil})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %q", response.Code, response.Body)
	}
	if got := strings.TrimSpace(response.Body.String()); got != `{"services":[]}` {
		t.Errorf("body = %q, want {\"services\":[]}", got)
	}
}

func TestHealthRunsComposePs(t *testing.T) {
	runner := &fakeRunner{output: []byte(sample)}
	get(context.Background(), t, runner)
	want := []string{"ps", "--all", "--format", "json"}
	if !slices.Equal(runner.args, want) {
		t.Errorf("runner args = %q, want %q", runner.args, want)
	}
}

func TestHealthPassesTheRequestsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &fakeRunner{output: []byte(sample)}
	get(ctx, t, runner)
	if !runner.called {
		t.Fatal("the runner was not called")
	}
	if !errors.Is(runner.ctx.Err(), context.Canceled) {
		t.Errorf("the runner's context is not the request's: Err() = %v", runner.ctx.Err())
	}
}
