package subproc

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain lets the test binary act as the subprocess for the Run tests.
func TestMain(m *testing.M) {
	switch os.Getenv("SUBPROC_TEST_MODE") {
	case "flood":
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		for {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(1)
			}
		}
	case "ok":
		_, _ = os.Stdout.WriteString("hello")
		os.Exit(0)
	case "fail":
		_, _ = os.Stdout.WriteString("partial")
		_, _ = os.Stderr.WriteString("boom")
		os.Exit(3)
	}
	os.Exit(m.Run())
}

func helperCmd(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "SUBPROC_TEST_MODE="+mode)
	return cmd
}

func TestCappedBuffer_Write(t *testing.T) {
	c := NewCappedBuffer(4)
	n, err := c.Write([]byte("abcd"))
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.False(t, c.Exceeded())
	_, err = c.Write([]byte("e"))
	assert.True(t, errors.Is(err, ErrOutputLimit))
	assert.True(t, c.Exceeded())
	assert.Equal(t, "abcd", string(c.Bytes()))
}

func TestCappedBuffer_IOCopyFromPipeStopsAtCap(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	go func() {
		defer w.Close()
		chunk := make([]byte, 1024)
		for range 64 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}()
	c := NewCappedBuffer(4096)
	_, err = io.Copy(c, r)
	assert.True(t, errors.Is(err, ErrOutputLimit))
	assert.True(t, c.Exceeded())
	assert.LessOrEqual(t, len(c.Bytes()), 4096)
}

func TestTruncatingWriter(t *testing.T) {
	w := &TruncatingWriter{}
	n, err := w.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", w.String())

	big := make([]byte, StderrKeep)
	n, err = w.Write(big)
	require.NoError(t, err)
	assert.Equal(t, len(big), n)
	n, err = w.Write([]byte("more"))
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	s := w.String()
	assert.True(t, strings.HasSuffix(s, "\n[stderr truncated]"))
	assert.Equal(t, StderrKeep, w.buf.Len())
	assert.True(t, strings.HasPrefix(s, "hello"))
	assert.NotContains(t, w.buf.String(), "more")
}

func TestTruncatingWriter_ChunkedCrossingCap(t *testing.T) {
	w := &TruncatingWriter{}
	chunk := bytes.Repeat([]byte("a"), 24<<10)
	for range 4 { // 96 KiB total, cap is 64 KiB
		n, err := w.Write(chunk)
		require.NoError(t, err)
		require.Equal(t, len(chunk), n)
	}
	assert.Equal(t, StderrKeep, w.buf.Len())
	assert.Equal(t, StderrKeep+len("\n[stderr truncated]"), len(w.String()))
}

func TestRun_FloodHitsLimit(t *testing.T) {
	cmd := helperCmd("flood")
	start := time.Now()
	stdout, _, err := Run(cmd, 1<<20, DefaultWaitDelay)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrOutputLimit), "got %v", err)
	var ee *exec.ExitError
	assert.False(t, errors.As(err, &ee), "must not also wrap the run error")
	assert.LessOrEqual(t, len(stdout), 1<<20)
	assert.Less(t, time.Since(start), 10*time.Second)
}

func TestRun_CleanExit(t *testing.T) {
	stdout, stderr, err := Run(helperCmd("ok"), 1<<10, DefaultWaitDelay)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(stdout))
	assert.Empty(t, stderr)
}

func TestRun_NonZeroExit(t *testing.T) {
	_, stderr, err := Run(helperCmd("fail"), 1<<10, DefaultWaitDelay)
	var ee *exec.ExitError
	require.True(t, errors.As(err, &ee), "got %v", err)
	assert.Equal(t, 3, ee.ExitCode())
	assert.False(t, errors.Is(err, ErrOutputLimit))
	assert.Equal(t, "boom", stderr)
}
