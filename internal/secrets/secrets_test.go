package secrets

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// read is the content of a file in a test's temporary folder.
func read(t *testing.T, path string) string {
	t.Helper()
	value, err := os.ReadFile(path) //nolint:gosec // the test's own temporary file
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}

func TestInitCreatesTheDatabasePassword(t *testing.T) {
	dir := t.TempDir()
	created, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !slices.Equal(created, []string{"db_password"}) {
		t.Errorf("created = %v, want [db_password]", created)
	}
	path := filepath.Join(dir, "db_password")
	value := read(t, path)
	if decoded, err := hex.DecodeString(value); err != nil || len(decoded) != 32 {
		t.Errorf("db_password = %q, want 32 bytes in hex", value)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 644", info.Mode().Perm())
	}
}

func TestInitKeepsASecretThatExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db_password")
	if err := os.WriteFile(path, []byte("chosen by hand"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("created = %v, want nothing", created)
	}
	if value := read(t, path); value != "chosen by hand" {
		t.Errorf("db_password = %q, want it kept", value)
	}
}

func TestInitTwiceChangesNothing(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	first := read(t, filepath.Join(dir, "db_password"))
	if _, err := Init(dir); err != nil {
		t.Fatalf("Init again: %v", err)
	}
	if second := read(t, filepath.Join(dir, "db_password")); first != second {
		t.Error("the second Init changed db_password")
	}
}

func TestInitLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the store holds %d files, want only db_password", len(entries))
	}
}

func TestInitFailsWithoutTheStore(t *testing.T) {
	if _, err := Init(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("Init succeeded, want an error")
	}
}
