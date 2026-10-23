package render

import (
	"context"
	"fmt"
	"time"
)

// withRenderTimeout derives a context that expires after the Go duration in
// timeout. An empty timeout disables the deadline. The returned duration is 0
// when disabled.
func withRenderTimeout(ctx context.Context, timeout string) (context.Context, context.CancelFunc, time.Duration, error) {
	if timeout == "" {
		c, cancel := context.WithCancel(ctx)
		return c, cancel, 0, nil
	}
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("invalid duration %q", timeout)
	}
	c, cancel := context.WithTimeout(ctx, d)
	return c, cancel, d, nil
}

// runWithContext runs fn in a goroutine and returns when fn finishes or ctx is
// done, whichever comes first. krusty.Run takes no context and helm's
// LocateChart and loader.Load are not context-aware, so on cancellation the
// goroutine is abandoned and keeps running (for example finishing a chart
// download into the helm cache) until the CLI exits.
func runWithContext[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type result struct {
		val T
		err error
	}
	ch := make(chan result, 1) // buffered so an abandoned goroutine never blocks
	go func() {
		v, err := fn()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.val, r.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// wrapRenderTimeout converts err into a "timed out" error when the render's own
// deadline (timed) expired while the parent context is still live. Otherwise
// err is returned unchanged.
func wrapRenderTimeout(parent, timed context.Context, renderer, app string, d time.Duration, err error) error {
	if err == nil || parent.Err() != nil || timed.Err() != context.DeadlineExceeded {
		return err
	}
	return fmt.Errorf("%s render of %q timed out after %s: %w", renderer, app, d, context.DeadlineExceeded)
}
