# CI integration

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
