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

## CI integration

Run glint as a GitHub Action (inline PR annotations, SARIF upload) or as a pre-commit hook:

```yaml
- uses: lukashankeln/glint@v0.1.16
  with:
    fail-on: "error"
```

See [`docs/ci.md`](docs/ci.md) for the full workflow, action inputs and pre-commit setup.

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
| `matchesGlob(str, pattern)` | `matchesGlob(name, "legacy-*")`, `matchesGlob(ns, "prod-*")` |
| `semverLT(a, b)` | `semverLT(imageTag(c.image), "2.0.0")` |

See [`docs/writing-rules.md`](docs/writing-rules.md) for a full guide with examples.

---

## CIS rules

The CIS rules are built-in rules that are off by default. Enable the CIS Kubernetes Benchmark v1.10 (section 5) rules with:

```yaml
rules:
  catalogs:
    cis:
      enabled: true
```

See [`docs/rule-catalogs.md`](docs/rule-catalogs.md) for the rule list, per-rule overrides and limitations.

---

## Plugins

Plugins let any external tool participate in the lint pipeline and surface findings as native glint violations:

```yaml
plugins:
  - name:    my-validator
    command: ./scripts/validate.sh
```

See [`docs/plugins.md`](docs/plugins.md) for config fields, the kubeconform adapter and the output protocol.

---

## CLI reference

Commands: `glint lint`, `glint discover`, `glint render`, `glint rules list`, `glint rules validate`, `glint init`, `glint version`. Exit codes: `0` = clean, `1` = violations found, `2` = pipeline error. Output formats: `text`, `json`, `github-actions`, `sarif`, `junit`.

See [`docs/cli.md`](docs/cli.md) for all flags and output formats.

---

## Documentation

See [`docs/README.md`](docs/README.md) for the index.

- [Configuration](docs/configuration.md)
- [Writing custom rules](docs/writing-rules.md)
- [Built-in CIS rules](docs/rule-catalogs.md)
- [Plugins](docs/plugins.md)
- [CLI reference and output formats](docs/cli.md)
- [GitHub Actions and pre-commit](docs/ci.md)

---

## License

[MIT](LICENSE)
