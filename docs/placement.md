# Workload placement — namespaces, naming, labels

Validated architecture: **the Workspace CRs (and all governance:
templates, policies, catalog, webhooks) stay in the platform
namespace**; only the workloads (Deployment/StatefulSet/Pod, Service,
home PVC, VM) go into a target namespace.

The CR namespace (`workspaces.namespace`, Helm) is **empty by
default**: it then resolves to the release namespace, i.e. the
same namespace where the operator/api-server/wwt/frontend run.
An admin who wants to isolate the CRs in their own namespace sets
`workspaces.namespace` explicitly.

## Pattern precedence chain

From highest to lowest priority — **enforced server-side**
(the api-server resolves, the webhook re-checks the same chain, the UI
only displays the result):

1. **`spec.placement.namespace` of the template** (overridable at
   instantiation if the `placement` field is delegated);
2. **`WAAS_DEFAULT_NAMESPACE_PATTERN`** — environment variable
   shared by the operator and the api-server (**a single Helm key**:
   `workspaces.defaultNamespacePattern`, GitOps-driveable). An
   invalid pattern (unknown placeholder, no room for expansion) makes
   both components **refuse to start** — never a silent fallback,
   a placement different from what Git declares would be an invisible
   drift;
3. **built-in `waas-workspaces`**: a single shared namespace.

⚠️ **Changing the pattern (variable or template) only affects
NEW workspaces**: the resolved value is frozen into
`spec.targetNamespace` at creation and immutable afterward. Existing
workspaces keep their namespace — this is intended, not a bug.

## Placeholders

List served by `GET /api/v1/meta/placeholders` (single source:
`pkg/naming`) and shown as contextual help in the template editor; the
**resolved** namespace is displayed at creation time
(`GET /api/v1/workspaces/namespace-preview`) and in the admin fleet view.

| Token | Source | Absence |
|---|---|---|
| `{user}` | OIDC IdP username (trusted identity) | never absent (identity required) |
| `{workspace}` | workspace displayName | empty → `x` (sanitization) |
| `{templateName}` | template `metadata.name` | never absent |
| `{os}` | `template.spec.os` — the actual provisioning path (pod vs VM), required and enum-validated | never absent |

Cross-cutting rules:
- **systematic sanitization** of each value (NFKD, lowercase,
  runs → `-`, DNS-1123) — no raw value ever enters a name;
- **unknown placeholder = rejection** (template webhook, 400 API, refusal
  to start for the global variable) — never resolved to an empty string;
- **anti-collision truncation**: a budget of 63 is split between tokens
  after the literals; a value that overflows its budget is truncated **and**
  suffixed with a short deterministic hash of the raw value — two distinct
  long values can never silently merge, and the same value always lands
  in the same namespace. Short values stay readable (no hash).

## Target namespace (`spec.targetNamespace`)

- The api-server **resolves the pattern once at creation** and writes
  the explicit value into `workspace.spec.targetNamespace`. The webhook makes it
  **immutable** (like `owner`): moving a workspace means recreating it.
- Empty (workspaces created before the feature, or via kubectl without
  a value) = historical behavior: workloads next to the CR.
- Override at creation: explicit `targetNamespace` in the payload,
  gated by the overridable field **`placement`** (template ∩ policy,
  admins exempt).
- **Shared namespaces**: the server-resolved default may be shared
  (built-in `waas-workspaces`, `{os}`/`{templateName}` patterns). The webhook
  always admits the server-resolved default; the `waas-<user>` prefix
  rule only applies to deviations. A shared namespace receives
  **neither** an ownership label **nor** an auto ResourceQuota (it
  would cap the whole team at one person's budget) — the webhook remains
  the per-user enforcement, the admin sets a namespace quota if they want
  one.

### Sanitization (operator/pkg/naming — shared api-server/webhook/operator)

1. NFKD decomposition, combining marks removed (`é`→`e`);
2. lowercase;
3. any run outside `[a-z0-9]` → a single `-`;
4. trim `-`; empty result → `x`;
5. truncation ≤ 63 (DNS-1123), never on a `-`.

Normalization is lossy (`Zoé` and `zoe` collide): collisions
are broken by a **deterministic suffix** `-xxxxx` (5 hex chars of
sha256 of the raw value), applied only when the collision is
detected at creation, then frozen in the spec.

### Anti-spoofing enforcement (webhook, fail-closed)

- System/platform namespaces refused for everyone (`kube-*`, the
  CR namespace), bypass included.
- For a non-admin, `targetNamespace` must either match the
  `waas-<sanitize(username)>` prefix **recomputed from the trusted
  identity** (frozen annotations / userInfo — never a supplied value),
  or designate an existing namespace carrying `waas.xorhub.io/owner=<owner>`.
- Second line: the reconciler re-checks via `policy.CheckOverrides`
  before creating any compute (workspaces applied via GitOps).

### Namespace bootstrap (operator, create-only)

Created on first workload if it doesn't exist — never modified
afterward (admin settings are not overwritten):

- **Labels**: `app.kubernetes.io/managed-by=waas-operator`,
  `waas.xorhub.io/owner=<owner>`, Pod Security
  `enforce=baseline` / `warn=restricted` (desktops run non-root
  but may require baseline; warn surfaces hardening candidates) +
  template labels/annotations
  (`placement.namespaceLabels/Annotations`, denylist applied, platform
  keys always win).
- **`waas-quota` ResourceQuota** derived from the owner policy's
  aggregate caps (`limits.aggregate` → requests/limits cpu/memory,
  requests.storage). Defense in depth: the webhook remains the
  primary enforcement.
- **`waas-default-ingress` NetworkPolicy**: ingress denied except from
  the CR namespace **and** the release namespace (`WAAS_PLATFORM_NAMESPACE`,
  injected by the chart via the downward API) — that's where
  guacd/wwt actually run and they need to reach the desktops. Egress open.
- **No user RBAC**: users never talk directly to the Kubernetes
  API (everything goes through the portal); creating any would be
  gratuitous attack surface.

⚠️ **secretKeyRef constraint**: a template's `env.valueFrom.secretKeyRef`
resolves in the **pod's** namespace, i.e. the target
namespace. A placed template referencing a Secret in the platform
namespace (e.g. `dev-ssh-credentials`) breaks at startup
(`CreateContainerConfigError`): provision the Secret in the target
namespaces (External Secrets/Vault) or don't place that template.
Protocol `credentialsSecretRef`s are NOT affected: they are
resolved on the api-server side in the platform namespace.

### GC and cleanup

- ownerReferences don't cross namespaces: placed workspaces carry
  the **`waas.xorhub.io/teardown` finalizer**; on deletion, the operator
  deletes compute + service in the target namespace (the home PVC is
  kept — contract unchanged). Watches are remapped by labels
  (`waas.xorhub.io/workspace` +
  `waas.xorhub.io/workspace-namespace`).
- **Namespace cleanup: `Retain` by default** (`placement.cleanup`).
  Rationale: deleting a namespace deletes its PVCs, and the home
  outlives workspace deletion — Retain is the only default that cannot
  destroy data. `DeleteWhenEmpty` (opt-in) only deletes if the
  operator created the namespace AND no waas object remains in it
  (home PVC included).
- The cleanup policy is **frozen on the namespace at its creation**
  (label `waas.xorhub.io/cleanup`) and applied by the **namespace
  janitor**, an internal operator reconciler re-triggered by content
  deletion events: the reclamation survives template deletion, the
  asynchronous PVC drain (pvc-protection), and the late deletion of a
  retained volume. Details and unblocking procedure:
  `docs/workspace-deletion.md`.

## Workload naming (`spec.workloadName`)

- The api-server computes `sanitize(displayName)` (fallback: CR name) +
  a per-namespace anti-collision suffix, and freezes it into
  `spec.workloadName` (immutable). Deployment/Service = `<workloadName>`,
  home PVC = `<workloadName>-home`.
- **Renaming the displayName never renames the compute** (a rename =
  explicit recreation if the need arises).
- The webhook refuses workloadName collisions within the same
  target namespace (legacy `ws-<cr>` names counted).

### Migrating existing resources

None: empty `workloadName`/`targetNamespace` ⇒ historical names
(`ws-<cr>`) and namespace preserved, ownerReferences and GC unchanged.
The new convention only applies to new creations. No PVC is
moved or recreated.

## Custom labels/annotations

- Template: `placement.namespaceLabels/namespaceAnnotations` (namespace)
  and `workload.labels/annotations` (Deployment **and** pod template).
- Workspace: `overrides.labels/annotations`, gated by the overridable
  field **`metadata`**.
- **Server-side denylist** (`operator/pkg/metakeys`, webhook + reconciler
  re-filtering) by domain: `kubernetes.io` and subdomains
  (pod-security, kubectl…), `k8s.io`, `xorhub.io` (platform labels),
  `argoproj.io`, injectors (`istio.io`, `linkerd.io`,
  `vault.hashicorp.com`), `cilium.io`, `openshift.io`.
- The operator's labels (ownership, selectors) are applied after the
  merge: **they always win**, and the Deployment selector stays
  `waas.xorhub.io/workspace`.

## Added RBAC (operator)

`namespaces` create/delete, `resourcequotas` create,
`networkpolicies` create, `workspaces` update (finalizer) — Helm
mirror verified by `internal/controller/rbac_test.go`.
