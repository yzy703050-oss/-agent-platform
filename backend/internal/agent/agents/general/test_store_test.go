package general

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// General's execution tests use a fresh durable store independently of Eino internals.
type diskStore struct{ dir string }

func (s *diskStore) Get(_ context.Context, _ string) ([]byte, bool, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, "checkpoint"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}
func (s *diskStore) Set(_ context.Context, _ string, b []byte) error {
	return os.WriteFile(filepath.Join(s.dir, "checkpoint"), b, 0600)
}
func (s *diskStore) Delete(_ context.Context, _ string) error {
	err := os.Remove(filepath.Join(s.dir, "checkpoint"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func await(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("execution did not reach expected boundary")
	}
}
