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
| `schema` | object | — | Schema validation settings |
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
```

glint auto-detects the renderer for each app:
- **Helm** — directory contains `Chart.yaml`, or ArgoCD Application points to a Helm chart
- **Kustomize** — directory contains `kustomization.yaml`, or Flux Kustomization resource
- **Raw** — plain YAML files, no templating

---

## `render`

### Helm

```yaml
render:
  helm:
    kubernetes_version: "1.36.0"   # Sets .Capabilities.KubeVersion
    include_crds: true             # Include CRD manifests in output
    api_versions: []               # Extra entries for .Capabilities.APIVersions
    timeout: "120s"                # Per-chart render timeout
```

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
      enabled: false      # disabled by default
      severity: warning

    deprecated_apis:
      enabled: true
      severity: error
```

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

Each file contains a list of rule definitions using the same schema as `custom` above.

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
