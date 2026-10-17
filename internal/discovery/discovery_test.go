package discovery

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
)

// testdataPath returns the absolute path to test/testdata/<name>.
func testdataPath(name string) string {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "test", "testdata")
	return filepath.Join(root, name)
}

func defaultCfg() *config.Config {
	cfg, _ := config.Load("")
	return cfg
}

func TestDiscover_HelmChart(t *testing.T) {
	path := testdataPath("argocd-helm-app")
	apps, _, err := Discover(context.Background(), []string{path}, defaultCfg())
	require.NoError(t, err)

	// Expect at least one Helm app (the chart at charts/frontend)
	helmApps := filterByRenderer(apps, RendererHelm)
	assert.NotEmpty(t, helmApps, "expected at least one Helm app")

	// One of the apps should be the frontend chart
	assert.True(t, containsName(helmApps, "frontend"), "expected app named 'frontend'")
}

func TestDiscover_ArgoCDApplication(t *testing.T) {
	path := testdataPath("argocd-helm-app")
	apps, _, err := Discover(context.Background(), []string{path}, defaultCfg())
	require.NoError(t, err)

	argoApps := filterByFramework(apps, FrameworkArgoCD)
	// The Application CRD in apps/frontend.yaml should be picked up.
	// It may overlap with the chart discovery — both are valid.
	assert.NotEmpty(t, argoApps, "expected at least one ArgoCD app")
	assert.Equal(t, "production", argoApps[0].Namespace)
}

func TestDiscover_FluxHelmRelease(t *testing.T) {
	path := testdataPath("flux-helmrelease")
	apps, _, err := Discover(context.Background(), []string{path}, defaultCfg())
	require.NoError(t, err)

	fluxApps := filterByFramework(apps, FrameworkFlux)
	require.NotEmpty(t, fluxApps, "expected at least one Flux app")
	assert.Equal(t, "backend", fluxApps[0].Name)
	assert.Equal(t, RendererHelm, fluxApps[0].Renderer)
	assert.Equal(t, "production", fluxApps[0].Namespace)
}

func TestDiscover_KustomizeOverlay(t *testing.T) {
	path := testdataPath("kustomize-overlay")
	apps, _, err := Discover(context.Background(), []string{path}, defaultCfg())
	require.NoError(t, err)

	kApps := filterByRenderer(apps, RendererKustomize)
	assert.NotEmpty(t, kApps, "expected at least one Kustomize app")

	// Should discover both base and production overlay
	names := appNames(kApps)
	assert.Contains(t, names, "base")
	assert.Contains(t, names, "production")
}

func TestDiscover_RawYAML(t *testing.T) {
	path := testdataPath("raw-yaml")
	apps, _, err := Discover(context.Background(), []string{path}, defaultCfg())
	require.NoError(t, err)

	rawApps := filterByRenderer(apps, RendererRaw)
	assert.NotEmpty(t, rawApps, "expected at least one raw YAML app")
}

func TestDiscover_ExcludePattern(t *testing.T) {
	path := testdataPath("kustomize-overlay")
	cfg := defaultCfg()
	cfg.Discovery.Exclude = append(cfg.Discovery.Exclude, "**/overlays/**")

	apps, _, err := Discover(context.Background(), []string{path}, cfg)
	require.NoError(t, err)

	// The production overlay should be excluded
	for _, app := range apps {
		assert.NotContains(t, app.RootPath, "overlays/production",
			"production overlay should be excluded")
	}
}

func TestDiscover_NoDuplicates(t *testing.T) {
	path := testdataPath("argocd-helm-app")
	apps, _, err := Discover(context.Background(), []string{path}, defaultCfg())
	require.NoError(t, err)

	rootPaths := make(map[string]int)
	for _, app := range apps {
		rootPaths[app.RootPath]++
	}
	for p, count := range rootPaths {
		assert.Equal(t, 1, count, "duplicate app at path %s", p)
	}
}

func TestDiscover_FluxRemoteHelmRelease(t *testing.T) {
	path := testdataPath("flux-remote-helmrelease")
	apps, _, err := Discover(context.Background(), []string{path}, defaultCfg())
	require.NoError(t, err)

	fluxApps := filterByFramework(apps, FrameworkFlux)
	require.NotEmpty(t, fluxApps, "expected at least one Flux app")

	// Find the podinfo remote HelmRelease
	var podinfo *DiscoveredApp
	for i := range fluxApps {
		if fluxApps[i].Name == "podinfo" {
			podinfo = &fluxApps[i]
			break
		}
	}
	require.NotNil(t, podinfo, "expected app named 'podinfo'")
	assert.Equal(t, RendererHelm, podinfo.Renderer)
	assert.Equal(t, "https://stefanprodan.github.io/podinfo", podinfo.RepoURL)
	assert.Equal(t, "podinfo", podinfo.ChartName)
	assert.Equal(t, "6.x", podinfo.ChartVersion)
	assert.Equal(t, "production", podinfo.Namespace)
	assert.Equal(t, "", podinfo.RootPath, "remote chart should have empty RootPath")
}

func TestDiscover_ArgoCDApplicationDirIsRaw(t *testing.T) {
	// The apps/ directory in argocd-helm-app contains only an ArgoCD Application CRD.
	// Previously crdFound=true caused it to be excluded from rawDirs, so the
	// Application YAML itself was never CEL-evaluated. Now it must appear as a raw app.
	path := testdataPath("argocd-helm-app")
	apps, _, err := Discover(context.Background(), []string{path}, defaultCfg())
	require.NoError(t, err)

	// apps/frontend.yaml is an ArgoCD Application — its directory must also be a raw app.
	rawApps := filterByRenderer(apps, RendererRaw)
	rawPaths := make([]string, 0, len(rawApps))
	for _, a := range rawApps {
		rawPaths = append(rawPaths, a.RootPath)
	}
	hasAppsDir := false
	for _, p := range rawPaths {
		if filepath.Base(p) == "apps" {
			hasAppsDir = true
			break
		}
	}
	assert.True(t, hasAppsDir, "apps/ directory (ArgoCD Application CRDs) should be discoverable as a raw app for CEL evaluation; got raw paths: %v", rawPaths)
}

func TestDiscover_ArgoCDApplicationSet(t *testing.T) {
	path := testdataPath("argocd-appset")
	apps, _, err := Discover(context.Background(), []string{path}, defaultCfg())
	require.NoError(t, err)

	// ApplicationSets must not be expanded into ArgoCD DiscoveredApps.
	argoApps := filterByFramework(apps, FrameworkArgoCD)
	assert.Empty(t, argoApps, "ApplicationSets should not be treated as renderable ArgoCD apps")

	// The directory should still be picked up as raw YAML
	rawApps := filterByRenderer(apps, RendererRaw)
	assert.NotEmpty(t, rawApps, "expected raw YAML app for ApplicationSet directory")

	// No duplicates.
	rootPaths := make(map[string]int)
	for _, app := range apps {
		rootPaths[app.RootPath]++
	}
	for p, count := range rootPaths {
		assert.Equal(t, 1, count, "duplicate app at path %s", p)
	}
}

func TestDiscover_ArgoCDRemoteHelm(t *testing.T) {
	path := testdataPath("argocd-remote-helm")
	apps, _, err := Discover(context.Background(), []string{path}, defaultCfg())
	require.NoError(t, err)

	argoApps := filterByFramework(apps, FrameworkArgoCD)
	require.NotEmpty(t, argoApps, "expected at least one ArgoCD app")

	var nginxApp *DiscoveredApp
	for i := range argoApps {
		if argoApps[i].Name == "nginx-ingress" {
			nginxApp = &argoApps[i]
			break
		}
	}
	require.NotNil(t, nginxApp, "expected app named 'nginx-ingress'")
	assert.Equal(t, RendererHelm, nginxApp.Renderer)
	assert.Equal(t, "https://kubernetes.github.io/ingress-nginx", nginxApp.RepoURL)
	assert.Equal(t, "ingress-nginx", nginxApp.ChartName)
	assert.Equal(t, "4.10.0", nginxApp.ChartVersion)
	assert.Equal(t, "ingress-nginx", nginxApp.Namespace)
	assert.Equal(t, "", nginxApp.RootPath, "remote chart should have empty RootPath")
}

func TestDiscover_ArgoCDDirectoryRecurse(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	app := func(name string, recurse bool) string {
		doc := "apiVersion: argoproj.io/v1alpha1\nkind: Application\nmetadata:\n  name: " + name +
			"\nspec:\n  source:\n    repoURL: \".\"\n    path: " + name + "\n"
		if recurse {
			doc += "    directory:\n      recurse: true\n"
		}
		return doc
	}
	cm := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"
	write("apps/apps.yaml", app("deep", true)+"---\n"+app("flat", false))
	write("deep/top.yaml", cm)
	write("deep/nested/inner.yaml", cm)
	write("flat/top.yaml", cm)
	write("flat/nested/inner.yaml", cm)

	apps, _, err := Discover(context.Background(), []string{root}, defaultCfg())
	require.NoError(t, err)

	byPath := map[string]DiscoveredApp{}
	for _, a := range apps {
		byPath[a.RootPath] = a
	}
	assert.True(t, byPath[filepath.Join(root, "deep")].Recurse)
	assert.False(t, byPath[filepath.Join(root, "flat")].Recurse)

	// Nested directories of a recursive app are rendered by it, not on their own.
	assert.NotContains(t, byPath, filepath.Join(root, "deep", "nested"))
	assert.Contains(t, byPath, filepath.Join(root, "flat", "nested"))
}

// --- detect.go unit tests ---

func TestDetectFramework(t *testing.T) {
	tests := []struct {
		apiVersion string
		kind       string
		want       Framework
	}{
		{"argoproj.io/v1alpha1", "Application", FrameworkArgoCD},
		{"argoproj.io/v1alpha1", "ApplicationSet", FrameworkArgoCD},
		{"helm.toolkit.fluxcd.io/v2", "HelmRelease", FrameworkFlux},
		{"kustomize.toolkit.fluxcd.io/v1", "Kustomization", FrameworkFlux},
		{"source.toolkit.fluxcd.io/v1", "GitRepository", FrameworkFlux},
		{"apps/v1", "Deployment", FrameworkPlain},
		{"v1", "Service", FrameworkPlain},
	}
	for _, tt := range tests {
		t.Run(tt.apiVersion+"/"+tt.kind, func(t *testing.T) {
			got := detectFramework(tt.apiVersion, tt.kind)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- exclude.go unit tests ---

func TestIsExcluded(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"vendor/**", "vendor/github.com/foo/bar", true},
		{"vendor/**", "myapp/vendor/foo", true},
		{"**/.git/**", ".git/objects/pack", true},
		{"**/.git/**", "some/path/.git/config", true},
		{"test/testdata/**", "test/testdata/argocd-helm-app/Chart.yaml", true},
		{"*.yaml", "Chart.yaml", true},
		{"vendor/**", "app/main.go", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.path, func(t *testing.T) {
			got := isExcluded(tt.path, []string{tt.pattern})
			assert.Equal(t, tt.want, got, "isExcluded(%q, %q)", tt.path, tt.pattern)
		})
	}
}

// --- helpers ---

func filterByRenderer(apps []DiscoveredApp, r RendererType) []DiscoveredApp {
	var out []DiscoveredApp
	for _, a := range apps {
		if a.Renderer == r {
			out = append(out, a)
		}
	}
	return out
}

func filterByFramework(apps []DiscoveredApp, f Framework) []DiscoveredApp {
	var out []DiscoveredApp
	for _, a := range apps {
		if a.Framework == f {
			out = append(out, a)
		}
	}
	return out
}

func appNames(apps []DiscoveredApp) []string {
	names := make([]string, len(apps))
	for i, a := range apps {
		names[i] = a.Name
	}
	return names
}

func containsName(apps []DiscoveredApp, name string) bool {
	for _, a := range apps {
		if a.Name == name {
			return true
		}
	}
	return false
}
