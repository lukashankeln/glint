//go:build !windows

package discovery

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscover_FIFOSkipped(t *testing.T) {
	root := writeArgoRepo(t)
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "pipe.yaml"), 0o644))

	type result struct {
		apps  []DiscoveredApp
		perrs []ParseError
		err   error
	}
	done := make(chan result, 1)
	go func() {
		apps, perrs, err := Discover(context.Background(), []string{root}, defaultCfg())
		done <- result{apps, perrs, err}
	}()
	select {
	case res := <-done:
		require.NoError(t, res.err)
		assert.Empty(t, res.perrs)
		assert.NotEmpty(t, filterByFramework(res.apps, FrameworkArgoCD))
	case <-time.After(10 * time.Second):
		t.Fatal("Discover blocked on a FIFO")
	}
}
