package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTree creates files (relative path -> content) below a new temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	return root
}

func countByRootPath(apps []DiscoveredApp) map[string]int {
	count := map[string]int{}
	for _, a := range apps {
		count[a.RootPath]++
	}
	return count
}

func TestDiscover_ArgoCDApplicationsWinOverPlainChart(t *testing.T) {
	application := func(name string) string {
		return `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: ` + name + `
spec:
  source:
    repoURL: "."
    path: charts/app
    helm:
      valuesObject:
        instance: ` + name + `
  destination:
    namespace: ` + name + `
`
	}
	root := writeTree(t, map[string]string{
		"charts/app/Chart.yaml":  "apiVersion: v2\nname: app\nversion: 0.1.0\n",
		"charts/app/values.yaml": "instance: default\n",
		"apps/apps.yaml":         application("blue") + "---\n" + application("green"),
	})

	apps, _, err := Discover(context.Background(), []string{root}, defaultCfg())
	require.NoError(t, err)

	chart := filepath.Join(root, "charts", "app")
	assert.Equal(t, 2, countByRootPath(apps)[chart])
	for _, a := range apps {
		if a.RootPath == chart {
			assert.Equal(t, FrameworkArgoCD, a.Framework)
			assert.Equal(t, map[string]any{"instance": a.Name}, a.InlineValues)
		}
	}
}

func TestDiscover_FluxKustomizationWinsOverPlainOverlay(t *testing.T) {
	root := writeTree(t, map[string]string{
		"clusters/prod/apps.yaml": `apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: apps
  namespace: flux-system
spec:
  path: ./apps
  targetNamespace: apps
`,
		"apps/kustomization.yaml":  "resources:\n  - cm.yaml\n",
		"apps/cm.yaml":             "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n",
		"plain/kustomization.yaml": "resources:\n  - cm.yaml\n",
		"plain/cm.yaml":            "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n",
	})

	apps, _, err := Discover(context.Background(), []string{root}, defaultCfg())
	require.NoError(t, err)

	count := countByRootPath(apps)
	assert.Equal(t, 1, count[filepath.Join(root, "apps")])
	assert.Equal(t, 1, count[filepath.Join(root, "plain")])
	for _, a := range apps {
		switch a.RootPath {
		case filepath.Join(root, "apps"):
			assert.Equal(t, FrameworkFlux, a.Framework)
			assert.Equal(t, "apps", a.Namespace)
		case filepath.Join(root, "plain"):
			assert.Equal(t, FrameworkPlain, a.Framework)
		}
	}
}
