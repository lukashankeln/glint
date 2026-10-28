package render

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// errOutputLimit is returned by cappedBuffer.Write and wrapped by the
// subprocess renderers when a command's stdout exceeds the stream cap.
var errOutputLimit = errors.New("subprocess output over limit")

// execCommandContext is exec.CommandContext, replaceable in tests.
var execCommandContext = exec.CommandContext

// stderrKeep is how much subprocess stderr truncatingWriter retains.
const stderrKeep = 64 << 10

// readFileCapped reads path, failing with errStreamTooLarge if it holds more
// than limit bytes. Non-regular files (e.g. a symlink to /dev/zero) are read
// through a limit reader so they cannot exhaust memory either.
func readFileCapped(path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode().IsRegular() && info.Size() > int64(limit) {
		return nil, fmt.Errorf("file is %d bytes, over the %d-byte limit: %w",
			info.Size(), limit, errStreamTooLarge)
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("file is over the %d-byte limit: %w", limit, errStreamTooLarge)
	}
	return data, nil
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
