package cli

import (
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsoleHandlerEscapesControlCharacters(t *testing.T) {
	var buf bytes.Buffer
	h := &consoleHandler{w: &buf}
	file := "x\x1b]0;owned\x07.yaml"
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "bad\x1b[2Jmsg", 0)
	rec.AddAttrs(
		slog.String("file", file),
		slog.String("bidi", "a\u202eb"),
		slog.String("c1", "a\u009bb"),
	)
	require.NoError(t, h.Handle(context.Background(), rec))

	out := buf.String()
	require.True(t, strings.HasSuffix(out, "\n"))
	for i := 0; i < len(out)-1; i++ {
		assert.GreaterOrEqual(t, out[i], byte(0x20), "control byte at %d in %q", i, out)
	}
	assert.NotContains(t, out, "\u202e")
	assert.NotContains(t, out, "\u009b")
	assert.Contains(t, out, strconv.Quote(file))
	assert.Contains(t, out, strconv.Quote("bad\x1b[2Jmsg"))
}

func TestConsoleHandlerPlainRecordUnchanged(t *testing.T) {
	var buf bytes.Buffer
	h := &consoleHandler{w: &buf}
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "hello", 0)
	rec.AddAttrs(slog.String("k", "v"))
	require.NoError(t, h.Handle(context.Background(), rec))
	assert.True(t, strings.HasSuffix(buf.String(), "INF hello k=v\n"), buf.String())
}
