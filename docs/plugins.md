# Plugins

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

Plugin stdout is capped at 32 MiB (the run fails with an output-limit error) and stderr shown in errors is truncated at 64 KiB; see [Limits](configuration.md#limits).

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
