package compose

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestArgumentsPutNodeFolderAndProjectFirst(t *testing.T) {
	e := Exec{Dir: "/opt/titan", Project: "titan"}
	got := e.arguments([]string{"ps", "--all", "--format", "json"})
	want := []string{
		"compose", "--project-directory", "/opt/titan", "--project-name", "titan",
		"ps", "--all", "--format", "json",
	}
	if !slices.Equal(got, want) {
		t.Errorf("arguments = %q, want %q", got, want)
	}
}

// fakeDocker puts a `docker` script that runs body first on PATH.
func fakeDocker(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestRunReturnsStandardOutput(t *testing.T) {
	fakeDocker(t, `echo "$@"; echo noise >&2`)
	got, err := Exec{Dir: "/opt/titan", Project: "titan"}.Run(context.Background(), "ps")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := "compose --project-directory /opt/titan --project-name titan ps\n"; string(got) != want {
		t.Errorf("Run = %q, want %q", got, want)
	}
}

func TestRunWrapsStandardErrorOnFailure(t *testing.T) {
	fakeDocker(t, `echo "no such service" >&2; exit 1`)
	_, err := Exec{Dir: "/opt/titan", Project: "titan"}.Run(context.Background(), "ps")
	if err == nil {
		t.Fatal("Run succeeded, want an error")
	}
	for _, part := range []string{"docker compose ps", "exit status 1", "no such service"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q lacks %q", err, part)
		}
	}
}
