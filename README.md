# glint

**GitOps linter for ArgoCD and Flux repositories.**

glint auto-renders Helm charts and Kustomize overlays and enforces custom policies via [CEL](https://cel.dev) expressions — all in a single command, no external binaries required.

```
$ glint lint .

[ERROR] cel  Deployment/prod/api (apps/v1): One or more containers use the :latest image tag  [no-latest-tag]
[ERROR] cel  Ingress/prod/api (networking.k8s.io/v1beta1): apiVersion networking.k8s.io/v1beta1 is deprecated or removed  [deprecated-apis]
[WARNING] cel  Deployment/prod/api (apps/v1): One or more containers are missing resource requests  [resource-requests]

2 errors, 1 warnings across 47 resources
```

---

## Why glint?

A typical GitOps repo needs several tools chained together:

```bash
helm template . | kube-linter lint -
pluto detect-files -d .
# ... plus custom scripts for org-specific policies
```

glint replaces all of that with a single step that understands your repository structure — ArgoCD Applications, Flux HelmReleases, plain YAML directories — and handles rendering and policy enforcement in one pass.

---

## Install

**Homebrew (recommended):**
```bash
brew install lukashankeln/tap/glint
```

**Go:**
```bash
go install github.com/lukashankeln/glint/cmd/glint@latest
```

**From source:**
```bash
git clone https://github.com/lukashankeln/glint
cd glint
go build -o glint ./cmd/glint
```

---

## Quick start

```bash
# 1. Scaffold a config in your GitOps repo
glint init

# 2. Run the linter
glint lint .

# 3. Preview what glint discovers without linting
glint discover .

# 4. Render manifests to inspect what glint sees
glint render .

# 5. List all active policy rules
glint rules list
```

---

## GitHub Actions

Add to any GitOps repository in two steps.

**1. Create `.github/workflows/glint.yml`:**

```yaml
name: GitOps Lint

on:
  push:
    branches: [main]
  pull_request:

jobs:
  lint:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      security-events: write  # for SARIF upload to Security tab
      actions: read           # required by codeql-action/upload-sarif

    steps:
      - uses: actions/checkout@v4
      - uses: lukashankeln/glint@v0.1.16
        with:
          fail-on: "error"
      - uses: github/codeql-action/upload-sarif@v3
        if: always()
        with:
          sarif_file: glint-results.sarif
```

**2. That's it.** glint will:
- Post inline PR annotations for every violation
- Write SARIF results to `glint-results.sarif` for upload to the Security tab
- Fail the workflow on `error`-severity violations

**Action inputs:**

| Input | Default | Description |
|-------|---------|-------------|
| `config` | `glint.yaml` | Path to config file |
| `format` | `github-actions` | Output format |
| `fail-on` | `error` | Severities that fail the step |
| `sarif-file` | `glint-results.sarif` | SARIF output path |
| `paths` | `.` | Space-separated paths to lint |

---

## pre-commit

Add to `.pre-commit-config.yaml`:

```yaml
repos:
  - repo: https://github.com/lukashankeln/glint
    rev: v0.2.9
    hooks:
      - id: glint         # builds from source, requires Go
      # - id: glint-docker  # uses the published container image, requires Docker
```

The hook lints the whole repository whenever a staged `.yaml`, `.yml`, `.json` or `.tpl` file changes. Pass extra flags via `args`, e.g. `args: [--fail-on, "error,warning"]`.

---

## Configuration

glint reads `glint.yaml` from the working directory. Run `glint init` to generate a starter config.

```yaml
# glint.yaml
version: "v1alpha1"

discovery:
  paths: ["."]
  exclude:
    - "vendor/**"
    - "**/.git/**"

render:
  helm:
    kubernetes_version: "1.36.0"
    include_crds: true

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


fail_on: [error]
```

See [`docs/configuration.md`](docs/configuration.md) for the full reference.

---

## Writing custom rules

Rules are CEL expressions that return `true` (compliant) or `false` (violation):

```yaml
rules:
  custom:
    - id: no-privileged-containers
      description: "Containers must not run privileged"
      severity: error
      match:
        kinds: [Deployment, StatefulSet, DaemonSet]
      expression: |
        resource.spec.template.spec.containers.all(c,
          !has(c.securityContext) ||
          !has(c.securityContext.privileged) ||
          c.securityContext.privileged == false
        )
      message: "{{ .kind }}/{{ .name }} has a privileged container"
```

glint provides extra functions on top of standard CEL:

| Function | Example |
|----------|---------|
| `imageTag(image)` | `imageTag(c.image) != "latest"` |
| `inList(value, list)` | `inList(kind, ["Deployment", "StatefulSet"])` |
| `matchesGlob(str, pattern)` | `matchesGlob(namespace, "prod-*")` |
| `semverLT(a, b)` | `semverLT(imageTag(c.image), "2.0.0")` |

See [`docs/writing-rules.md`](docs/writing-rules.md) for a full guide with examples.

---

## Plugins

Plugins let any external tool participate in the lint pipeline and surface findings as native glint violations. They run after rendering, receive each app's manifests as YAML on stdin, and write violations as JSON to stdout.

```yaml
# glint.yaml
plugins:
  - name:    my-validator
    command: ./scripts/validate.sh
    args:    ["--strict"]
```

Plugin violations feed into the same output formatters, `fail_on` logic, and `--skip-rules`/`--only-rules` filters as CEL rules. The `source` field on each violation is set to the plugin name.

**Plugin config fields:**

| Field | Default | Description |
|-------|---------|-------------|
| `name` | required | Unique identifier for this plugin |
| `command` | required | Executable to run |
| `args` | `[]` | Arguments passed before manifest input |
| `adapter` | — | Built-in adapter name (e.g. `kubeconform`) for third-party tools |
| `severity` | — | Override severity for all violations from this plugin |
| `input` | `stdin` | How manifests are passed: `stdin` or `file` (path appended to args) |
| `timeout` | `30s` | Kill the plugin after this duration (e.g. `"60s"`, `"2m"`). The plugin's output pipes are closed after a short grace period so a leftover child process cannot hold the run open; a child still holding output open after the plugin exits fails `glint lint` |
| `allow_nonzero_exit` | `false` | Suppress the eval-error violation when the plugin exits non-zero (for tools that exit 1 on findings) |
| `env` | `{}` | Extra environment variables |

**kubeconform** — validate manifests against Kubernetes JSON schemas:

```yaml
plugins:
  - name:    kubeconform
    command: kubeconform
    args:    ["-strict", "-output", "json", "-"]
    adapter: kubeconform
    severity: error
```

Install kubeconform: `brew install kubeconform` or see [github.com/yannh/kubeconform](https://github.com/yannh/kubeconform).

**Native output protocol** — plugins without an adapter must write a JSON array to stdout:

```json
[
  {
    "rule_id":   "my-linter/check-name",
    "severity":  "error",
    "message":   "hostNetwork must not be true",
    "file_path": "apps/api/deployment.yaml",
    "resource": {
      "kind":      "Deployment",
      "name":      "api",
      "namespace": "production"
    }
  }
]
```

All fields except `rule_id`, `severity`, and `message` are optional.

---

## CLI reference

```
glint lint [path...] [flags]
  --config string        config file (default: glint.yaml)
  --format string        text | json | sarif | junit | github-actions
  --fail-on string       comma-separated severities (default: from config)
  --output string        write output to file
  --output-sarif string  write SARIF report to file
  --output-junit string  write JUnit report to file
  --output-json string   write JSON summary to file (for CI step capture)
  --only-rules string    run only these rule IDs
  --skip-rules string    skip these rule IDs

glint discover [path...]
  -f, --format string    text | json

glint render [path...]
  -f, --format string    yaml | json
  -o, --output string    directory to write per-app files (default: stdout)

glint rules list
glint rules validate [file...]

glint init [path]
  --force                overwrite existing glint.yaml
  --framework string     argocd | flux | plain (default: auto-detect)
```

**Exit codes:** `0` = clean, `1` = violations found, `2` = pipeline error

An app that fails to render is a `glint/render-error` violation in `glint lint`, and `glint render` exits `2` if any app fails. With `--output`, an app whose file cannot be fully written also makes `glint render` exit `2`, and a partially written regular file is removed.

In `glint lint`, a report file (`--output`, `--output-sarif`, `--output-junit`, `--output-json`) that cannot be fully written or closed makes the run exit `2` regardless of `fail_on`, and a partially written regular file is removed.

---

## Output formats

| Format | Use case |
|--------|----------|
| `text` | Human-readable terminal output (default) |
| `json` | Machine-readable, stable schema |
| `github-actions` | GitHub workflow annotations on PRs (auto-selected when `GITHUB_ACTIONS=true`) |
| `sarif` | GitHub Security tab / Code Scanning |

---

## License

[MIT](LICENSE)
