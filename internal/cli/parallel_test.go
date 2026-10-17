package cli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lukashankeln/glint/internal/config"
	"github.com/lukashankeln/glint/internal/discovery"
)

const serviceMonitorCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: servicemonitors.monitoring.coreos.com
spec:
  group: monitoring.coreos.com
  names:
    kind: ServiceMonitor
    plural: servicemonitors
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
`

func TestRenderAppsParallel_DiscoveredCRDsEnableCapabilities(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	chart := filepath.Join(filepath.Dir(file), "..", "..", "test", "testdata", "capability-chart")

	crdDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(crdDir, "crd.yaml"), []byte(serviceMonitorCRD), 0o644))

	apps := []discovery.DiscoveredApp{
		{Name: "chart", Renderer: discovery.RendererHelm, RootPath: chart, ReleaseName: "chart", Namespace: "default"},
		{Name: "crds", Renderer: discovery.RendererRaw, RootPath: crdDir},
	}

	cfg, err := config.Load("")
	require.NoError(t, err)

	results := renderAppsParallel(context.Background(), apps, cfg)
	require.NoError(t, results[0].err)
	require.NoError(t, results[1].err)
	require.Len(t, results[0].manifests, 1)
	assert.Equal(t, "ServiceMonitor", results[0].manifests[0].Kind)

	cfg.Render.Helm.DiscoverAPIVersions = false
	results = renderAppsParallel(context.Background(), apps, cfg)
	assert.Error(t, results[0].err, "without discovery the chart's capability guard fails")
}
