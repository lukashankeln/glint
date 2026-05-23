# glint

**GitOps linter for ArgoCD and Flux repositories.**

glint auto-renders Helm charts and Kustomize overlays and enforces custom policies via [CEL](https://cel.dev) expressions — all in a single command, no external binaries required.

```
$ glint lint .

[ERROR] Deployment/prod/api (apps/v1): one or more containers use the :latest image tag  [no-latest-tag]
[ERROR] Ingress/prod/api (networking.k8s.io/v1beta1): apiVersion is deprecated or removed  [deprecated-apis]
[WARNING] Deployment/prod/api (apps/v1): one or more containers are missing resource requests  [resource-requests]

2 errors, 1 warning across 47 resources
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

**Go (recommended):**
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
      security-events: write   # for SARIF upload to Security tab

    steps:
      - uses: actions/checkout@v4
      - uses: lukashankeln/glint@v0.1.2
        with:
          fail-on: "error"
          upload-sarif: "true"
```

**2. That's it.** glint will:
- Post inline PR annotations for every violation
- Upload SARIF results to the Security tab for persistent tracking
- Fail the workflow on `error`-severity violations

**Action inputs:**

| Input | Default | Description |
|-------|---------|-------------|
| `version` | `latest` | glint version tag to install |
| `go-version` | `1.25` | Go toolchain version |
| `config` | `glint.yaml` | Path to config file |
| `format` | `github-actions` | Output format |
| `fail-on` | `error` | Severities that fail the step |
| `upload-sarif` | `true` | Upload to GitHub Security tab |
| `sarif-file` | `glint-results.sarif` | SARIF output path |
| `paths` | `.` | Space-separated paths to lint |

**Action outputs:** `violations` (total count), `errors` (error count)

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

## CLI reference

```
glint lint [path...] [flags]
  --config string        config file (default: glint.yaml)
  --format string        text | json | sarif | github-actions
  --fail-on string       comma-separated severities (default: from config)
  --output string        write output to file
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
