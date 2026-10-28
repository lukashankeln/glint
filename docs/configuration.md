# Configuration reference

glint looks for its config file in this order:

1. Path given via `--config` flag
2. `glint.yaml` in the working directory
3. `.glint.yaml` in the working directory
4. `.glint/config.yaml` in the working directory

If no config file is found, glint runs with built-in defaults. Run `glint init` to generate a starter file.

---

## Top-level fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `version` | string | `v1alpha1` | Config schema version |
| `discovery` | object | — | Controls what glint scans |
| `render` | object | — | Renderer settings |
| `rules` | object | — | Policy rule configuration |
| `output` | object | — | Output formatting |
| `fail_on` | []string | `[error]` | Severities that produce exit code 1 |

---

## `discovery`

```yaml
discovery:
  paths: ["."]           # Directories to scan (default: ["."])
  exclude:               # Glob patterns to skip
    - "vendor/**"
    - "**/.git/**"
    - "**/.terraform/**"

  # Override renderer or Helm settings for a specific subtree:
  overrides:
    - path: "apps/legacy"
      renderer: helm
      helm:
        release_name: legacy-app
        namespace: production
        values_files:
          - values.yaml
          - values-prod.yaml
        set:
          image.tag: "1.2.3"

  # Additional URLs of this repository. ArgoCD Applications and Flux
  # GitRepositories whose URL matches the repository's git remotes (read from
  # .git/config) or one of these aliases are rendered from the local working
  # tree instead of being skipped as remote.
  repo_aliases:
    - "https://git.example.com/org/repo"
```

glint auto-detects the renderer for each app:
- **Helm** — directory contains `Chart.yaml`, or ArgoCD Application / Flux HelmRelease points to a Helm chart
- **Kustomize** — directory contains `kustomization.yaml`, or Flux Kustomization whose path contains one
- **Raw** — plain YAML files, no templating. ArgoCD `directory.recurse: true` and Flux Kustomizations whose path has no `kustomization.yaml` include subdirectories.

When an ArgoCD Application or Flux resource renders a directory that would also be discovered on its own, the GitOps resource takes precedence, so its values, namespace and source are used. Several GitOps resources rendering the same directory are all kept.

Flux sourceRefs are resolved against the source objects found in the scanned paths:
- A GitRepository is treated as this repository when its URL matches, when it is `flux-system/flux-system` (created by `flux bootstrap`), or when it is not defined in the scanned paths.
- Resources whose source is a different GitRepository, an OCIRepository (for Kustomizations) or a Bucket are skipped and logged at info level.
- HelmRelease `spec.chartRef` supports OCIRepository and HelmChart.

---

## `render`

### Helm

```yaml
render:
  helm:
    kubernetes_version: "1.36.0"   # Sets .Capabilities.KubeVersion
    include_crds: true             # Include CRD manifests in output
    api_versions: []               # Extra entries for .Capabilities.APIVersions
    discover_api_versions: true    # Add API versions of CRDs found while rendering
    timeout: "120s"                # Per-chart render timeout
```

glint renders offline, so `.Capabilities.APIVersions` only contains the built-in Kubernetes APIs. Charts that gate resources on a CRD (e.g. `monitoring.coreos.com/v1` for ServiceMonitors) either fail or silently omit them. With `discover_api_versions`, glint collects the API versions of every CustomResourceDefinition in the rendered output (from CRD charts, `crds/` directories with `include_crds`, or committed CRD manifests) and renders Helm charts a second time with them. `api_versions` covers CRDs that are installed outside the scanned repository.

### Kustomize

```yaml
render:
  kustomize:
    enable_helm: false             # Allow HelmChartInflationGenerator in overlays
    load_restrictor: "rootOnly"    # rootOnly | none
    timeout: "60s"
```

### Fallback

```yaml
render:
  subprocess_fallback: false       # Fall back to helm/kustomize binary on SDK error
```

### Limits

These limits are built in and not configurable.

- Each YAML stream (one raw manifest file, or one helm/kustomize render) is limited to 64 MiB and 10,000 documents. Exceeding either is an error for that app.
- helm/kustomize subprocess stdout is capped at the same 64 MiB; the render fails with a limit error. Subprocess stderr shown in errors is truncated at 64 KiB.
- YAML anchors/aliases: alias expansion within a stream shares a memory budget of `max(1 MiB, 10 × the stream size)`, capped at 64 MiB. A document whose aliases exceed it, or that trips the excessive-aliasing heuristic, is skipped (logged at debug level) rather than failing the app.

Caveats: limits apply per stream, not per app or per run. They bound memory, but plain YAML can still use tens of times its size in memory. Memory used inside the in-process helm/kustomize libraries is not capped.

---

## `rules`

### Built-in rules

```yaml
rules:
  built_in:
    no_latest_tag:
      enabled: true
      severity: error     # error | warning | info

    resource_requests:
      enabled: true       # enabled by default
      severity: warning
      params:
        init_containers: true   # false = only regular containers need requests

    deprecated_apis:
      enabled: true
      severity: error
```

Severity values are case-insensitive. Unknown values are rejected when loading config or rule files, and `glint rules validate` exits with an error.

### Custom inline rules

```yaml
rules:
  custom:
    - id: min-replicas
      description: "Deployments in production must run at least 2 replicas"
      severity: error
      match:
        kinds: [Deployment]
        namespaces: ["prod-*"]
      expression: "resource.spec.replicas >= 2"
      message: "{{ .kind }}/{{ .name }} must have replicas >= 2"
```

See [`docs/writing-rules.md`](writing-rules.md) for the full rule schema and CEL guide.

### Rule files

Load rules from external YAML files (useful for sharing policies across repos):

```yaml
rules:
  rule_files:
    - "policies/*.yaml"
    - "../shared-policies/security.yaml"
```

Each file contains a `rules:` list of rule definitions (or a single rule) using the same schema as `custom` above. A rule file that fails to parse is reported as an error rather than skipped.

### Exceptions

Skip a rule for specific resources without disabling it globally:

```yaml
rules:
  exceptions:
    - rule: no-latest-tag
      resources:
        - kind: Deployment
          name: "legacy-*"
          namespace: staging
          reason: "Legacy service, cleanup tracked in JIRA-1234"
```

| Field | Type | Description |
|-------|------|-------------|
| `rule` | string | Rule ID to suppress (required) |
| `resources[].kind` | string | Exact kind match; empty = any |
| `resources[].name` | string | Glob pattern; empty = any |
| `resources[].namespace` | string | Glob pattern; empty = any |
| `resources[].reason` | string | Documentation only |

---

## `output`

```yaml
output:
  format: text           # text | json | sarif | github-actions
  color: auto            # auto | always | never
  output_file: ""        # Write output here instead of stdout
  summary: true          # Print summary line at end of text output
```

CLI flags `--format` and `--output` override these values per-run.

---

## `fail_on`

```yaml
fail_on:
  - error       # exit 1 when any error-severity violations are found
  # - warning   # uncomment to also fail on warnings
```

Pass `--fail-on ""` (empty string) to suppress exit code 1 entirely — useful for SARIF generation steps that should never block the pipeline.

---

## Full example

```yaml
version: "v1alpha1"

discovery:
  paths: ["apps/", "clusters/"]
  exclude:
    - "vendor/**"
    - "**/.git/**"
    - "**/node_modules/**"

render:
  helm:
    kubernetes_version: "1.36.0"
    include_crds: true
  kustomize:
    enable_helm: false
  subprocess_fallback: false

rules:
  built_in:
    no_latest_tag:
      enabled: true
      severity: error
    resource_requests:
      enabled: true
      severity: warning
    deprecated_apis:
      enabled: true
      severity: error

  custom:
    - id: required-labels
      description: "All workloads must carry standard labels"
      severity: warning
      match:
        kinds: [Deployment, StatefulSet, DaemonSet]
      expression: |
        "app.kubernetes.io/name" in labels &&
        "app.kubernetes.io/version" in labels
      message: "{{ .kind }}/{{ .name }} is missing required labels"

  rule_files:
    - "policies/security.yaml"

  exceptions:
    - rule: resource-requests
      resources:
        - kind: Job
          reason: "Batch jobs are excluded from resource request policy"

output:
  format: text
  color: auto
  summary: true

fail_on:
  - error
```
