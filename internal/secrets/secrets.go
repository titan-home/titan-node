// Package secrets creates the node's secrets: files in the secret store, a
// folder of mode 0700 that only the controller's user enters (decisions #77,
// #163).
package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// generated are the secrets the controller makes up itself: random values no
// one has to type.
var generated = []string{"db_password"}

// Init creates every generated secret missing from dir and keeps those that
// exist, so running it again changes nothing. It returns the names of the
// secrets it created.
func Init(dir string) ([]string, error) {
	created := []string{}
	for _, name := range generated {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("checking secret %s: %w", name, err)
		}
		value, err := random()
		if err != nil {
			return nil, err
		}
		if err := create(path, value); err != nil {
			return nil, fmt.Errorf("creating secret %s: %w", name, err)
		}
		created = append(created, name)
	}
	return created, nil
}

// random is 32 random bytes in hex: 64 characters with nothing a shell or a
// connection string would read specially.
func random() ([]byte, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("reading random bytes: %w", err)
	}
	return []byte(hex.EncodeToString(value)), nil
}

// create writes value to path, which must not exist. It writes a temporary
// file first and links it into place, so a crash never leaves a half-written
// secret that a later Init would keep. The mode is 0644: compose mounts the
// file with it into containers that run as other users (decision #77).
func create(path string, value []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name()) //nolint:errcheck // gone already once linked
	if _, err := temporary.Write(value); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil { //nolint:gosec // see the comment above
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	// Link, unlike rename, fails when path exists, so a secret is never
	// replaced.
	return os.Link(temporary.Name(), path)
}
