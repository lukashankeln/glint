package discovery

import (
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
