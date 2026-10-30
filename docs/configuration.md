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
    timeout: "120s"                # Per-app render timeout (Go duration, e.g. "120s", "2m"; "" disables)
```

The helm timeout applies to each render pass of an app, including the
`discover_api_versions` re-render. When it expires, glint fails that app's
render with a "timed out" error, which `glint lint` reports as a `glint/render-error` violation.

glint renders offline, so `.Capabilities.APIVersions` only contains the built-in Kubernetes APIs. Charts that gate resources on a CRD (e.g. `monitoring.coreos.com/v1` for ServiceMonitors) either fail or silently omit them. With `discover_api_versions`, glint collects the API versions of every CustomResourceDefinition in the rendered output (from CRD charts, `crds/` directories with `include_crds`, or committed CRD manifests) and renders Helm charts a second time with them. `api_versions` covers CRDs that are installed outside the scanned repository.

### Kustomize

```yaml
render:
  kustomize:
    enable_helm: false             # Allow HelmChartInflationGenerator in overlays
    load_restrictor: "rootOnly"    # rootOnly | none
    timeout: "60s"                 # Per-app render timeout (Go duration; "" disables)
```

When the kustomize timeout expires, glint fails that app's render with a
"timed out" error, which `glint lint` reports as a `glint/render-error` violation.

`load_restrictor` controls which files an overlay may load:

- `rootOnly` (default, same as `kustomize build`): files such as patches,
  generator sources and resource files must be in or below the directory of
  the kustomization that references them. Directory references such as
  `resources: [../../base]` still work, because each referenced kustomization
  directory is its own root. A file outside the root fails the render with
  kustomize's `security; file ... is not in or below ...` error, reported as
  `glint/render-error`.
- `none`: overlays may load any file on disk.

Older glint versions ignored this setting and always behaved as `none`. If
your overlays load files from outside their own directory, set
`load_restrictor: "none"` to keep the old behaviour. Any other value is
rejected when the config is loaded.

`enable_helm` lets overlays use `helmCharts:` (kustomize's
HelmChartInflationGenerator). It needs a `helm` binary on `PATH` and applies
to both the built-in renderer and `subprocess_fallback`
(`kustomize build --enable-helm`).

> **Only enable `enable_helm` for repositories you trust.** It has the same
> trust model as `kustomize build --enable-helm`:
>
> - An overlay can point `helmGlobals.configHome` at a directory it ships,
>   which helm uses as its config and data home, so the overlay can make helm
>   run plugins it contains, i.e. execute arbitrary code.
> - `helmGlobals.chartHome` is not subject to `load_restrictor`, so an
>   overlay can render charts from outside its root.
> - Charts with a `repo:` are fetched with `helm pull` over the network
>   (glint otherwise renders offline) and unpacked into the chart home.
> - The `helm` process that kustomize starts is not covered by glint's
>   render timeout or output size limit; on timeout glint stops waiting but
>   the process may keep running.

### Fallback

```yaml
render:
  subprocess_fallback: false       # Fall back to helm/kustomize binary on SDK error
```

With `subprocess_fallback`, when the timeout expires the helm/kustomize process is killed and its output pipes are closed after a short grace period, so a leftover child process cannot hold the render open. A child process that still holds the output open after helm/kustomize has exited fails the render.

### Limits

These limits are built in and not configurable.

An app whose render fails (the stream, document-count and subprocess stdout limits below, YAML decode errors, helm/kustomize errors, timeouts) is reported by `glint lint` as an error-severity `glint/render-error` violation, so with the default `fail_on` the run fails instead of passing with the app unlinted. `glint render` still writes the other apps and exits 2. With `--output`, an app whose file cannot be fully written also makes it exit 2, and the partial file is removed. Unreachable remote charts and other network failures fail the app's render the same way; use `--skip-rules glint/render-error` to opt out deliberately. `--only-rules` also drops `glint/render-error` unless it is listed, so include it when restricting the rules a CI run checks.

- Each YAML stream (one raw manifest file, or one helm/kustomize render) is limited to 64 MiB and 10,000 documents. Exceeding either is an error for that app.
- Discovery reads each `*.yaml`/`*.yml` file up to 64 MiB and skips larger files with a warning.
- Non-regular files (devices, FIFOs, and symlinks to them) are skipped by discovery and are an error for raw manifest and Helm values file reads, for a local chart's `Chart.yaml` and `.helmignore`, and for files the in-process kustomize render reads (kustomization files, resources, patches, generator sources). A Helm values file path that is a directory is also an error.
- Helm values files, `Chart.yaml`, `.helmignore` and the files the in-process kustomize render reads share the 64 MiB limit; exceeding it fails that app. A non-regular or oversized kustomization file is reported as not found rather than as a limit error. These file checks do not cover `render.subprocess_fallback`: the kustomize binary reads files itself and only its stdout is capped.
- helm/kustomize subprocess stdout is capped at the same 64 MiB; the render fails with a limit error. Subprocess stderr shown in errors is truncated at 64 KiB.
- YAML anchors/aliases: alias expansion within a stream shares a memory budget of `max(1 MiB, 10 × the stream size)`, capped at 64 MiB. A document whose aliases exceed it, or that trips the excessive-aliasing heuristic, is skipped (logged at debug level) from both render output and linting, rather than failing the app.
- Nesting: a document's nesting depth shares a budget of `max(1 MiB, 10 × the stream size)`, capped at 256 MiB, per stream. It approximates the indentation `glint render` writes (for YAML; JSON output is about twice that), including the repeated indentation of multi-line and long strings in deeply nested documents. A document over it is skipped (logged at debug level) from both render output and linting, like the alias limits.
- Merge keys: a quoted or block `"<<"` mapping key whose `!` tag cannot be resolved from the source is an error for that app, rather than being guessed: `glint lint` reports it as an error-severity violation like other render failures, but with the more specific `glint/parse-error` ID, so `--skip-rules glint/render-error` does not silence it.

Caveats: limits apply per stream, not per app or per run. Kustomize file reads are capped per file, but the combined size of many files under the limit is not. They bound memory, but plain YAML can still use tens of times its size in memory. Other memory used inside the in-process helm/kustomize libraries is not capped.

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

`color` applies to glint's log output on stderr (the text report itself is
not colored). `auto` colors only when stderr is a terminal and `NO_COLOR` is
not set, `always` colors regardless of terminal and `NO_COLOR`, and `never`
disables color. The `--no-color` flag overrides any `color` setting. Any other
value is rejected when the config is loaded.

`summary` only controls the trailing `N errors, N warnings across N resources`
line of `text` output; the JSON summary (`--format json`, `--output-json`) is
always written.

A report file (`--output`, `--output-sarif`, `--output-junit`, `--output-json`) that cannot be fully written or closed makes `glint lint` exit `2` regardless of `fail_on`, and a partially written regular file is removed.

---

## `fail_on`

```yaml
fail_on:
  - error       # exit 1 when any error-severity violations are found
  # - warning   # uncomment to also fail on warnings
```

Pass `--fail-on ""` (empty string) to suppress exit code 1 entirely — useful for SARIF generation steps that should never block the pipeline. A SARIF or other report file that cannot be written still exits `2`.

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
