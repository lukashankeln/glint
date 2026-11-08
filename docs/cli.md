# CLI reference

```
glint lint [path...] [flags]
  --config string        config file (default: glint.yaml)
  --format string        text | json | sarif | junit | github-actions
  --fail-on string       comma-separated severities (default: from config)
  --output string        write output to file
  --output-sarif string  write SARIF report to file
  --output-junit string  write JUnit report to file
  --output-json string   write JSON summary to file (for CI step capture)
  --only-rules string    report only findings from these rule IDs
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

`glint rules list` prints one row per rule. SOURCE can be `built-in:<category>` for [built-in category rules](rule-catalogs.md) such as the CIS rules. Disabled rules, including CIS rules that are not enabled (the default) or are set to `enabled: false` under `rules.built_in`, are listed with `ENABLED` `no`.

`--only-rules` and `--skip-rules` filter the reported findings after all rules are evaluated; they do not change which rules run.

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
| `junit` | JUnit XML for CI test reports |

The `text` and `github-actions` formats escape non-printable and bidi characters in severities, rule IDs, messages, file paths and resource names; `json`, `sarif` and `junit` carry the raw values (JSON-/XML-encoded).

`glint discover` (text output) and `glint render` (the stdout `# --- app:` header and error messages) escape the same characters in app names, render targets and sources; `discover --format json`, the rendered manifests and the `--output` file names keep the raw values.
