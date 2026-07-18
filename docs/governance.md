# Workspace governance — catalog, policies, enforcement

Admin-defined guardrails for self-service workspaces: the admin approves
**images** (catalog) and assigns **limits** (policies) to IdP (OIDC)
users/groups; users deploy freely inside that envelope, whether they go
through the portal or straight at the Kubernetes API.

## Data model

```
OIDC IdP (groups claim)                      Workspace CR
        │                                        │ spec.templateRef
        │ mirrored: users.user_groups            ▼
        │ + identity annotations           WorkspaceTemplate
        ▼                                        │ spec.image (verbatim)
WorkspacePolicy ── images[] ───────────►  WorkspaceImage
 (who may use how much)                    (what is approved, for whom)
```

- **`WorkspaceImage`** (`wsi`): one approved image — exact ref (pin the
  digest), protocols, architectures (→ node affinity ARM64/AMD64),
  `enabled` kill-switch, `allowedGroups` global restriction, and default/
  min/max sizing.
- **`WorkspacePolicy`** (`wsp`): priority, subjects (`User`/`Group`),
  image subset, limits (`maxWorkspaces`, `maxRunningWorkspaces`,
  `perWorkspace`, `aggregate`, `defaults`), lifecycle
  (`idleSuspendAfter`, `maxLifetime`).
  `maxWorkspaces` caps OWNERSHIP (paused workspaces count — their home
  PVC still holds storage) while `maxRunningWorkspaces` caps compute
  CONCURRENCY (paused workspaces and retained volumes don't count);
  the latter is enforced at both transitions into compute: creating a
  non-paused workspace and resuming a paused one.
  `limits.defaults` is the sizing the portal pre-selects on the creation
  sliders when the image declares no `resources.default` (image wins);
  it is display-only and never enforced.
- A user's **effective images** = enabled catalog ∩ policy `images`
  ∩ `allowedGroups` match. The same function
  (`operator/pkg/policy.AllowedImages`) feeds the webhook, the reconciler
  and the portal — they cannot disagree.

## Policy resolution (validated decision)

Among policies whose subjects match the user, the **highest
`spec.priority` wins and applies as a whole — no field merging**. Ties
break on the lexicographically smallest name and emit a warning.
`subjects: []` matches every authenticated user; ship a `default`
policy at priority 0 as the restrictive fallback. **No matching policy
= denial** (fail closed). Convention: 0 default, 100–999 groups,
1000+ per-user exceptions, 10000 the `admins` policy.

### Admin all-rights policy

Admins are subject to the admission gates like everyone else — the
escape hatch is the bootstrapped `admins` WorkspacePolicy (priority
10000, subject `User:admin` + your IDP admin groups): no
`limits`/`lifecycle` fields (absent fields constrain nothing — the
quota/TTL checks are all nil-guarded), whole catalog, every override
field, remote workspaces on. This keeps the bypass **explicit and
auditable** (a CR in Git, visible in effective-policy) instead of a
code path, and leaves fail-closed intact for every other user.
Canonical channel: `gitops/governance/policies.yaml`; the chart can
render the equivalent (`adminPolicy.*`, disabled by default) when it is
the sole governance source — never enable both for the same name.

### Bootstrap default policy

Fail-closed cuts both ways: with zero policies in the cluster (no
GitOps, `adminPolicy` off), **no one** can create a workspace, not even
regular users — "no matching policy = DENY" applies uniformly.
`defaultPolicy.*` (`values.yaml`) renders a catch-all `WorkspacePolicy`
at priority 0 (the CRD's own "default fallback" convention, no
`subjects` = every authenticated user) with a modest quota (3
workspaces, per-workspace/aggregate caps, 2h idle-suspend, 14-day
max-lifetime). Defaults **on**, same doctrine as `adminPolicy` and the
catalogs above: a visible, auditable CR, editable/removable afterwards,
disabled once `gitops/governance/policies.yaml` defines the equivalent
— never both at once for the same name.

### Bootstrap catalog entries

Same doctrine, for the catalog side: the chart can render the two
official registry-wide `WorkspaceImage` entries (`catalogs.*` in
`values.yaml`) so a first install already has a non-empty picker
instead of waiting on GitOps. `catalogs.waasImages` (registry
`docker.io/xorhub`) defaults **on**; `catalogs.kasm`
(registry `docker.io/kasmweb`, `kasmvnc`-only) defaults **off** since
it's an extra data plane an admin opts into. Both sync their picker
metadata live from the `catalog-*.yaml` manifests published by the
`waas-images` repo (see `docs/image-catalog.md`). Canonical channel:
`gitops/governance/images.yaml`; disable the matching `catalogs.*` flag
once that takes over, same never-both-at-once rule as `adminPolicy`.

## Identity binding (trusted-writer model, validated decision)

- Portal path: the api-server authenticates against the OIDC IdP, then
  writes `spec.owner` (platform UUID) plus the identity annotations
  `waas.xorhub.io/username` and `waas.xorhub.io/groups`. The webhook
  accepts those **only** from the SAs listed in `WAAS_TRUSTED_WRITERS`
  (set by Helm to the api-server SA).
- Direct kubectl: identity comes from the request `userInfo`;
  `spec.owner` must equal the authenticated username and the identity
  annotations must be absent — anything else is denied
  (`IdentityViolation`).
- Both paths: owner and identity annotations are **immutable** after
  creation, whoever calls.
- The group mirror `users.groups` has two writers, by design:
  - **OIDC login** (when `apiServer.oidc.*` is configured in Helm): the
    IdP's `groups` claim overwrites the mirror at **every** SSO login;
    role can also be synced via `adminGroups`. Local login stays
    available as break-glass — unless disabled, see below.
  - **Admin editing** (always available): the Users page or
    PATCH `/api/v1/users/{id}` with `groups` — the only path when OIDC
    is not configured.
  A user whose mirror is empty matches only subjects-less policies:
  that is the `default`-policy-for-everyone symptom, not a priority
  bug.

### OIDC-only login (`WAAS_LOGIN_OIDC_ONLY`)

`WAAS_LOGIN_OIDC_ONLY` (Helm: `apiServer.oidc.disableLocalLogin`)
disables local username/password login **for everyone without
exception, bootstrap admin included** — every account must go through
the IdP. Opt-in, never the default. Behavior:

- `POST /api/v1/auth/login` answers 404 from a guard at the top of the
  handler (route mounting unchanged, same pattern as the unconfigured
  SSO endpoints); `GET /api/v1/auth/providers` returns `local: false`,
  so the portal renders only the SSO button.
- **Fail-closed at startup**: the flag without a configured OIDC
  issuer/clientID makes the api-server refuse to start — a mistaken
  flag can never silently lock everyone out. The chart deliberately
  emits the env var **outside** the `issuerURL` block so that
  misconfiguration reaches the startup error instead of being hidden by
  Helm.
- `EnsureBootstrapAdmin` stays unconditional — the bootstrap admin
  account still exists, it just cannot log in locally while the flag is
  set. There is deliberately **no hidden bypass** for it: the
  break-glass is redeploying without the flag (a cluster-admin act,
  visible and auditable), signing in locally, fixing the IdP, then
  setting the flag back.
- Set `adminGroups` at the same time: with the flag on and
  `WAAS_OIDC_ADMIN_GROUPS` empty, no account can ever reach the admin
  role through SSO (role sync only runs when `adminGroups` is
  configured) while the bootstrap admin is unreachable — the api-server
  logs a startup warning for this combination.

### Clipboard policy

`spec.clipboard` gates the two clipboard directions independently
(`copyFromWorkspace`, `pasteToWorkspace`; absent = allowed). The
api-server resolves the CONNECTING user's policy at session start and
stamps the grant into the connection token; the wwt proxy enforces it by
filtering `clipboard` streams in both directions and clamps the
overlay's live toggles to the grant. No policy match = both directions
denied (fail closed) while the session itself still opens.

On the **kasmvnc** data plane there is no guacd tunnel to filter:
enforcement is container-side instead. The operator derives KasmVNC DLP
directives from the workspace **owner's** resolved policy at reconcile
and stamps them into the mounted `kasmvnc.yaml`, so a policy denial
actually disables copy/paste inside the container (see
`docs/kasmvnc.md` and the full precedence map in `docs/clipboard.md`).
Either way the policy stays the sole security authority — templates and
connection params can only restrict further, never widen.

### Override restriction (`spec.overrides`)

`spec.overrides.allowedFields` bounds instantiation-time template
overrides for the governed users; the effective allow-list is the
intersection with the template's own `overrides.allowedFields` (see
`docs/templates-and-protocols.md`). Block absent = no policy
restriction; empty list = all overrides forbidden. Admins bypass both
lists, template owners only the template one.

#### Governable fields — single registry

The list of valid `allowedFields` values lives in ONE place:
`AllOverridableFields()` + `OverridableFieldDescriptions()`
(`operator/api/v1alpha1/workspacetemplate_types.go`) and the enforcement
claims in `operator/pkg/policy/overrides.go`. The CRD enum (kubectl and
GitOps validation), the api-server's policy validation, the admin UI
(`GET /api/v1/meta/override-fields` feeds the policy and template
editors) and the enforcement all derive from it — no duplicated list
anywhere, guarded by tests (below).

| Field | Grants | Enforced at |
|---|---|---|
| `env` | merge env vars over the template's | admission (creation/update) |
| `securityContext` / `podSecurityContext` | replace the container / pod security context | admission |
| `volumes` | add volumes and mounts | admission |
| `nodeSelector` / `tolerations` | steer pod scheduling | admission |
| `resources` | choose the sizing — **`spec.resources` present = override, whatever its values**; policy limits keep bounding them separately | admission |
| `protocol` | pick the default protocol among the template's | admission |
| `protocolParams` | tune guacd parameters at connect time; the template's per-protocol `userParams` stays the fine-grained name filter | api-server `/connect` |
| `schedule` | replace the uptime/downtime crons | admission |
| `placement` | target a namespace deviating from the resolved default pattern (ownership still checked separately for every caller) | admission |
| `metadata` | add labels/annotations on the workload; the reserved-keys denylist (`pkg/metakeys`) applies on top, always | admission |

Fail-closed rules: a field absent from the template's list is denied
(owner/admin excepted); if the policy declares `overrides`, the field
must be in BOTH lists. **Pausing is exempt** — it only frees compute, so
a grandfathered workspace that no longer complies can always be paused;
resuming re-runs the full check.

#### Adding a new field to the Workspace spec

The registry tests (`operator/pkg/policy/overrides_registry_test.go`)
force the decision — the build stays red until the new field is either:

1. **governed**: add an `OverridableField` const + Enum marker + entry in
   `AllOverridableFields()` and `OverridableFieldDescriptions()`, claim
   the JSON field in `overrideClaims`/`specClaims`, run `make manifests`,
   add its case to the enforcement-matrix test; or
2. **exempt**: add it to `specExempt` with the reason (e.g. checked by a
   dedicated webhook rule, cosmetic only).

Nothing else to touch: CRD, api-server validation, UI editors and
enforcement follow automatically.

### Remote Workspaces opt-in (`spec.remoteWorkspaces`)

`spec.remoteWorkspaces: true` opts the governed users into the Remote
Workspaces feature (out-of-cluster machines via guacd, see
`docs/remote-workspaces.md`). Absent/false = the feature is invisible in
the portal and refused by the API (fail closed); platform admins always
have it. The flag reaches the portal through `GET /api/v1/me/quota`
(`features.remoteWorkspaces`).

### Debugging resolution

`GET /api/v1/admin/users/{id}/effective-policy` replays the exact
resolution the webhook performs and returns the resolved identity,
every policy with its match outcome (`via` = matching subject), the
winner and any tie warnings. The same view is embedded in the admin
Users page (edit dialog).

## Admission decision matrix

| # | Check (order) | Failure → reason code |
|---|---|---|
| 1 | owner / identity annotations unchanged (update) | `IdentityViolation` |
| 2 | spec unchanged on update → **admit** (grandfathering) | — |
| 3 | template missing → **admit with warning** (GitOps ordering; reconciler enforces before compute) | — |
| 4 | windows template without KubeVirt | denied (cluster fact, bypass included) |
| 5 | caller in `WAAS_POLICY_BYPASS` → **admit with warning** | — |
| 6 | identity resolution (trusted writer vs userInfo) | `IdentityViolation` |
| 7 | a policy matches | `NoPolicyMatches` |
| 8 | template image in catalog | `ImageNotInCatalog` |
| 9 | image enabled | `ImageDisabled` |
| 10 | image allowed (groups + policy subset) | `ImageNotAllowed` |
| 11 | template protocol served by image | `ProtocolMismatch` |
| 12 | sizing within image min/max and policy perWorkspace | `ResourcesOutOfBounds` |
| 13 | count + aggregates within policy | `QuotaExceeded` |

Denials read `[ReasonCode] human message with the numbers` — surfaced
verbatim by kubectl, mapped to HTTP 403 by the api-server, stored in the
CR's `Ready` condition by the reconciler, and shown by the portal.

**Second line (reconciler)**: the same evaluation re-runs just before
compute creation — covers the admission TOCTOU race, templates that
arrived after the workspace, and images disabled in between. Running
pods are **never** torn down by policy: pre-governance workspaces are
grandfathered until their next spec change, but they DO count toward
quotas. Lifecycle is the exception: `maxLifetime` deletes expired
workspaces (home included) from the operator; `idleSuspendAfter` pauses
session-less workspaces from the api-server's sweeper (it owns session
data), compute freed, home kept.

## Procedures

**Add an image to the catalog**: build/push via the `waas-images` repo
(separate from this one since 2026-07-10) → add a
`WorkspaceImage` in `gitops/governance/images.yaml` (or admin console)
with the exact ref → create a `WorkspaceTemplate` using that ref →
reference it from policies (or leave `images: []`). Adding the template
without the catalog entry yields `ImageNotInCatalog` at workspace
creation.

**Change a quota**: edit the `WorkspacePolicy` (Git or console). Applies
to new creations/resumes immediately; running workspaces are untouched
until their next spec change.

**Emergency-disable an image**: `POST /api/v1/admin/images/{name}/disable`
or `kubectl patch wsi <name> --type=merge -p '{"spec":{"enabled":false}}'`.
New workspaces are blocked instantly; running ones keep working (pause
them via the console if the image is actively dangerous).

**Admin writes** (validated decision): the console edits CRDs directly —
CRDs are the source of truth, Git seeds them. Configure the ArgoCD app
with `selfHeal: false` or `ignoreDifferences` on these two kinds. A
PR-based flow can be added later for regulated setups.

## Admin console (editing model)

The image catalog **and** policies are editable from the admin UI. The
model is deliberately simple: the api-server **writes the CR directly**
(`AdminUpsertImage`/`AdminUpsertPolicy`, validated server-side, audited).

- If you run these objects through **GitOps/ArgoCD**, Git remains the
  source of truth: a UI edit is a manual override that ArgoCD overwrites
  on the next sync (keep selfHeal as you see fit). In manual/test setups
  the drift is negligible.
- Both YAML editors are **pre-filled with the whole schema** (every field,
  even empty), generated server-side from the PUT payload types
  (`GET /api/v1/meta/scaffold/{kind}`) — never a hand-maintained template.
  On edit the scaffold is deep-merged with the object (real values win),
  so admins discover every available field without leaving the editor.
- **User creation** takes a group selection: chips from the known groups
  (`GET /api/v1/admin/groups` = policy Group subjects ∪ existing users'
  groups) plus free entry. No group ⇒ only subjects-less policies match
  (the `default` policy). The IdP stays the source of truth — the OIDC
  claim overwrites the mirror at the first SSO login.
- The **Fleet** dashboard has a *Remote workspaces* tab
  (`GET /api/v1/admin/remote-workspaces`, metadata only, never
  credentials): owner, target, protocol, MAC/WoL, session activity, last
  connection.

## API (portal contract)

`GET /api/v1/catalog`, `GET /api/v1/me/quota`, and under admin:
`GET/PUT/DELETE /admin/images[/{name}]`, `POST /admin/images/{name}/enable|disable`,
`GET/PUT/DELETE /admin/policies[/{name}]`, `GET /admin/usage`,
`GET /admin/groups`, `GET /admin/remote-workspaces`,
`GET /api/v1/meta/scaffold/{kind}`.
Request/response shapes are the Go models (`api-server/internal/model`),
mirrored to the frontend via tygo (`frontend/src/types.gen.ts`).

## Audit

- API server: `workspace.created/denied/paused/resumed/auto_paused`,
  `catalog.image_*`, `policy.*` in the audit table (who, what, when,
  which policy, client IP).
- Operator: `PolicyApplied` / denial-reason / `MaxLifetimeReached`
  Kubernetes Events on each Workspace + structured logs; denial reason
  and message land in `status.conditions[Ready]`.

## Threat model

| Threat | Mitigation |
|---|---|
| Bypass the portal with kubectl | Enforcement is the **admission webhook** (`FailurePolicy=Fail`, fail-closed) + reconciler re-check; the portal implements no rule of its own. |
| Spoof another owner (`spec.owner: bob`) | Untrusted callers must have `owner == userInfo.username`; owner immutable afterwards. |
| Self-grant groups via annotations | Identity annotations only accepted from `WAAS_TRUSTED_WRITERS` SAs; forged ones are denied, and they are immutable post-creation. |
| Escalate by editing the CR (bigger resources, other template) | Every spec change re-runs the full matrix above. |
| Quota race (two parallel creates) | Reconciler re-checks against *granted* capacity before creating compute; the loser goes `Failed/QuotaExceeded`. |
| Webhook outage | `FailurePolicy=Fail`: no workspace admission while down — availability traded for integrity, deliberately. |
| Rogue admin / compromised api-server SA | Its RBAC is namespace-scoped to the workspace namespace; catalog/policy edits are audited; bypass list is short, explicit and Helm-reviewed. |
| Stale groups (user removed from an IdP group) | Groups are frozen per-workspace at creation; the sweeper/reconciler re-evaluate with stored identity. Residual risk until OIDC login refresh lands — documented, and an admin can edit `users.groups` + pause offending workspaces today. |

Residual gaps (assumed): identity annotations are not re-synced when a
user's groups change (next OIDC login will fix); `WAAS_POLICY_BYPASS`
holders are fully exempt — keep it to the GitOps SA and break-glass.
