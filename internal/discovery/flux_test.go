package discovery

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func appByName(t *testing.T, apps []DiscoveredApp, f Framework, name string) DiscoveredApp {
	t.Helper()
	for _, a := range apps {
		if a.Framework == f && a.Name == name {
			return a
		}
	}
	require.Failf(t, "app not found", "%s app %q not discovered", f, name)
	return DiscoveredApp{}
}

const cm = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"

func fluxKustomizationDoc(name, path, sourceKind, sourceName string) string {
	return `apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: ` + name + `
  namespace: flux-system
spec:
  path: ` + path + `
  targetNamespace: ` + name + `
  sourceRef:
    kind: ` + sourceKind + `
    name: ` + sourceName + `
`
}

func TestDiscover_FluxKustomizationWithoutKustomizationFileIsRecursiveRaw(t *testing.T) {
	root := writeTree(t, map[string]string{
		"clusters/prod/apps.yaml": fluxKustomizationDoc("apps", "./apps", "GitRepository", "flux-system"),
		"apps/cm.yaml":            cm,
		"apps/nested/other.yaml":  cm,
	})

	apps, _, err := Discover(context.Background(), []string{root}, defaultCfg())
	require.NoError(t, err)

	app := appByName(t, apps, FrameworkFlux, "apps")
	assert.Equal(t, RendererRaw, app.Renderer)
	assert.True(t, app.Recurse)
	for _, a := range apps {
		assert.NotEqual(t, filepath.Join(root, "apps", "nested"), a.RootPath)
	}
}

func TestDiscover_FluxGitRepositoryResolution(t *testing.T) {
	gitRepo := func(ns, name, url string) string {
		return "apiVersion: source.toolkit.fluxcd.io/v1\nkind: GitRepository\nmetadata:\n  name: " + name +
			"\n  namespace: " + ns + "\nspec:\n  url: " + url + "\n"
	}
	root := writeTree(t, map[string]string{
		"flux-system/gotk-sync.yaml": gitRepo("flux-system", "flux-system", "ssh://git@internal.example/org/repo") +
			"---\n" + gitRepo("flux-system", "foreign", "https://git.example.com/other/repo") +
			"---\n" + gitRepo("flux-system", "self", "https://git.example.com/org/repo"),
		"clusters/prod/ks.yaml": fluxKustomizationDoc("bootstrap", "./a", "GitRepository", "flux-system") +
			"---\n" + fluxKustomizationDoc("foreign", "./b", "GitRepository", "foreign") +
			"---\n" + fluxKustomizationDoc("self", "./c", "GitRepository", "self") +
			"---\n" + fluxKustomizationDoc("unresolved", "./d", "GitRepository", "applied-outside-git") +
			"---\n" + fluxKustomizationDoc("oci", "./e", "OCIRepository", "artifact"),
	})
	cfg := defaultCfg()
	cfg.Discovery.RepoAliases = []string{"git@git.example.com:org/repo.git"}

	apps, _, err := Discover(context.Background(), []string{root}, cfg)
	require.NoError(t, err)

	names := appNames(filterByFramework(apps, FrameworkFlux))
	assert.ElementsMatch(t, []string{"bootstrap", "self", "unresolved"}, names)
}

func TestDiscover_FluxHelmReleaseChartRef(t *testing.T) {
	root := writeTree(t, map[string]string{
		"sources.yaml": `apiVersion: source.toolkit.fluxcd.io/v1beta2
kind: OCIRepository
metadata:
  name: podinfo
  namespace: apps
spec:
  url: oci://ghcr.io/stefanprodan/charts/podinfo
  ref:
    tag: 6.7.0
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: HelmRepository
metadata:
  name: bitnami-free
  namespace: flux-system
spec:
  url: https://charts.example.com
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: HelmChart
metadata:
  name: redis
  namespace: apps
spec:
  chart: redis
  version: 1.2.3
  sourceRef:
    kind: HelmRepository
    name: bitnami-free
    namespace: flux-system
`,
		"releases.yaml": `apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: podinfo
  namespace: apps
spec:
  chartRef:
    kind: OCIRepository
    name: podinfo
---
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: redis
  namespace: apps
spec:
  chartRef:
    kind: HelmChart
    name: redis
---
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: missing
  namespace: apps
spec:
  chartRef:
    kind: OCIRepository
    name: does-not-exist
`,
	})

	apps, _, err := Discover(context.Background(), []string{root}, defaultCfg())
	require.NoError(t, err)

	podinfo := appByName(t, apps, FrameworkFlux, "podinfo")
	assert.Equal(t, "oci://ghcr.io/stefanprodan/charts", podinfo.RepoURL)
	assert.Equal(t, "podinfo", podinfo.ChartName)
	assert.Equal(t, "6.7.0", podinfo.ChartVersion)
	assert.Equal(t, "apps", podinfo.Namespace)

	redis := appByName(t, apps, FrameworkFlux, "redis")
	assert.Equal(t, "https://charts.example.com", redis.RepoURL)
	assert.Equal(t, "redis", redis.ChartName)
	assert.Equal(t, "1.2.3", redis.ChartVersion)

	assert.NotContains(t, appNames(filterByFramework(apps, FrameworkFlux)), "missing")
}

func TestDiscover_FluxHelmReleaseGitRepositoryChartPath(t *testing.T) {
	release := func(name string) string {
		return `apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: ` + name + `
  namespace: apps
spec:
  chart:
    spec:
      chart: charts/app
      sourceRef:
        kind: GitRepository
        name: flux-system
        namespace: flux-system
  values:
    instance: ` + name + `
`
	}
	root := writeTree(t, map[string]string{
		"charts/app/Chart.yaml":  "apiVersion: v2\nname: app\nversion: 0.1.0\n",
		"charts/app/values.yaml": "instance: default\n",
		"releases.yaml":          release("blue") + "---\n" + release("green"),
	})

	apps, _, err := Discover(context.Background(), []string{root}, defaultCfg())
	require.NoError(t, err)

	chart := filepath.Join(root, "charts", "app")
	for _, name := range []string{"blue", "green"} {
		app := appByName(t, apps, FrameworkFlux, name)
		assert.Equal(t, chart, app.RootPath)
		assert.Equal(t, map[string]any{"instance": name}, app.InlineValues)
	}
	for _, a := range apps {
		if a.RootPath == chart {
			assert.NotEqual(t, FrameworkPlain, a.Framework, "plain chart must yield to the HelmReleases")
		}
	}
}

func fluxHelmReleaseChartDoc(chart string) []byte {
	return []byte(`apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: rel
  namespace: apps
spec:
  chart:
    spec:
      chart: ` + chart + `
      sourceRef:
        kind: GitRepository
        name: flux-system
        namespace: flux-system
`)
}

func TestParseFluxKustomization_PathContainment(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "ks.yaml")

	_, err := parseFluxKustomization([]byte(fluxKustomizationDoc("apps", "../../outside", "GitRepository", "flux-system")), newRepoRoot(root), src, fluxResolver{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `Flux Kustomization "apps"`)
	assert.Contains(t, err.Error(), `spec.path "../../outside"`)

	for _, p := range []string{`"."`, `""`, "./apps"} {
		app, err := parseFluxKustomization([]byte(fluxKustomizationDoc("apps", p, "GitRepository", "flux-system")), newRepoRoot(root), src, fluxResolver{})
		require.NoError(t, err, p)
		require.NotNil(t, app, p)
	}
}

func TestParseFluxHelmRelease_ChartContainment(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "rel.yaml")

	_, err := parseFluxHelmRelease(fluxHelmReleaseChartDoc("../outside"), newRepoRoot(root), src, fluxResolver{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `Flux HelmRelease "rel"`)
	assert.Contains(t, err.Error(), `chart "../outside"`)

	app, err := parseFluxHelmRelease(fluxHelmReleaseChartDoc("./charts/app"), newRepoRoot(root), src, fluxResolver{})
	require.NoError(t, err)
	require.NotNil(t, app)
	assert.Equal(t, filepath.Join(root, "charts", "app"), app.RootPath)
}
