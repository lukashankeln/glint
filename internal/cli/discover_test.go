package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/discovery"
)

func TestPrintDiscoverText_EscapesRepositoryControlledValues(t *testing.T) {
	var buf bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&buf)

	hostilePath := filepath.Join(t.TempDir(), "p\x1b[2J\u202e")
	apps := []discovery.DiscoveredApp{{
		Name:      "name\x1b[2J",
		Renderer:  discovery.RendererType("rend\u202e"),
		Framework: discovery.Framework("fw\tx"),
		ChartName: "chart\x1b[2Jc",
		RepoURL:   "https://example.com/r\u202e",
		Source:    &discovery.SourceRef{Path: "/abs/src\tfile\x1b[2J"},
	}}

	require.NoError(t, printDiscoverText(c, []string{hostilePath}, apps))
	out := buf.String()

	assert.NotContains(t, out, "\x1b")
	assert.NotContains(t, out, "\u202e")
	assert.NotContains(t, out, "\t")
	for _, want := range []string{
		`name\x1b[2J`,
		`rend\u202e`,
		`fw\tx`,
		`chart\x1b[2Jc`,
		`src\tfile\x1b[2J`,
		`p\x1b[2J\u202e`,
	} {
		assert.Contains(t, out, want)
	}
	assert.Equal(t, 1, strings.Count(out, "Discovered 1 app(s) in "))
}

func TestPrintDiscoverText_EmptyEscapesPaths(t *testing.T) {
	var buf bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&buf)

	require.NoError(t, printDiscoverText(c, []string{"a\x1b[2J", "b\u202e"}, nil))

	assert.Equal(t, "No apps discovered in a\\x1b[2J, b\\u202e\n", buf.String())
}
