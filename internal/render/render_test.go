package render

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
	"github.com/lukashankeln/glint/internal/manifest"
)

func testdataPath(name string) string {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "test", "testdata")
	return filepath.Join(root, name)
}

func defaultCfg() *config.Config {
	cfg, _ := config.Load("")
	return cfg
}

func TestHelmRenderer_LocalChart(t *testing.T) {
	chartPath := testdataPath("argocd-helm-app/charts/frontend")
	app := discovery.DiscoveredApp{
		Name:        "frontend",
		Framework:   discovery.FrameworkPlain,
		Renderer:    discovery.RendererHelm,
		RootPath:    chartPath,
		ReleaseName: "frontend",
		Namespace:   "default",
	}

	cfg := defaultCfg()
	renderer := New(app, cfg)
	manifests, err := renderer.Render(context.Background(), app)
	require.NoError(t, err)
	assert.NotEmpty(t, manifests, "expected at least one manifest from the frontend chart")

	// Verify we have some expected resource kinds
	kinds := make(map[string]bool)
	for _, m := range manifests {
		kinds[m.Kind] = true
	}
	// A typical Helm chart should produce at least a Deployment or Service
	hasExpectedKind := kinds["Deployment"] || kinds["Service"] || kinds["ConfigMap"] || kinds["Pod"]
	assert.True(t, hasExpectedKind, "expected at least one k8s resource kind, got: %v", kinds)
}

func TestHelmRenderer_APIVersions(t *testing.T) {
	chartPath := testdataPath("capability-chart")
	app := discovery.DiscoveredApp{
		Name:        "capability-chart",
		Framework:   discovery.FrameworkPlain,
		Renderer:    discovery.RendererHelm,
		RootPath:    chartPath,
		ReleaseName: "capability-chart",
		Namespace:   "default",
	}

	// Without the extra API version advertised, the chart hard-fails its
	// capability guard (mirrors the Traefik ServiceMonitor failure).
	cfg := defaultCfg()
	_, err := New(app, cfg).Render(context.Background(), app)
	require.Error(t, err, "expected render to fail when monitoring.coreos.com/v1 is not advertised")

	// Advertising it via config lets the chart render the ServiceMonitor.
	cfg = defaultCfg()
	cfg.Render.Helm.APIVersions = []string{"monitoring.coreos.com/v1"}
	manifests, err := New(app, cfg).Render(context.Background(), app)
	require.NoError(t, err)

	kinds := make(map[string]bool)
	for _, m := range manifests {
		kinds[m.Kind] = true
	}
	assert.True(t, kinds["ServiceMonitor"], "expected a ServiceMonitor, got: %v", kinds)
}

func TestKustomizeRenderer_Base(t *testing.T) {
	basePath := testdataPath("kustomize-overlay/base")
	app := discovery.DiscoveredApp{
		Name:      "base",
		Framework: discovery.FrameworkPlain,
		Renderer:  discovery.RendererKustomize,
		RootPath:  basePath,
	}

	cfg := defaultCfg()
	renderer := New(app, cfg)
	manifests, err := renderer.Render(context.Background(), app)
	require.NoError(t, err)
	assert.NotEmpty(t, manifests, "expected at least one manifest from the kustomize base")
}

func TestHelmRenderer_Timeout(t *testing.T) {
	app := discovery.DiscoveredApp{
		Name:        "frontend",
		Framework:   discovery.FrameworkPlain,
		Renderer:    discovery.RendererHelm,
		RootPath:    testdataPath("argocd-helm-app/charts/frontend"),
		ReleaseName: "frontend",
		Namespace:   "default",
	}
	cfg := defaultCfg()
	cfg.Render.Helm.Timeout = "1ns"
	_, err := New(app, cfg).Render(context.Background(), app)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "timed out")
}

func TestKustomizeRenderer_Timeout(t *testing.T) {
	app := discovery.DiscoveredApp{
		Name:      "base",
		Framework: discovery.FrameworkPlain,
		Renderer:  discovery.RendererKustomize,
		RootPath:  testdataPath("kustomize-overlay/base"),
	}
	cfg := defaultCfg()
	cfg.Render.Kustomize.Timeout = "1ns"
	_, err := New(app, cfg).Render(context.Background(), app)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "timed out")
}

func TestRawRenderer_RawYAML(t *testing.T) {
	rawPath := testdataPath("raw-yaml")
	app := discovery.DiscoveredApp{
		Name:      "raw-yaml",
		Framework: discovery.FrameworkPlain,
		Renderer:  discovery.RendererRaw,
		RootPath:  rawPath,
	}

	cfg := defaultCfg()
	renderer := New(app, cfg)
	manifests, err := renderer.Render(context.Background(), app)
	require.NoError(t, err)
	assert.NotEmpty(t, manifests, "expected at least one manifest from raw YAML dir")

	kinds := make(map[string]bool)
	for _, m := range manifests {
		kinds[m.Kind] = true
	}
	// Based on test/testdata/raw-yaml/rbac.yaml
	assert.True(t, kinds["ClusterRole"] || kinds["ClusterRoleBinding"],
		"expected ClusterRole or ClusterRoleBinding, got: %v", kinds)
}

func TestRawRenderer_SourcePathSet(t *testing.T) {
	rawPath := testdataPath("raw-yaml")
	app := discovery.DiscoveredApp{
		Name:      "raw-yaml",
		Framework: discovery.FrameworkPlain,
		Renderer:  discovery.RendererRaw,
		RootPath:  rawPath,
	}

	cfg := defaultCfg()
	renderer := New(app, cfg)
	manifests, err := renderer.Render(context.Background(), app)
	require.NoError(t, err)
	for _, m := range manifests {
		assert.NotEmpty(t, m.SourcePath, "SourcePath should be set for raw manifests")
		assert.False(t, m.Rendered, "raw manifests should not be marked as rendered")
	}
}

func TestRawRenderer_Recurse(t *testing.T) {
	root := t.TempDir()
	write := func(rel, name string) {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+name+"\n"), 0o644))
	}
	write("top.yaml", "top")
	write("nested/inner.yml", "inner")
	write(".hidden/ignored.yaml", "ignored")

	names := func(recurse bool) []string {
		app := discovery.DiscoveredApp{Name: "raw", Renderer: discovery.RendererRaw, RootPath: root, Recurse: recurse}
		manifests, err := New(app, defaultCfg()).Render(context.Background(), app)
		require.NoError(t, err)
		var out []string
		for _, m := range manifests {
			out = append(out, m.Name)
		}
		return out
	}

	assert.ElementsMatch(t, []string{"top"}, names(false))
	assert.ElementsMatch(t, []string{"top", "inner"}, names(true))
}

func TestRawRenderer_MalformedFileErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	content := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: ok\n---\nfoo: [unclosed\n"
	require.NoError(t, os.WriteFile(bad, []byte(content), 0o644))

	app := discovery.DiscoveredApp{Name: "raw", Renderer: discovery.RendererRaw, RootPath: dir}
	_, err := New(app, defaultCfg()).Render(context.Background(), app)
	require.Error(t, err)
	assert.Contains(t, err.Error(), bad)
}

func TestRendererFactory(t *testing.T) {
	cfg := defaultCfg()

	helmApp := discovery.DiscoveredApp{Renderer: discovery.RendererHelm}
	assert.IsType(t, &HelmRenderer{}, New(helmApp, cfg))

	kApp := discovery.DiscoveredApp{Renderer: discovery.RendererKustomize}
	assert.IsType(t, &KustomizeRenderer{}, New(kApp, cfg))

	rawApp := discovery.DiscoveredApp{Renderer: discovery.RendererRaw}
	assert.IsType(t, &RawRenderer{}, New(rawApp, cfg))
}

func TestNormalizeRepoURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Already-prefixed URLs are returned unchanged.
		{"oci://ghcr.io/actions/arc-charts", "oci://ghcr.io/actions/arc-charts"},
		{"https://charts.bitnami.com/bitnami", "https://charts.bitnami.com/bitnami"},
		{"http://internal.example.com/charts", "http://internal.example.com/charts"},
		{"file:///local/charts", "file:///local/charts"},
		// Bare OCI hostnames get the oci:// prefix.
		{"ghcr.io/actions/actions-runner-controller-charts", "oci://ghcr.io/actions/actions-runner-controller-charts"},
		{"registry.k8s.io/charts", "oci://registry.k8s.io/charts"},
		{"myregistry.azurecr.io/helm", "oci://myregistry.azurecr.io/helm"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeRepoURL(tt.input))
		})
	}
}

// assertRenderTimesOut runs render with an SDK stand-in that ignores its
// context and blocks, so only runWithContext can return control.
func assertRenderTimesOut(t *testing.T, render func(block func(context.Context, discovery.DiscoveredApp) ([]manifest.Manifest, error)) error) {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	block := func(context.Context, discovery.DiscoveredApp) ([]manifest.Manifest, error) {
		<-release
		return nil, nil
	}
	done := make(chan error, 1)
	go func() { done <- render(block) }()
	select {
	case err := <-done:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Contains(t, err.Error(), "timed out")
	case <-time.After(5 * time.Second):
		t.Fatal("Render did not return after its timeout; blocking SDK render was not abandoned")
	}
}

func TestHelmRenderer_TimeoutAbandonsBlockedSDK(t *testing.T) {
	cfg := defaultCfg()
	cfg.Render.Helm.Timeout = "50ms"
	app := discovery.DiscoveredApp{Name: "frontend"}
	assertRenderTimesOut(t, func(block func(context.Context, discovery.DiscoveredApp) ([]manifest.Manifest, error)) error {
		r := newHelmRenderer(cfg)
		r.sdk = block
		_, err := r.Render(context.Background(), app)
		return err
	})
}

func TestKustomizeRenderer_TimeoutAbandonsBlockedSDK(t *testing.T) {
	cfg := defaultCfg()
	cfg.Render.Kustomize.Timeout = "50ms"
	app := discovery.DiscoveredApp{Name: "base"}
	assertRenderTimesOut(t, func(block func(context.Context, discovery.DiscoveredApp) ([]manifest.Manifest, error)) error {
		r := newKustomizeRenderer(cfg)
		r.sdk = block
		_, err := r.Render(context.Background(), app)
		return err
	})
}
