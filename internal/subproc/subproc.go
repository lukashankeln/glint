// Package subproc runs external commands with a hard cap on captured stdout, a
// bounded copy of stderr and a bounded pipe-drain delay. It depends only on
// the standard library so any internal package can use it.
package subproc

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// ErrOutputLimit is returned by CappedBuffer.Write and wrapped by Run when a
// command's stdout exceeds the cap.
var ErrOutputLimit = errors.New("subprocess output over limit")

// DefaultWaitDelay is the default bound on how long the output pipes are
// drained after the child exits or the context is done, so a grandchild that
// inherited the pipes cannot hold cmd.Run open.
const DefaultWaitDelay = 2 * time.Second

// StderrKeep is how much subprocess stderr TruncatingWriter retains.
const StderrKeep = 64 << 10

// CappedBuffer collects writes up to a limit. The buffer is a named field,
// not embedded, so bytes.Buffer.ReadFrom is not promoted: io.Copy would prefer
// it and bypass the cap.
type CappedBuffer struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
}

// NewCappedBuffer returns a CappedBuffer that accepts at most limit bytes.
func NewCappedBuffer(limit int) *CappedBuffer { return &CappedBuffer{limit: limit} }

// Write stores p unless it would take the buffer past the limit, in which case
// nothing is stored, the buffer is marked exceeded and ErrOutputLimit is
// returned.
func (c *CappedBuffer) Write(p []byte) (int, error) {
	if c.exceeded || c.buf.Len()+len(p) > c.limit {
		c.exceeded = true
		return 0, ErrOutputLimit
	}
	return c.buf.Write(p)
}

// Exceeded reports whether a write was rejected for passing the limit.
func (c *CappedBuffer) Exceeded() bool { return c.exceeded }

// Bytes returns the collected output.
func (c *CappedBuffer) Bytes() []byte { return c.buf.Bytes() }

// TruncatingWriter keeps the first StderrKeep bytes written and discards the
// rest without ever returning an error, so a chatty child is never blocked.
type TruncatingWriter struct {
	buf       bytes.Buffer
	truncated bool
}

// Write always reports len(p), nil.
func (t *TruncatingWriter) Write(p []byte) (int, error) {
	room := max(StderrKeep-t.buf.Len(), 0)
	if room < len(p) {
		t.truncated = true
		t.buf.Write(p[:room])
		return len(p), nil
	}
	t.buf.Write(p)
	return len(p), nil
}

// String returns the kept output, with a marker if anything was dropped.
func (t *TruncatingWriter) String() string {
	if t.truncated {
		return t.buf.String() + "\n[stderr truncated]"
	}
	return t.buf.String()
}

// Run sets cmd's Stdout, Stderr and WaitDelay, runs it and returns the
// captured stdout and (truncated) stderr.
//
// If stdout passes stdoutLimit bytes the returned error wraps ErrOutputLimit
// and nothing else. Otherwise err is cmd.Run's error unchanged (for example
// *exec.ExitError or exec.ErrWaitDelay).
func Run(cmd *exec.Cmd, stdoutLimit int, waitDelay time.Duration) (stdout []byte, stderr string, err error) {
	out := NewCappedBuffer(stdoutLimit)
	errw := &TruncatingWriter{}
	cmd.Stdout = out
	cmd.Stderr = errw
	cmd.WaitDelay = waitDelay

	runErr := cmd.Run()
	// Check the cap first, whatever Run returned: on a writer error os/exec
	// closes the pipe, the child dies of SIGPIPE and Run reports "signal:
	// broken pipe"; Wait only returns the copy error when the child exited 0.
	// A child that ignored SIGPIPE would keep running until the context is
	// cancelled; helm, a Go program, dies on SIGPIPE on stdout.
	if out.Exceeded() {
		return out.Bytes(), errw.String(),
			fmt.Errorf("output exceeds the %d-byte limit: %w", stdoutLimit, ErrOutputLimit)
	}
	return out.Bytes(), errw.String(), runErr
}
