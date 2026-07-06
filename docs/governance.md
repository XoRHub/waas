# Workspace governance — catalog, policies, enforcement

Admin-defined guardrails for self-service workspaces: the admin approves
**images** (catalog) and assigns **limits** (policies) to Authentik
users/groups; users deploy freely inside that envelope, whether they go
through the portal or straight at the Kubernetes API.

## Data model

```
Authentik (OIDC groups)                      Workspace CR
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
  image subset, limits (`maxWorkspaces`, `perWorkspace`, `aggregate`,
  `defaults`), lifecycle (`idleSuspendAfter`, `maxLifetime`).
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
1000+ per-user exceptions.

## Identity binding (trusted-writer model, validated decision)

- Portal path: the api-server authenticates against Authentik, then
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
    available as break-glass.
  - **Admin editing** (always available): the Users page or
    PATCH `/api/v1/users/{id}` with `groups` — the only path when OIDC
    is not configured.
  A user whose mirror is empty matches only subjects-less policies:
  that is the `default`-policy-for-everyone symptom, not a priority
  bug.

### Clipboard policy

`spec.clipboard` gates the two clipboard directions independently
(`copyFromWorkspace`, `pasteToWorkspace`; absent = allowed). The
api-server resolves the CONNECTING user's policy at session start and
stamps the grant into the connection token; the wwt proxy enforces it by
filtering `clipboard` streams in both directions and clamps the
overlay's live toggles to the grant. No policy match = both directions
denied (fail closed) while the session itself still opens.

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

**Add an image to the catalog**: build/push via `waas-images` → add a
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

## API (portal contract)

`GET /api/v1/catalog`, `GET /api/v1/me/quota`, and under admin:
`GET/PUT/DELETE /admin/images[/{name}]`, `POST /admin/images/{name}/enable|disable`,
`GET/PUT/DELETE /admin/policies[/{name}]`, `GET /admin/usage`.
Full schema: `docs/openapi-governance.yaml`.

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
| Stale groups (user removed from a Authentik group) | Groups are frozen per-workspace at creation; the sweeper/reconciler re-evaluate with stored identity. Residual risk until OIDC login refresh lands — documented, and an admin can edit `users.groups` + pause offending workspaces today. |

Residual gaps (assumed): identity annotations are not re-synced when a
user's groups change (next OIDC login will fix); `WAAS_POLICY_BYPASS`
holders are fully exempt — keep it to the GitOps SA and break-glass.
