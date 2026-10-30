package render

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
)

func kustCfg(restrictor string, enableHelm bool) *config.Config {
	cfg := defaultCfg()
	cfg.Render.Kustomize.LoadRestrictor = restrictor
	cfg.Render.Kustomize.EnableHelm = enableHelm
	return cfg
}

// writeFiles writes relative path -> content pairs under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
}

func TestKustomizeSDK_LoadRestrictor(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"cm.yaml":                    kustCM,
		"overlay/kustomization.yaml": "resources:\n- ../cm.yaml\n",
	})
	app := discovery.DiscoveredApp{Name: "k", Renderer: discovery.RendererKustomize, RootPath: filepath.Join(dir, "overlay")}

	for _, v := range []string{"", config.LoadRestrictorRootOnly} {
		_, err := newKustomizeRenderer(kustCfg(v, false)).renderSDK(context.Background(), app)
		require.Error(t, err, "%q", v)
		assert.Contains(t, err.Error(), "is not in or below")
	}

	ms, err := newKustomizeRenderer(kustCfg(config.LoadRestrictorNone, false)).renderSDK(context.Background(), app)
	require.NoError(t, err)
	require.Len(t, ms, 1)
	assert.Equal(t, "a", ms[0].Name)
}

func TestKustomizeSDK_EnableHelm(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	dir := t.TempDir()
	// The chart sits in the default chartHome (charts/) under the root, so no pull happens.
	writeFiles(t, dir, map[string]string{
		"kustomization.yaml":               "helmCharts:\n- name: mychart\n",
		"charts/mychart/Chart.yaml":        "apiVersion: v2\nname: mychart\nversion: 0.1.0\n",
		"charts/mychart/templates/cm.yaml": kustCM,
		"charts/mychart/values.yaml":       "{}\n",
	})
	app := discovery.DiscoveredApp{Name: "k", Renderer: discovery.RendererKustomize, RootPath: dir}

	_, err := newKustomizeRenderer(kustCfg("", false)).renderSDK(context.Background(), app)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must specify --enable-helm")

	ms, err := newKustomizeRenderer(kustCfg("", true)).renderSDK(context.Background(), app)
	require.NoError(t, err)
	require.Len(t, ms, 1)
	assert.Equal(t, "a", ms[0].Name)
}

func TestKustomizeSubprocess_Args(t *testing.T) {
	tests := []struct {
		name       string
		restrictor string
		helm       bool
		want       []string
	}{
		{"default", "", false, []string{"build", "--load-restrictor", "LoadRestrictionsRootOnly", "/app"}},
		{"rootOnly", config.LoadRestrictorRootOnly, false, []string{"build", "--load-restrictor", "LoadRestrictionsRootOnly", "/app"}},
		{"none", config.LoadRestrictorNone, false, []string{"build", "--load-restrictor", "LoadRestrictionsNone", "/app"}},
		{"helm", "", true, []string{"build", "--load-restrictor", "LoadRestrictionsRootOnly", "--enable-helm", "/app"}},
		{"none+helm", config.LoadRestrictorNone, true, []string{"build", "--load-restrictor", "LoadRestrictionsNone", "--enable-helm", "/app"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeExec(t, "manifest")
			fake := execCommandContext
			var gotName string
			var gotArgs []string
			execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
				gotName, gotArgs = name, args
				return fake(ctx, name, args...)
			}
			app := discovery.DiscoveredApp{Name: "x", RootPath: "/app"}
			_, err := renderKustomizeSubprocess(context.Background(), kustCfg(tt.restrictor, tt.helm), app)
			require.NoError(t, err)
			assert.Equal(t, "kustomize", gotName)
			assert.Equal(t, tt.want, gotArgs)
		})
	}
}
