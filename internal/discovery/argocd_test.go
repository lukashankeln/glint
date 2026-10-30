package discovery

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArgoCDApplication_OCIChartInRepoURL(t *testing.T) {
	raw := []byte(`apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: calibre-web
spec:
  source:
    repoURL: oci://ghcr.io/org/helm-charts/calibre-web
    path: "."
    targetRevision: 1.1.4
    helm:
      valuesObject:
        replicas: 2
  destination:
    namespace: media
`)

	app, err := parseArgoCDApplication(raw, "/repo", "/repo/apps/calibre-web.yaml", nil)
	require.NoError(t, err)
	require.NotNil(t, app)
	assert.Equal(t, RendererHelm, app.Renderer)
	assert.Empty(t, app.RootPath)
	assert.Equal(t, "oci://ghcr.io/org/helm-charts", app.RepoURL)
	assert.Equal(t, "calibre-web", app.ChartName)
	assert.Equal(t, "1.1.4", app.ChartVersion)
	assert.Equal(t, "media", app.Namespace)
	assert.Equal(t, map[string]any{"replicas": float64(2)}, app.InlineValues)
}

func TestSplitOCIChartURL(t *testing.T) {
	tests := []struct {
		repoURL, path       string
		wantRepo, wantChart string
	}{
		{"oci://ghcr.io/org/charts/app", ".", "oci://ghcr.io/org/charts", "app"},
		{"oci://ghcr.io/org/charts/app/", "", "oci://ghcr.io/org/charts", "app"},
		{"oci://ghcr.io/org/charts/app", "sub/dir", "oci://ghcr.io/org/charts/app", ""},
		{"oci://ghcr.io", ".", "oci://ghcr.io", ""},
		{"https://charts.example.com", ".", "https://charts.example.com", ""},
	}
	for _, tt := range tests {
		repo, chart := splitOCIChartURL(tt.repoURL, tt.path)
		assert.Equal(t, tt.wantRepo, repo, tt.repoURL)
		assert.Equal(t, tt.wantChart, chart, tt.repoURL)
	}
}

func argoAppDoc(path string, valueFiles ...string) []byte {
	doc := "apiVersion: argoproj.io/v1alpha1\nkind: Application\nmetadata:\n  name: app\nspec:\n  source:\n    repoURL: \".\"\n    path: " + path + "\n"
	if len(valueFiles) > 0 {
		doc += "    helm:\n      valueFiles:\n"
		for _, vf := range valueFiles {
			doc += "        - " + vf + "\n"
		}
	}
	return []byte(doc)
}

func TestParseArgoCDApplication_SourcePathContainment(t *testing.T) {
	root := t.TempDir()

	_, err := parseArgoCDApplication(argoAppDoc("../../x"), root, filepath.Join(root, "app.yaml"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `ArgoCD Application "app"`)
	assert.Contains(t, err.Error(), `spec.source.path "../../x"`)
	assert.Contains(t, err.Error(), "outside the repository root")

	app, err := parseArgoCDApplication(argoAppDoc("apps/a/../../charts/x"), root, filepath.Join(root, "app.yaml"), nil)
	require.NoError(t, err)
	require.NotNil(t, app)
	assert.Equal(t, filepath.Join(root, "charts", "x"), app.RootPath)
}

func TestParseArgoCDApplication_ValueFilesContainment(t *testing.T) {
	root := writeTree(t, map[string]string{"charts/x/Chart.yaml": "apiVersion: v2\nname: x\nversion: 0.1.0\n"})
	src := filepath.Join(root, "app.yaml")

	_, err := parseArgoCDApplication(argoAppDoc("charts/x", "../../../etc/x"), root, src, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `spec.source.helm.valueFiles "../../../etc/x"`)

	app, err := parseArgoCDApplication(argoAppDoc("charts/x", "../values.yaml"), root, src, nil)
	require.NoError(t, err)
	require.NotNil(t, app)
	assert.Equal(t, []string{filepath.Join(root, "charts", "values.yaml")}, app.ValuesFiles)

	app, err = parseArgoCDApplication(argoAppDoc("charts/x", "https://example.com/v.yaml"), root, src, nil)
	require.NoError(t, err)
	require.NotNil(t, app)
	assert.Equal(t, RendererHelm, app.Renderer)
}

func TestDiscover_ArgoCDEscapingPathIsParseError(t *testing.T) {
	root := writeTree(t, map[string]string{
		".git/HEAD":     "ref: refs/heads/main\n",
		"apps/app.yaml": string(argoAppDoc("../../outside")),
	})

	apps, perrs, err := Discover(context.Background(), []string{root}, defaultCfg())
	require.NoError(t, err)
	require.Len(t, perrs, 1)
	assert.Equal(t, filepath.Join(root, "apps", "app.yaml"), perrs[0].FilePath)
	assert.Contains(t, perrs[0].Message, "outside the repository root")
	outside := filepath.Clean(filepath.Join(root, "../../outside"))
	for _, a := range apps {
		assert.NotEqual(t, outside, a.RootPath)
	}
	assert.Empty(t, filterByFramework(apps, FrameworkArgoCD))
}
