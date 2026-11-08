# Rule catalogs

A rule catalog is a predefined set of rules that ships with glint and is **opt-in**: nothing is evaluated until you enable the catalog in `glint.yaml`. Catalog rules use the same schema and engine as [custom rules](writing-rules.md), and you can read them as examples. They live in `internal/rules/catalogs/`.

Currently available catalogs:

| Catalog | Description |
|---------|-------------|
| `cis` | CIS Kubernetes Benchmark v1.10, section 5 (Policies); see [CIS](#cis-kubernetes-benchmark-v110) |

---

## Enabling a catalog

```yaml
rules:
  catalogs:
    cis:
      enabled: true
      severity: warning          # optional: override the severity of every rule in the catalog
      exclude_rules:             # optional: catalog rule IDs to turn off
        - cis-5.7.3-security-context
        - cis-5.1.6-service-account-token-mount
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Load the catalog's rules |
| `severity` | string | — | `error`, `warning` or `info` (case-insensitive). Replaces the default severity of every rule in the catalog; empty keeps each rule's own severity |
| `exclude_rules` | []string | — | IDs of catalog rules to disable |

Catalog rules are reported like any other rule, with the rule ID in brackets, and count towards `fail_on`. CIS rules have mixed default severities (`error`, `warning`, `info`); with the default `fail_on: [error]` only the error-severity ones (5.1.7, 5.2.2, 5.2.3, 5.2.4, 5.2.5, 5.2.11, 5.2.12) fail the run. Use `severity` or `fail_on` to tighten or loosen that.

Validation happens in two places:

- An unknown catalog name is an error that lists the available catalogs. It is rejected when the rule engine starts (`glint lint`, `glint rules list`); `glint render` and `glint discover` do not check it.
- An unknown ID in `exclude_rules` is an error, rejected at the same point. It is checked even when `enabled: false`.
- An invalid `severity` is rejected when the config is loaded.
- A disabled catalog loads nothing, and its rules do not appear in `glint rules list`.

> `glint.yaml` is parsed without strict key checking, so a misspelt key is silently ignored. A misspelt `catalogs:` (for example `catalog:`) means no catalog rule runs. A misplaced `exclude_rules` (for example directly under `rules`) or a misspelt one (for example `exclude_rule:`) is ignored too, so the rules you meant to exclude still run. Check with `glint rules list` that the rules you expect are present and that excluded ones show `ENABLED` `no`.

---

## Excluding and replacing rules

`exclude_rules` disables a rule for the whole run. Excluded rules still show up in `glint rules list` with `ENABLED` set to `no`, so the exclusion is visible.

To replace a catalog rule with your own version, exclude it and define a rule with the same ID under `custom` or in a rule file:

```yaml
rules:
  catalogs:
    cis:
      enabled: true
      exclude_rules: [cis-5.7.4-default-namespace]
  custom:
    - id: cis-5.7.4-default-namespace
      description: "Our variant: also flag a missing namespace"
      severity: error
      expression: |
        has(resource.metadata) && has(resource.metadata.namespace) &&
        resource.metadata.namespace != "default"
      message: "{{ .kind }}/{{ .name }} must set a namespace other than default"
```

An **enabled** catalog rule whose ID equals that of a built-in, inline custom or rule-file rule is a startup error (`loading rule catalogs: ...`). Excluded catalog rules do not collide.

---

## Interaction with other features

- **Exceptions** are keyed by rule ID, so `rules.exceptions` works with catalog rules like any other (`rule: cis-5.2.2-privileged-containers`). With the catalog disabled, an exception that names a `cis-*` rule logs `exception references unknown rule ID`.
- **`--only-rules` / `--skip-rules`** filter by rule ID, for example `glint lint --skip-rules cis-5.7.3-security-context`. `--only-rules` restricts the reported findings to the listed IDs, including catalog ones. Both filter the findings after all rules have been evaluated; they do not change which rules run.
- **`glint rules list`** shows catalog rules with the source `catalog:cis`.
- **Evaluation errors** from a catalog rule are reported at `error` severity regardless of the rule's own severity (see [`on_eval_error`](writing-rules.md#evaluation-errors-and-on_eval_error)). The catalog rules are written to avoid them; one that still occurs is a rule bug worth reporting.

---

## CIS Kubernetes Benchmark v1.10

The `cis` catalog is pinned to **CIS Kubernetes Benchmark v1.10, section 5 (Policies)**. It checks the manifests glint renders, so it covers the controls that can be decided from a single resource.

Rule IDs follow `cis-<control>-<slug>`. The workload kinds are `Pod`, `Deployment`, `StatefulSet`, `DaemonSet`, `ReplicaSet`, `ReplicationController`, `Job` and `CronJob`; pod templates are resolved with [`podSpec` and `podContainers`](writing-rules.md#custom-functions), so every workload shape is handled and init and ephemeral containers are included.

### RBAC (5.1)

| Control | Rule ID | Severity | Flags | Kinds |
|---------|---------|----------|-------|-------|
| 5.1.1 | `cis-5.1.1-cluster-admin-binding` | warning | Binding to the `cluster-admin` ClusterRole | ClusterRoleBinding, RoleBinding |
| 5.1.2 | `cis-5.1.2-secrets-access` | warning | A rule granting `get`, `list`, `watch` or `*` on `secrets` or `*` | Role, ClusterRole |
| 5.1.3 | `cis-5.1.3-rbac-wildcards` | warning | `*` in `apiGroups`, `resources` or `verbs` | Role, ClusterRole |
| 5.1.4 | `cis-5.1.4-create-pods` | warning | `create` or `*` on `pods` or `*` | Role, ClusterRole |
| 5.1.5 | `cis-5.1.5-default-service-account` | warning | A ServiceAccount named `default` that does not set `automountServiceAccountToken: false` | ServiceAccount |
| 5.1.6 | `cis-5.1.6-service-account-token-mount` | info | A workload whose pod spec does not set `automountServiceAccountToken: false` | Workloads |
| 5.1.7 | `cis-5.1.7-system-masters` | error | A subject of kind `Group` named `system:masters` | ClusterRoleBinding, RoleBinding |
| 5.1.8 | `cis-5.1.8-bind-impersonate-escalate` | warning | `bind`, `impersonate`, `escalate` or `*` verbs | Role, ClusterRole |
| 5.1.9 | `cis-5.1.9-create-persistent-volumes` | warning | `create` or `*` on `persistentvolumes` or `*` | Role, ClusterRole |
| 5.1.10 | `cis-5.1.10-node-proxy` | warning | `nodes/proxy`, `*/proxy` or `*` in `resources` with at least one verb | Role, ClusterRole |
| 5.1.11 | `cis-5.1.11-csr-approval` | warning | `update`, `patch` or `*` on `certificatesigningrequests/approval`, `*/approval` or `*` | Role, ClusterRole |
| 5.1.12 | `cis-5.1.12-webhook-configurations` | warning | `create`, `update`, `patch`, `delete` or `*` on `validatingwebhookconfigurations`, `mutatingwebhookconfigurations` or `*` | Role, ClusterRole |
| 5.1.13 | `cis-5.1.13-service-account-token-creation` | warning | `create` or `*` on `serviceaccounts/token`, `*/token` or `*` | Role, ClusterRole |

### Pod security (5.2)

All 5.2 rules apply to the workload kinds listed above.

| Control | Rule ID | Severity | Flags |
|---------|---------|----------|-------|
| 5.2.2 | `cis-5.2.2-privileged-containers` | error | A container with `securityContext.privileged: true` |
| 5.2.3 | `cis-5.2.3-host-pid` | error | `hostPID: true` |
| 5.2.4 | `cis-5.2.4-host-ipc` | error | `hostIPC: true` |
| 5.2.5 | `cis-5.2.5-host-network` | error | `hostNetwork: true` |
| 5.2.6 | `cis-5.2.6-allow-privilege-escalation` | warning | A container that does not set `allowPrivilegeEscalation: false` |
| 5.2.7 | `cis-5.2.7-root-containers` | warning | A container that may run as root; see [5.2.7 semantics](#527-root-containers) |
| 5.2.8 | `cis-5.2.8-net-raw` | warning | A container that does not drop `NET_RAW` (or `ALL`), or that adds `NET_RAW` |
| 5.2.9 | `cis-5.2.9-added-capabilities` | warning | A container with a non-empty `capabilities.add` |
| 5.2.10 | `cis-5.2.10-drop-all-capabilities` | info | A container that does not drop `ALL` capabilities |
| 5.2.11 | `cis-5.2.11-windows-hostprocess` | error | `windowsOptions.hostProcess: true` at pod or container level |
| 5.2.12 | `cis-5.2.12-hostpath-volumes` | error | A `hostPath` volume |
| 5.2.13 | `cis-5.2.13-host-ports` | warning | A container port with a non-zero `hostPort` |

### Secrets and general policies (5.4, 5.7)

| Control | Rule ID | Severity | Flags | Kinds |
|---------|---------|----------|-------|-------|
| 5.4.1 | `cis-5.4.1-secrets-as-env` | warning | A container that reads a Secret through `env[].valueFrom.secretKeyRef` or `envFrom[].secretRef` | Workloads |
| 5.7.2 | `cis-5.7.2-seccomp-profile` | warning | A container whose effective seccomp profile is not `RuntimeDefault` or `Localhost`; see [5.7.2 semantics](#572-seccomp-profile) | Workloads |
| 5.7.3 | `cis-5.7.3-security-context` | info | A workload with no pod `securityContext` and at least one container without one | Workloads |
| 5.7.4 | `cis-5.7.4-default-namespace` | warning | `metadata.namespace: default` | All kinds |

### Rule semantics

#### 5.2.7 root containers

Every container (`containers`, `initContainers`, `ephemeralContainers`) must pass. A container passes when any of these holds:

- its `securityContext.runAsNonRoot` is `true`
- its `securityContext.runAsUser` is greater than `0`
- it sets neither `runAsNonRoot: false` nor any `runAsUser`, and the pod `securityContext` has `runAsNonRoot: true` or a `runAsUser` greater than `0`

A container-level `runAsUser` (even `0`) or `runAsNonRoot: false` stops the pod-level values from being inherited. Absent and null fields count as unset.

#### 5.7.2 seccomp profile

Every container, including init and ephemeral containers, must have an effective `RuntimeDefault` or `Localhost` seccomp profile. A container's own `seccompProfile.type` overrides the pod-level value; the pod-level value applies only to containers that set none.

### Not covered

These controls are not implemented, because they cannot be decided from a single manifest:

| Control | Why it is out of scope |
|---------|------------------------|
| 5.2.1 | Ensure that the cluster has at least one active policy control mechanism in place: a cluster-level property, not a property of a manifest |
| 5.3.1 | Ensure that the CNI in use supports NetworkPolicies: depends on the cluster's network plugin |
| 5.3.2 | Ensure that all Namespaces have NetworkPolicies defined: needs a cross-resource check between Namespaces and NetworkPolicies |
| 5.4.2 | Consider external secret storage: a process and architecture decision |
| 5.5.1 | Configure image provenance using ImagePolicyWebhook admission controller: API server configuration |
| 5.7.1 | Create administrative boundaries between resources using namespaces: a process control |

### Limitations

- **RBAC rules ignore `apiGroups`.** 5.1.2, 5.1.4, 5.1.9 to 5.1.13 match on `resources` and `verbs` only, and 5.1.8 checks `verbs` only (ignoring `resources` too). Only 5.1.3 reads `apiGroups`. A rule for `secrets` in an unrelated API group is reported too, so they can over-report but not under-report.
- **5.7.4 only catches an explicit `namespace: default`.** A manifest without `metadata.namespace` is not flagged, because glint cannot know the namespace it will be applied to (for example through `kubectl -n` or an ArgoCD destination namespace). When glint renders a Helm chart and the app has no destination namespace, the release namespace defaults to `default`, so charts that template `.Release.Namespace` are flagged; set the destination namespace or exclude the rule.
- **Evaluation errors are reported at `error` severity** regardless of the rule's own severity, because every catalog rule sets `on_eval_error: error` (a rule with `on_eval_error: warning` logs and skips instead).
- **Null entries in container lists are handled inconsistently.** `podContainers` returns list elements as-is, so a `null` entry in `containers` is not skipped; in testing no catalog rule raised an evaluation error on it: some rules pass it, others flag it as a container without settings. The API server rejects such manifests anyway.
- **glint checks manifests, not live cluster state.** The catalog does not replace [kube-bench](https://github.com/aquasecurity/kube-bench) and complements it: kube-bench audits nodes and the control plane, glint audits what you are about to deploy.
