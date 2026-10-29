//go:build !windows

package fsutil

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadFileCapped_FIFO(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pipe")
	require.NoError(t, syscall.Mkfifo(p, 0o644))

	done := make(chan error, 1)
	go func() {
		_, err := ReadFileCapped(p, 1024)
		done <- err
	}()
	select {
	case err := <-done:
		assert.True(t, errors.Is(err, ErrNotRegular))
	case <-time.After(5 * time.Second):
		t.Fatal("ReadFileCapped blocked on a FIFO")
	}
}

func TestCheckFileCapped_FIFO(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pipe")
	require.NoError(t, syscall.Mkfifo(p, 0o644))

	done := make(chan error, 1)
	go func() { done <- CheckFileCapped(p, 1024) }()
	select {
	case err := <-done:
		assert.True(t, errors.Is(err, ErrNotRegular))
	case <-time.After(5 * time.Second):
		t.Fatal("CheckFileCapped blocked on a FIFO")
	}
}
