package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/rules"
)

func TestRenderErrorViolation(t *testing.T) {
	tests := []struct {
		name     string
		app      discovery.DiscoveredApp
		wantPath string
	}{
		{
			name: "file path from source",
			app: discovery.DiscoveredApp{
				Name: "web", RootPath: "/repo/charts/web",
				Source: &discovery.SourceRef{Path: "/repo/apps/web.yaml"},
			},
			wantPath: "/repo/apps/web.yaml",
		},
		{
			name:     "file path from root when no source",
			app:      discovery.DiscoveredApp{Name: "web", RootPath: "/repo/charts/web"},
			wantPath: "/repo/charts/web",
		},
		{
			name: "file path from root when source path empty",
			app: discovery.DiscoveredApp{
				Name: "web", RootPath: "/repo/charts/web", Source: &discovery.SourceRef{},
			},
			wantPath: "/repo/charts/web",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := renderErrorViolation(tt.app, errors.New("boom"))
			assert.Equal(t, "glint/render-error", v.RuleID)
			assert.Equal(t, rules.SeverityError, v.Severity)
			assert.Equal(t, "render", v.Source)
			assert.Equal(t, tt.wantPath, v.FilePath)
			assert.Contains(t, v.Message, `"web"`)
			assert.Contains(t, v.Message, "boom")
		})
	}
}

func TestRenderErrorViolation_ThresholdAndFilter(t *testing.T) {
	v := renderErrorViolation(discovery.DiscoveredApp{Name: "web", RootPath: "/x"}, errors.New("boom"))
	assert.True(t, failsThreshold([]rules.Violation{v}, []string{"error"}))

	kept := filterViolations([]rules.Violation{v}, "", "glint/render-error")
	assert.Empty(t, kept)

	kept = filterViolations([]rules.Violation{v}, "", "other-rule")
	assert.Len(t, kept, 1)
}

// runLintOnUnrenderableChart runs the lint command against a temp dir holding
// a single Helm chart whose template always fails to render.
func runLintOnUnrenderableChart(t *testing.T, extraArgs ...string) (string, error) {
	t.Helper()

	oldCfg := cfgFile
	cfgFile = ""
	t.Cleanup(func() { cfgFile = oldCfg })
	t.Setenv("GITHUB_ACTIONS", "")

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "templates"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Chart.yaml"),
		[]byte("apiVersion: v2\nname: broken\nversion: 0.1.0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "templates", "bad.yaml"),
		[]byte("{{ fail \"boom\" }}\n"), 0o644))

	var out bytes.Buffer
	cmd := newLintCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{dir}, extraArgs...))
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestLintCmd_RenderFailureIsReported(t *testing.T) {
	out, err := runLintOnUnrenderableChart(t)

	var vf *ViolationsFoundError
	require.ErrorAs(t, err, &vf)
	assert.Contains(t, out, "glint/render-error")
}

func TestLintCmd_RenderFailureCanBeSkipped(t *testing.T) {
	out, err := runLintOnUnrenderableChart(t, "--skip-rules", "glint/render-error")

	assert.NoError(t, err)
	assert.NotContains(t, out, "glint/render-error")
}
