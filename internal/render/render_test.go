package render

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
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
