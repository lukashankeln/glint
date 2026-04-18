# Writing custom rules

glint policies are [CEL](https://cel.dev) expressions evaluated against each Kubernetes manifest. A rule returns `true` to indicate the resource is **compliant**; returning `false` produces a violation.

---

## Rule schema

```yaml
rules:
  custom:
    - id: my-rule              # unique identifier (required)
      description: "..."       # human-readable explanation
      severity: error          # error | warning | info
      match:                   # pre-filter (optional)
        kinds: [Deployment]
        api_groups: [apps]
        namespaces: ["prod-*"]
        exclude_namespaces: ["test-*"]
        labels:
          team: platform
      expression: |            # CEL expression (required)
        resource.spec.replicas >= 2
      message: "{{ .kind }}/{{ .name }} requires replicas >= 2"
```

---

## CEL variables

Every expression has access to these variables:

| Variable | Type | Description |
|----------|------|-------------|
| `resource` | `map` | Full manifest (Kubernetes Unstructured) |
| `name` | `string` | `metadata.name` |
| `namespace` | `string` | `metadata.namespace` (empty for cluster-scoped) |
| `kind` | `string` | e.g. `"Deployment"` |
| `apiVersion` | `string` | e.g. `"apps/v1"` |
| `labels` | `map<string,string>` | `metadata.labels` |
| `annotations` | `map<string,string>` | `metadata.annotations` |

---

## Custom functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `imageTag` | `imageTag(image string) string` | Extract tag from an image reference. Returns `"latest"` for untagged images, `""` for digest-pinned. |
| `inList` | `inList(value string, list list) bool` | True if value appears in list |
| `matchesGlob` | `matchesGlob(str string, pattern string) bool` | Doublestar glob match |
| `semverLT` | `semverLT(a string, b string) bool` | True if semver `a` is less than `b` |

Standard CEL built-ins (`has()`, `size()`, `.all()`, `.any()`, `.filter()`, `string()`, `int()`, etc.) are all available.

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

Use `match` to avoid writing kind checks in every expression — it's more efficient and makes rules easier to read.

---

## Message templates

The `message` field is a Go `text/template`. Available variables:

| Variable | Value |
|----------|-------|
| `{{ .kind }}` | Resource kind |
| `{{ .name }}` | Resource name |
| `{{ .namespace }}` | Resource namespace |
| `{{ .apiVersion }}` | API version |
| `{{ .severity }}` | Rule severity |

---

## Examples

### No `:latest` image tag

```yaml
- id: no-latest-tag
  description: "Container images must be pinned to a specific tag"
  severity: error
  match:
    kinds: [Deployment, StatefulSet, DaemonSet, Job, CronJob]
  expression: |
    resource.spec.template.spec.containers.all(c,
      imageTag(c.image) != "latest"
    )
  message: "{{ .kind }}/{{ .name }} has a container with :latest image"
```

### Minimum replicas in production

```yaml
- id: prod-min-replicas
  description: "Production Deployments must run at least 2 replicas"
  severity: error
  match:
    kinds: [Deployment]
    namespaces: ["prod-*"]
  expression: "resource.spec.replicas >= 2"
  message: "{{ .kind }}/{{ .name }} must have replicas >= 2 in production"
```

### Required standard labels

```yaml
- id: required-labels
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

```yaml
- id: no-privileged
  description: "Containers must not run with privileged security context"
  severity: error
  match:
    kinds: [Deployment, StatefulSet, DaemonSet, Pod]
  expression: |
    resource.spec.template.spec.containers.all(c,
      !has(c.securityContext) ||
      !has(c.securityContext.privileged) ||
      c.securityContext.privileged == false
    )
  message: "{{ .kind }}/{{ .name }} has a privileged container"
```

### Allowed registries

```yaml
- id: allowed-registries
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
- id: resource-requests
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
- id: no-nodeport
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
- id: digest-pinned
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

---

## Validating rule files

Before committing, check that your CEL expressions compile:

```bash
glint rules validate policies/security.yaml
```

This catches syntax errors without running a full lint pass.

---

## Loading rules from files

Share policies across repositories by putting rules in standalone YAML files:

```yaml
# policies/security.yaml
- id: no-privileged
  ...
- id: no-nodeport
  ...
```

Reference them from `glint.yaml`:

```yaml
rules:
  rule_files:
    - "policies/security.yaml"
    - "policies/labeling.yaml"
```

---

## Tips

**Use `match` instead of kind checks in expressions.** This is faster and clearer:

```yaml
# Good
match:
  kinds: [Deployment]
expression: "resource.spec.replicas >= 2"

# Avoid
expression: "kind == 'Deployment' && resource.spec.replicas >= 2"
```

**Use `has()` before accessing optional fields.** CEL will error at runtime on missing fields:

```yaml
# Safe
expression: |
  !has(resource.spec.securityContext) ||
  !has(resource.spec.securityContext.runAsRoot) ||
  resource.spec.securityContext.runAsRoot == false

# Will panic if securityContext is absent
expression: "resource.spec.securityContext.runAsRoot == false"
```

**Test with `--only-rules` while developing:**

```bash
glint lint . --only-rules my-new-rule --format text
```
