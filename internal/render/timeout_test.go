package render

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunWithContext_Timeout(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	got, err := runWithContext(ctx, func() (string, error) {
		<-block
		return "late", nil
	})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, got)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestRunWithContext_PassThrough(t *testing.T) {
	got, err := runWithContext(context.Background(), func() (int, error) { return 7, nil })
	require.NoError(t, err)
	assert.Equal(t, 7, got)

	boom := errors.New("boom")
	_, err = runWithContext(context.Background(), func() (int, error) { return 0, boom })
	assert.ErrorIs(t, err, boom)
}

func TestWithRenderTimeout(t *testing.T) {
	ctx, cancel, d, err := withRenderTimeout(context.Background(), "")
	require.NoError(t, err)
	defer cancel()
	_, hasDeadline := ctx.Deadline()
	assert.False(t, hasDeadline)
	assert.Zero(t, d)

	ctx, cancel2, d, err := withRenderTimeout(context.Background(), "30s")
	require.NoError(t, err)
	defer cancel2()
	_, hasDeadline = ctx.Deadline()
	assert.True(t, hasDeadline)
	assert.Equal(t, 30*time.Second, d)

	_, _, _, err = withRenderTimeout(context.Background(), "2 minutes")
	assert.Error(t, err)
}

func TestWrapRenderTimeout(t *testing.T) {
	orig := errors.New("orig")

	parent := context.Background()
	timed, cancel := context.WithTimeout(parent, time.Nanosecond)
	defer cancel()
	<-timed.Done()
	err := wrapRenderTimeout(parent, timed, "helm", "app", time.Nanosecond, orig)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "timed out")

	pctx, pcancel := context.WithCancel(context.Background())
	tctx, tcancel := context.WithTimeout(pctx, time.Hour)
	defer tcancel()
	pcancel()
	err = wrapRenderTimeout(pctx, tctx, "helm", "app", time.Hour, context.Canceled)
	assert.Equal(t, context.Canceled, err)
	assert.NotContains(t, err.Error(), "timed out")

	assert.NoError(t, wrapRenderTimeout(parent, timed, "helm", "app", 0, nil))
}
