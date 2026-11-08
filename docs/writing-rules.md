# Writing custom rules

glint policies are [CEL](https://cel.dev) expressions evaluated against each Kubernetes manifest. A rule returns `true` to indicate the resource is **compliant**; anything else produces a violation (except an evaluation error with `on_eval_error: warning`, which is skipped with only a log warning and produces no violation).

This guide covers where rules live, the rule file format, the field reference, how a rule is evaluated, the variables and functions available to expressions, message templates, worked examples and a testing workflow.

- [Where rules live](#where-rules-live)
- [Rule schema](#rule-schema)
- [Rule file format](#rule-file-format)
- [How a rule is evaluated](#how-a-rule-is-evaluated)
- [Evaluation errors and `on_eval_error`](#evaluation-errors-and-on_eval_error)
- [CEL variables](#cel-variables)
- [Custom functions](#custom-functions)
- [Match filters](#match-filters)
- [Message templates](#message-templates)
- [Examples](#examples)
- [Testing workflow](#testing-workflow)
- [Tips](#tips)

---

## Where rules live

A rule can be defined inline in `glint.yaml` or in a separate rule file. Both use the same fields.

**Inline** (`rules.custom` in `glint.yaml`):

```yaml
# glint.yaml
rules:
  custom:
    - id: prod-min-replicas
      description: "Production Deployments must run at least 2 replicas"
      severity: error
      match:
        kinds: [Deployment]
        namespaces: ["prod-*"]
      expression: "has(resource.spec.replicas) && resource.spec.replicas >= 2"
      message: "{{ .kind }}/{{ .name }} must have replicas >= 2 in production"
```

**Separate file** (referenced through `rules.rule_files`):

```yaml
# glint.yaml
rules:
  rule_files:
    - "policies/*.yaml"
```

```yaml
# policies/replicas.yaml
rules:
  - id: prod-min-replicas
    description: "Production Deployments must run at least 2 replicas"
    severity: error
    match:
      kinds: [Deployment]
      namespaces: ["prod-*"]
    expression: "has(resource.spec.replicas) && resource.spec.replicas >= 2"
    message: "{{ .kind }}/{{ .name }} must have replicas >= 2 in production"
```

| | Inline (`rules.custom`) | Separate file (`rules.rule_files`) |
|---|---|---|
| Defined in | `glint.yaml` | any number of YAML files matched by glob patterns |
| Shown as source in `glint rules list` | `glint.yaml` | the file path |
| `severity` validated | at config load | when the file is loaded |
| `on_eval_error` validated | at config load | not validated (see [below](#evaluation-errors-and-on_eval_error)) |
| Checked with `glint rules validate` | no (`glint rules validate glint.yaml` fails with a parse error, because `rules` there is a mapping, not a list) | yes |
| Rule without an `id` | not skipped | skipped silently |

Which to choose:

- **Inline** suits a handful of short, repo-specific rules. Everything stays in one reviewed file.
- **Separate files** suit sharing policies across repositories (check them out or vendor them and point `rule_files` at them), giving a platform or security team review ownership of the policy files (for example through `CODEOWNERS`) separately from the application config, and keeping `glint.yaml` small once rules grow past a few dozen lines. They are also the only form you can pre-check with `glint rules validate`.

Built-in rules are always loaded as well; see [`configuration.md`](configuration.md) to tune or disable them.

---

## Rule schema

```yaml
id: my-rule                # unique identifier (required)
description: "..."         # human-readable explanation
severity: error            # error | warning | info
on_eval_error: error       # error | warning
match:                     # pre-filter (optional)
  kinds: [Deployment]
  api_groups: [apps]
  namespaces: ["prod-*"]
  exclude_namespaces: ["test-*"]
  labels:
    team: platform
expression: |              # CEL expression
  has(resource.spec.replicas) && resource.spec.replicas >= 2
message: "{{ .kind }}/{{ .name }} requires replicas >= 2"
```

(Inline rules sit under `rules.custom` as list items, and a rule file holds them under `rules:`; see [Rule file format](#rule-file-format).)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `id` | string | none | Rule identifier, used in output, `--only-rules` / `--skip-rules` and `rules.exceptions`. Rule file entries without an `id` are skipped. Uniqueness is not enforced by the loader; exceptions are keyed by ID, so reusing an ID shares its exceptions. |
| `description` | string | empty | Shown to humans; not used in evaluation. |
| `severity` | string | `error` | `error`, `warning` or `info`. Case-insensitive. Unknown values are rejected when loading config or rule files, and `glint rules validate` exits with an error. |
| `match` | object | match everything | Pre-filter; see [Match filters](#match-filters). |
| `expression` | string | none | CEL expression; `true` means compliant. A rule with an empty expression is skipped entirely: it is dropped before compilation, so it appears in neither `glint lint` nor `glint rules list`. |
| `message` | string | empty | Go `text/template` shown with each violation; see [Message templates](#message-templates). |
| `on_eval_error` | string | `error` | What to do when the expression fails at runtime: `error` or `warning`. See [Evaluation errors](#evaluation-errors-and-on_eval_error). |

---

## Rule file format

A rule file is either a `rules:` list:

```yaml
rules:
  - id: no-nodeport
    description: "Services must not use NodePort type"
    severity: warning
    match:
      kinds: [Service]
    expression: |
      !has(resource.spec.type) || resource.spec.type != "NodePort"
    message: "Service/{{ .name }} uses NodePort"
  - id: no-loadbalancer
    description: "Services must not use LoadBalancer type"
    severity: warning
    match:
      kinds: [Service]
    expression: |
      !has(resource.spec.type) || resource.spec.type != "LoadBalancer"
    message: "Service/{{ .name }} uses LoadBalancer"
```

or a single top-level rule:

```yaml
id: no-nodeport
description: "Services must not use NodePort type"
severity: warning
match:
  kinds: [Service]
expression: |
  !has(resource.spec.type) || resource.spec.type != "NodePort"
message: "Service/{{ .name }} uses NodePort"
```

Behaviour of the loader (`internal/rules/loader.go`):

- Entries without an `id` are skipped without a warning. A single-rule file without an `id` loads zero rules.
- A file with a top-level `rules` key that fails to parse is an error; it is not silently treated as an empty or single-rule file.
- An unknown `severity` is an error naming the file and rule.
- `rule_files` entries are glob patterns (Go `filepath.Glob`: `*`, `?`, `[...]`; `**` is not recursive). A relative pattern resolves against the **working directory of the glint process**, not the directory of `glint.yaml`; nothing joins it with the config location. Run glint from the repository root, or use an absolute path, when the two differ.
- A pattern that matches no file is silently ignored, so a typo in a path loads no rules and reports nothing. Check with `glint rules list`. A malformed pattern (for example `[`) is different: it is an error (`invalid glob`).
- Duplicates are detected by the exact path string `filepath.Glob` returns, so a file matched by several patterns is loaded once only when the patterns yield the same string. `policies/p.yaml` and `./policies/p.yaml` load the file twice. Two different paths defining the same `id` are both loaded.
- `on_eval_error` in rule files is not validated. Any value other than `warning` behaves like the default `error`.

---

## How a rule is evaluated

For every manifest, each rule is processed in this order (`evaluateManifest` in `internal/rules/engine.go`):

1. A disabled rule is skipped. Built-in rules can be disabled in config; custom rules are always enabled.
2. The `match` filter is applied. If the manifest does not match, the rule is skipped.
3. `rules.exceptions` are applied. If the manifest matches an exception for this rule ID, the rule is skipped. See [Exceptions in `configuration.md`](configuration.md#exceptions).
4. The CEL expression is evaluated.
   - If evaluation fails, [`on_eval_error`](#evaluation-errors-and-on_eval_error) decides the outcome.
   - If the result is exactly `true`, the resource is compliant and nothing is reported.
   - Any other result is a violation. This includes `false` and also a non-boolean value (for example an expression that returns a string or an int): the engine only compares the result with `true`.
5. For a violation, the `message` template is rendered and the violation is recorded with the rule's `severity`.

Compile-time behaviour:

- Every expression is compiled once when the engine starts. A compile error fails the whole run with `rule "<id>": CEL compile error: ...`, even for rules that would never match a manifest.
- A rule whose expression is empty is skipped before compilation and never evaluated (and so is not shown by `glint rules list`).
- `glint lint --only-rules` and `--skip-rules` filter the reported violations after evaluation; they do not stop other rules from being compiled, so a broken expression in any rule still fails the run.

---

## Evaluation errors and `on_eval_error`

An evaluation error is a runtime failure of an expression that compiled fine. The typical cause is reading a field that does not exist on a particular manifest without checking `has()` first:

```yaml
id: no-run-as-root
description: "Pods must not run as root"
severity: error
match:
  kinds: [Deployment]
expression: |
  resource.spec.template.spec.securityContext.runAsNonRoot == true
message: "{{ .kind }}/{{ .name }} may run as root"
```

A Deployment without `spec.template.spec.securityContext` produces an evaluation error (`no such key`) rather than `false`. What happens next depends on `on_eval_error`:

| Value | Behaviour |
|-------|-----------|
| `error` (default) | A violation is reported with severity `error` and the message `CEL evaluation error: ...`, regardless of the rule's own `severity`. Your `message` template is not used. |
| `warning` | A warning is written to the log (`CEL evaluation error, skipping`, with the rule ID and resource) and the rule is skipped for that resource. No violation is reported. |

Prefer fixing the expression with `has()` over setting `on_eval_error: warning`, which can hide real problems:

```yaml
id: no-run-as-root
description: "Pods must not run as root"
severity: error
match:
  kinds: [Deployment]
expression: |
  has(resource.spec.template.spec.securityContext) &&
  has(resource.spec.template.spec.securityContext.runAsNonRoot) &&
  resource.spec.template.spec.securityContext.runAsNonRoot == true
message: "{{ .kind }}/{{ .name }} may run as root"
```

`on_eval_error: warning` is reasonable when a rule inspects fields whose shape you cannot control (for example custom resources) and a skipped resource is acceptable.

Inline rules reject any `on_eval_error` other than `error` or `warning` at config load. Rule files are not checked: the engine only tests for `warning`, so any other value behaves like `error`.

---

## CEL variables

Every expression has access to these variables:

| Variable | Type | Description |
|----------|------|-------------|
| `resource` | `map` | Full manifest (Kubernetes Unstructured) |
| `name` | `string` | `metadata.name` |
| `namespace` | `string` | `metadata.namespace` (empty for cluster-scoped resources and for namespaced manifests without an explicit `metadata.namespace`; glint does not default it). **Not usable in expressions:** `namespace` is a reserved word in CEL, so any expression that references it fails with `reserved identifier: namespace`. Use `match.namespaces` / `exclude_namespaces`, or read `resource.metadata.namespace` behind a `has(resource.metadata.namespace)` guard |
| `kind` | `string` | e.g. `"Deployment"` |
| `apiVersion` | `string` | e.g. `"apps/v1"` |
| `labels` | `map<string,string>` | `metadata.labels` |
| `annotations` | `map<string,string>` | `metadata.annotations` |

Because `namespace` is not defaulted, `match.namespaces` / `exclude_namespaces` filters also see that empty value. `labels` and `annotations` are always maps, empty when the manifest has none. Non-string label or annotation values are left out.

### `hasLabel` and `hasAnnotation`

Two helpers test for a non-empty label or annotation:

```yaml
id: has-owner
description: "Workloads must name an owner"
severity: warning
match:
  kinds: [Deployment, StatefulSet, DaemonSet]
expression: |
  hasLabel("owner") && hasAnnotation("contact")
message: "{{ .kind }}/{{ .name }} needs an owner label and a contact annotation"
```

These are not CEL functions. Before compilation, glint rewrites `hasLabel("key")` to `("key" in labels && labels["key"] != "")` and `hasAnnotation("key")` to `("key" in annotations && annotations["key"] != "")` (`preprocessExpression` in `internal/rules/loader.go`). Consequences:

- Only double-quoted string literals are supported. `hasLabel('owner')` or `hasLabel(someVariable)` is not rewritten and fails to compile.
- A label that is present but empty counts as missing, unlike `"owner" in labels`.
- The rewrite applies to inline rules and rule files alike.

---

## Custom functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `imageTag` | `imageTag(image string) string` | Extract tag from an image reference. Returns `"latest"` for untagged images, `""` for digest-pinned. |
| `inList` | `inList(value string, list list) bool` | True if value appears in list |
| `matchesGlob` | `matchesGlob(str string, pattern string) bool` | Doublestar glob match |
| `semverLT` | `semverLT(a string, b string) bool` | True if semver `a` is less than `b` |
| `podSpec` | `podSpec(resource) map` | Pod spec of a workload: `spec.jobTemplate.spec.template.spec` (CronJob), else `spec.template.spec` (Deployment etc.), else `spec` when it has a `containers` key (Pod), else `{}`. Null, absent or wrong-typed intermediate values fall through; it never errors. |
| `podContainers` | `podContainers(resource) list` | `containers`, `initContainers` and `ephemeralContainers` of `podSpec(resource)` concatenated in that order. Fields that are absent, null or not a list are skipped; list elements are returned as-is. |

Standard CEL built-ins (`has()`, `size()`, `.all()`, `.exists()`, `.filter()`, `string()`, `int()`, etc.) are all available.

---

## Match filters

The `match` block is a pre-CEL filter applied before evaluating the expression. All non-empty fields must match (AND logic).

```yaml
match:
  kinds: [Deployment, StatefulSet]      # exact kind names
  api_groups: [apps, batch]             # API group portion of apiVersion
  namespaces: ["prod-*", "staging"]     # glob whitelist (empty = any)
  exclude_namespaces: ["test-*"]        # glob blacklist
  labels:                               # required labels (AND)
    env: production
```

Use `match` to avoid writing kind checks in every expression; it is more efficient and makes rules easier to read. `match` also determines which pod-template shape your expression must handle; see [Pod template shapes](#pod-template-shapes).

---

## Message templates

The `message` field is a Go `text/template`. Available variables:

| Variable | Value |
|----------|-------|
| `{{ .kind }}` | Resource kind |
| `{{ .name }}` | Resource name |
| `{{ .namespace }}` | Resource namespace |
| `{{ .apiVersion }}` | API version |
| `{{ .severity }}` | Rule severity (`error`, `warning`, or `info`) |

These five keys are the only ones provided (`renderMessage` in `internal/rules/engine.go`). Behaviour worth knowing:

- A template that fails to parse (for example `{{ .kind `) is not an error. The raw `message` text is used as-is.
- A template that parses but fails to execute also falls back to the raw `message` text.
- An unknown key such as `{{ .labels }}` does not fail; Go templates render a missing map key as `<no value>`.
- An empty or missing `message` produces a violation with an empty message. glint does not generate a default, so the text output reads `[ERROR] cel  Kind/name (apiVersion):   [rule-id]`. Always set a message.
- The message is not used for [evaluation errors](#evaluation-errors-and-on_eval_error), which carry their own text.

---

## Examples

Each example is a complete rule file containing a single rule. Paste the rule under `rules.custom` in `glint.yaml` (as a list item) or save it to a file listed in `rule_files`.

### Pod template shapes

The path to the containers depends on the kind:

| Kind | Containers at |
|------|---------------|
| `Pod` | `resource.spec.containers` |
| `Deployment`, `StatefulSet`, `DaemonSet`, `ReplicaSet`, `Job` | `resource.spec.template.spec.containers` |
| `CronJob` | `resource.spec.jobTemplate.spec.template.spec.containers` |

An expression that reads `resource.spec.template...` fails with an evaluation error on a `Pod` or `CronJob`. Either restrict `match.kinds` to kinds that share the path you read (most examples below do), or handle the shapes with `has()`, as the built-in `no-latest-tag` rule does.

`podSpec` and `podContainers` hide these shapes, so one expression works for every workload kind, and also covers init and ephemeral containers. Both return empty results rather than erroring, but you still need `has()` for fields inside a container:

```yaml
id: no-host-network-or-privileged
description: "Workloads must not use the host network or run privileged containers"
severity: error
match:
  kinds: [Pod, Deployment, StatefulSet, DaemonSet, ReplicaSet, ReplicationController, Job, CronJob]
expression: |
  !(has(podSpec(resource).hostNetwork) && podSpec(resource).hostNetwork == true) &&
  podContainers(resource).all(c,
    !(has(c.securityContext) && has(c.securityContext.privileged) &&
      c.securityContext.privileged == true))
message: "{{ .kind }}/{{ .name }} uses the host network or a privileged container"
```

The [rule catalogs](rule-catalogs.md) are written this way; their rule files in `internal/rules/catalogs/` (for example `cis/cis-5.2.7-root-containers.yaml`) are further examples.

### No `:latest` image tag

Covers every workload shape, including init containers:

```yaml
id: no-latest-tag-custom
description: "Container images must be pinned to a specific tag"
severity: error
match:
  kinds: [Pod, Deployment, StatefulSet, DaemonSet, Job, CronJob]
expression: |
  (!has(resource.spec.containers) ||
    (resource.spec.containers.all(c, imageTag(c.image) != "latest") &&
      (!has(resource.spec.initContainers) ||
        resource.spec.initContainers == null ||
        resource.spec.initContainers.all(c, imageTag(c.image) != "latest")))) &&
  (!has(resource.spec.template) ||
    (resource.spec.template.spec.containers.all(c, imageTag(c.image) != "latest") &&
      (!has(resource.spec.template.spec.initContainers) ||
        resource.spec.template.spec.initContainers == null ||
        resource.spec.template.spec.initContainers.all(c, imageTag(c.image) != "latest")))) &&
  (!has(resource.spec.jobTemplate) ||
    (resource.spec.jobTemplate.spec.template.spec.containers.all(c, imageTag(c.image) != "latest") &&
      (!has(resource.spec.jobTemplate.spec.template.spec.initContainers) ||
        resource.spec.jobTemplate.spec.template.spec.initContainers == null ||
        resource.spec.jobTemplate.spec.template.spec.initContainers.all(c, imageTag(c.image) != "latest"))))
message: "{{ .kind }}/{{ .name }} has a container with :latest image"
```

(This mirrors the shipped built-in `no-latest-tag` rule, which is why the ID here is different: if a custom rule reuses a built-in `id`, both are loaded and every violation is reported twice.)

### Minimum replicas in production

```yaml
id: prod-min-replicas
description: "Production Deployments must run at least 2 replicas"
severity: error
match:
  kinds: [Deployment]
  namespaces: ["prod-*"]
expression: "has(resource.spec.replicas) && resource.spec.replicas >= 2"
message: "{{ .kind }}/{{ .name }} must have replicas >= 2 in production"
```

`has()` guards the case where `replicas` is unset (Kubernetes then defaults to 1, which is a violation here).

### Required standard labels

```yaml
id: required-labels
description: "Workloads must carry standard Kubernetes recommended labels"
severity: warning
match:
  kinds: [Deployment, StatefulSet, DaemonSet]
expression: |
  "app.kubernetes.io/name" in labels &&
  "app.kubernetes.io/version" in labels
message: "{{ .kind }}/{{ .name }} is missing required labels"
```

### No privileged containers

Handles pod-level and workload-level shapes (a `Pod` has no `spec.template`):

```yaml
id: no-privileged
description: "Containers must not run with privileged security context"
severity: error
match:
  kinds: [Pod, Deployment, StatefulSet, DaemonSet]
expression: |
  (!has(resource.spec.containers) ||
    resource.spec.containers.all(c,
      !has(c.securityContext) ||
      !has(c.securityContext.privileged) ||
      c.securityContext.privileged == false)) &&
  (!has(resource.spec.template) ||
    resource.spec.template.spec.containers.all(c,
      !has(c.securityContext) ||
      !has(c.securityContext.privileged) ||
      c.securityContext.privileged == false))
message: "{{ .kind }}/{{ .name }} has a privileged container"
```

### Allowed registries

```yaml
id: allowed-registries
description: "Images must be pulled from the internal registry"
severity: error
match:
  kinds: [Deployment, StatefulSet]
expression: |
  resource.spec.template.spec.containers.all(c,
    c.image.startsWith("registry.internal.example.com/")
  )
message: "{{ .kind }}/{{ .name }} uses an image from a disallowed registry"
```

### Resource requests required

```yaml
id: resource-requests-required
description: "All containers must specify CPU and memory requests"
severity: warning
match:
  kinds: [Deployment, StatefulSet, DaemonSet]
expression: |
  resource.spec.template.spec.containers.all(c,
    has(c.resources) &&
    has(c.resources.requests) &&
    "cpu" in c.resources.requests &&
    "memory" in c.resources.requests
  )
message: "{{ .kind }}/{{ .name }} has containers without resource requests"
```

### Disallow NodePort services

```yaml
id: no-nodeport
description: "Services must not use NodePort type"
severity: warning
match:
  kinds: [Service]
expression: |
  !has(resource.spec.type) || resource.spec.type != "NodePort"
message: "Service/{{ .name }} uses NodePort — use ClusterIP or LoadBalancer"
```

### Image digest pinning

```yaml
id: digest-pinned
description: "Production images should be pinned by digest, not tag"
severity: warning
match:
  kinds: [Deployment]
  namespaces: ["prod-*"]
expression: |
  resource.spec.template.spec.containers.all(c,
    c.image.contains("@sha256:")
  )
message: "{{ .kind }}/{{ .name }} should use digest-pinned images in production"
```

### Evaluation errors: guard or tolerate

Guard optional fields with `has()` so a missing field is a violation rather than an evaluation error. With `on_eval_error: warning` the missing key would be skipped and never reported:

```yaml
id: ingress-class-set
description: "Ingresses must set an ingress class"
severity: warning
match:
  kinds: [Ingress]
expression: |
  has(resource.spec.ingressClassName) && resource.spec.ingressClassName != ""
message: "{{ .kind }}/{{ .name }} has no ingressClassName"
```

Use `on_eval_error: warning` only where skipping a resource is genuinely acceptable: a resource whose evaluation fails is then not checked at all and produces no violation, only a log warning. If the resources that would raise the error are exactly the ones the rule should catch, guard with `has()` instead.

---

## Testing workflow

1. **Compile the expressions.** `glint rules validate` compiles every rule in the given files and prints one line per rule:

   ```bash
   glint rules validate policies/security.yaml
   ```

   It exits non-zero when a CEL expression fails to compile, a file cannot be parsed or a severity is unknown. Its limits (`internal/cli/rules.go`):
   - It only accepts rule files, not `glint.yaml` (passing it fails with a parse error, since `rules` there is a mapping). Inline rules are compiled by `glint lint` (and `glint rules list`), which fail on a compile error.
   - It only compiles. It does not run the expression against manifests, so evaluation errors from missing fields are not caught, and it does not check `match` or `on_eval_error`.
   - It compiles every loaded rule's expression, including an empty one, which fails with a CEL syntax error; `glint lint` skips rules with an empty expression.
   - Arguments are globs, like `rule_files`. A path that matches nothing prints `No rules found in provided files.` and exits `0`, so check the path if you expected output.
2. **Confirm the rule is loaded.** `glint rules list` shows each compiled rule's ID, severity, enabled state and source (`built-in`, `glint.yaml` for inline rules, or the rule file path). A rule with an empty `expression` is not compiled and does not appear:

   ```bash
   glint rules list
   glint rules list --format json
   ```

3. **Run only your rule** against sample manifests while developing:

   ```bash
   glint lint . --only-rules my-new-rule --format text
   ```

   Use a known-bad and a known-good manifest, and a manifest without the optional fields you read, to expose evaluation errors. Pass a comma-separated list for several rules. Only reported violations are filtered; all rules are still compiled and evaluated.
4. **Exclude known exceptions** with `rules.exceptions` rather than weakening the expression; see [`configuration.md`](configuration.md#exceptions).

---

## Tips

**Use `match` instead of kind checks in expressions.** This is faster and clearer:

```yaml
# Good
match:
  kinds: [Deployment]
expression: "has(resource.spec.replicas) && resource.spec.replicas >= 2"

# Avoid
expression: "kind == 'Deployment' && has(resource.spec.replicas) && resource.spec.replicas >= 2"
```

**Use `has()` before accessing optional fields.** CEL fails the evaluation on missing fields (see [Evaluation errors](#evaluation-errors-and-on_eval_error)):

```yaml
# Safe (match kinds: [Pod])
expression: |
  has(resource.spec.securityContext) &&
  has(resource.spec.securityContext.runAsNonRoot) &&
  resource.spec.securityContext.runAsNonRoot == true

# Evaluation error if securityContext is absent
expression: "resource.spec.securityContext.runAsNonRoot == true"
```

**Return a boolean.** Only `true` passes; a non-boolean result is reported as a violation.

**Always set `message`.** An empty message yields a violation with no explanation.
