package cli

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommands_ApplyOutputColorToLogger checks that every command that loads
// config re-initialises the logger with output.color from the config file.
func TestCommands_ApplyOutputColorToLogger(t *testing.T) {
	cmds := map[string]struct {
		args func(dir string) []string
	}{
		"lint":       {func(dir string) []string { return []string{"lint", "--format", "text", dir} }},
		"render":     {func(dir string) []string { return []string{"render", dir} }},
		"discover":   {func(dir string) []string { return []string{"discover", dir} }},
		"rules list": {func(string) []string { return []string{"rules", "list"} }},
	}
	modes := map[string]bool{"always": true, "never": false}

	for name, c := range cmds {
		for mode, want := range modes {
			t.Run(name+"/"+mode, func(t *testing.T) {
				orig := slog.Default()
				t.Cleanup(func() { slog.SetDefault(orig) })

				dir := lintFixture(t)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "glint.yaml"),
					[]byte("output:\n  color: "+mode+"\n"), 0o644))

				// Start from the opposite of what the config requests. Only the
				// "always" cases guard the re-init: the root pre-run resets the
				// logger to "auto", which is uncolored on a non-TTY writer.
				slog.SetDefault(slog.New(&consoleHandler{w: &bytes.Buffer{}, color: !want}))

				root := newRootCmd()
				root.SetOut(&bytes.Buffer{})
				root.SetErr(&bytes.Buffer{})
				root.SetArgs(append(c.args(dir), "--config", filepath.Join(dir, "glint.yaml")))
				require.NoError(t, root.Execute())

				h, ok := slog.Default().Handler().(*consoleHandler)
				require.True(t, ok)
				assert.Equal(t, want, h.color)
			})
		}
	}
}
