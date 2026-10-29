package render

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"

	"github.com/lukashankeln/glint/internal/fsutil"
)

// errOutputLimit is returned by cappedBuffer.Write and wrapped by the
// subprocess renderers when a command's stdout exceeds the stream cap.
var errOutputLimit = errors.New("subprocess output over limit")

// execCommandContext is exec.CommandContext, replaceable in tests.
var execCommandContext = exec.CommandContext

// stderrKeep is how much subprocess stderr truncatingWriter retains.
const stderrKeep = 64 << 10

// readFileCapped reads path through fsutil.ReadFileCapped. A file over limit
// returns an error wrapping both errStreamTooLarge and fsutil.ErrTooLarge; a
// non-regular file (e.g. a symlink to /dev/zero or a FIFO) wraps
// fsutil.ErrNotRegular and is never opened.
func readFileCapped(path string, limit int) ([]byte, error) {
	data, err := fsutil.ReadFileCapped(path, limit)
	if errors.Is(err, fsutil.ErrTooLarge) {
		return nil, fmt.Errorf("%w: %w", errStreamTooLarge, err)
	}
	return data, err
}

// cappedBuffer collects writes up to limit bytes. The buffer is a named field,
// not embedded, so bytes.Buffer.ReadFrom is not promoted: io.Copy would prefer
// it and bypass the cap.
type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
}

// Write stores p unless it would take the buffer past limit, in which case
// nothing is stored, exceeded is set and errOutputLimit is returned.
func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.exceeded || c.buf.Len()+len(p) > c.limit {
		c.exceeded = true
		return 0, errOutputLimit
	}
	return c.buf.Write(p)
}

// Bytes returns the collected output.
func (c *cappedBuffer) Bytes() []byte { return c.buf.Bytes() }

// truncatingWriter keeps the first stderrKeep bytes written and discards the
// rest without ever returning an error, so a chatty child is never blocked.
type truncatingWriter struct {
	buf       bytes.Buffer
	truncated bool
}

// Write always reports len(p), nil.
func (t *truncatingWriter) Write(p []byte) (int, error) {
	room := stderrKeep - t.buf.Len()
	if room < len(p) {
		t.truncated = true
		p2 := p[:max(room, 0)]
		t.buf.Write(p2)
		return len(p), nil
	}
	t.buf.Write(p)
	return len(p), nil
}

// String returns the kept output, with a marker if anything was dropped.
func (t *truncatingWriter) String() string {
	if t.truncated {
		return t.buf.String() + "\n[stderr truncated]"
	}
	return t.buf.String()
}
