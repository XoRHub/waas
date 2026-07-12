# Fable 5 Prompt — Feature 13 v2, part B (waas-fable): creating a workspace without a WorkspaceTemplate ("orphan")

Paste this document as-is as an implementation prompt. It assumes
that you (Fable 5) have no prior conversation context.

**This document is part B of a feature split into two prompts**
(architect review, see also
`docs/studies/26-prompt-feature13-direct-image-deploy-waas-v2.md`,
part A — image catalog). **This part B depends on part A**: it
consumes `WorkspaceImage.status.catalog.entries` (the nested
`CatalogImage.discovered` field, exposed by `GET /catalog`) for its
picker. Implement part A first if not already done.

**Architecture point to settle BEFORE any implementation** (§ Open
architecture below): the namespace placement of a workspace created
through this path. Don't start coding without having decided this
point — it potentially touches `operator/pkg/policy/policy.go` and the
`WorkspaceTemplate`/`WorkspaceImage` contract beyond the scope of this
single feature.

## Context and goal

A user (if their `WorkspacePolicy` allows it) directly picks an image
from an approved registry (`WorkspaceImage.spec.registry`, cataloged
by part A), without an admin having had to create a
`WorkspaceTemplate` for that specific image. The admin can do the same
thing without needing a particular policy grant (nuance, see § Admin
bypass).

## Open architecture: namespace placement for a templateless workspace

**This point is not yet settled — to be decided before implementing.**

Today, placement (which namespace a workspace lands in) follows the
precedence "template pattern > global operator pattern > built-in"
(`policy.ResolvedDefaultNamespace`, `operator/pkg/policy/policy.go:536-541`):

```go
func ResolvedDefaultNamespace(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, id Identity, globalPattern string) (string, error) {
	pattern := naming.EffectivePattern(tpl.Spec.PlacementNamespacePattern(), globalPattern)
	return naming.ResolveNamespace(pattern, PlacementValues(ws, tpl, id))
}
```

`PlacementValues` (`policy.go:527-534`) and this function ONLY read
the template (`tpl.Spec.PlacementNamespacePattern()`, `tpl.Name`,
`tpl.Spec.OS`) — no notion of `WorkspaceImage` enters this computation
anywhere today. `Placement *WorkspacePlacement` lives on
`WorkspaceTemplateSpec` (`operator/api/v1alpha1/workspacetemplate_types.go:389-394`),
not on `WorkspaceImageSpec`.

This part's synthetic template (§ Central architecture decision #1)
never sets a custom `Spec.Placement` — so for an "orphan" workspace,
the precedence always collapses to "global pattern > built-in",
silently skipping the first tier. Two possible approaches, to be
decided:

**Option 1 — accept the asymmetry, just document it.** The orphan
workspace simply never has a custom placement pattern (no worse than
an admin-authored template that also doesn't define one). Cost:
`GET /workspaces/namespace-preview` (used by `useNamespacePreview`,
`frontend/src/hooks/useApi.ts:337-347`, "template > global > built-in
precedence, resolved SERVER-SIDE, never client-side") is today wired
to a mandatory `templateRef` — but in orphan mode, no template exists
before creation (synthesized after submission, § api-server). This
endpoint would need to be extended, at minimum, to accept a call
without a template (or with an `?image=X` parameter), directly
returning the global/built-in tier — otherwise the namespace preview
AND the attachment of an existing retained volume (`attachableVolumes`,
filtered on the previewed namespace, `CreateWorkspaceDialog.tsx:92-94`)
silently disappear for this mode, an undocumented transparency/feature
regression if not handled explicitly.

**Option 2 — give the image a real structural anchor point.** Add
`WorkspaceTemplateSpec.WorkspaceImageRef *corev1.LocalObjectReference`
(name of a `WorkspaceImage`), **mutually exclusive with
`WorkspaceTemplateSpec.Image`** (same style of XValidation that
`WorkspaceImageSpec` already has for `image`/`registry` — "exactly one
of image or registry must be set"). This part's synthetic template
would set `WorkspaceImageRef` instead of copying the exact reference
into `Spec.Image`. If `WorkspaceImageSpec` in turn gains an optional
`Placement`, the precedence widens to 4 tiers (template > **image
(new)** > global > built-in), and `PlacementValues`/`ResolvedDefaultNamespace`
must resolve the referenced `WorkspaceImage` (new dependency: these
functions, pure over the template alone today, would need a K8s
client or an already-loaded `img *WorkspaceImage` parameter —
`policy.LoadOf` already does this for resources, `L383`, a precedent
to follow). Benefit: a single placement-resolution path for all
workspaces (classic OR synthetic template), no special "orphan = no
placement preference possible" branch. Cost: touches `enforce()`,
`buildPodTemplate`, and every consumer of the current precedence — a
real blast radius, beyond the "create a workspace without a template"
scope alone.

**Starting recommendation (to be confirmed, not fixed)**: start with
Option 1 for this part — it's enough to ship the feature without
touching the `WorkspaceTemplate`/`WorkspaceImage` contract beyond what
this prompt already plans. Option 2 remains a possible later evolution
(preferring `WorkspaceImageRef` over a raw copy of `Spec.Image` in the
synthetic template does NOT prevent starting with Option 1 for
placement — the two questions, structural reference and 4-tier
placement, are separable). If Option 2 is chosen anyway, treat it as a
full-fledged architecture decision with its own review, not a subpoint
of this prompt.

## Central architecture decision #1: synthesize a WorkspaceTemplate, don't bypass existing enforcement

**Do NOT create a new field on `Workspace.Spec`** (no
`imageRef`/`catalogImageRef`). `Workspace.Spec.TemplateRef` remains the
sole provisioning pointer.

Reason: `enforce()` (`operator/internal/webhook/v1alpha1/workspace_webhook.go`,
function `enforce()`, `policy.Resolve` called at L370),
`policy.LoadOf`/`policy.PlacementValues`/`policy.ResolvedDefaultNamespace`
(`operator/pkg/policy/policy.go`), and `buildPodTemplate`
(`operator/internal/controller/workload.go`) ALL take a real
`*WorkspaceTemplate` as a parameter and use it in depth. Duplicating
this path for a "templateless" provisioning would double the
maintenance burden and reopen the divergence risk that
`policy.OwnerLoads` already documents having fixed once ("hand-copied
loops whose vanished-template fallbacks had silently diverged").

**Solution adopted (unchanged from v1)**: the api-server **synthesizes
a private, single-use `WorkspaceTemplate`**, 1:1 with the future
workspace — `spec.image` = the exact reference chosen (or
`spec.workspaceImageRef` if Option 2 above is chosen),
`spec.protocols`/`spec.resources` derived from the user's choice,
bounded by the `WorkspaceImage` — then calls the **existing,
unchanged** workspace creation path with `templateRef` pointing at this
synthetic template. The whole enforcement/provisioning chain (webhook,
quotas, `buildPodTemplate`, placement) then applies word for word.

The synthetic template is marked by a label (not a new schema field):
- `waas.xorhub.io/synthetic-template: "true"`
- `waas.xorhub.io/owner: <owner UUID>` (already existing label,
  `operator/api/v1alpha1/identity.go:71`, reused as-is)

Add the label constant in `operator/api/v1alpha1/identity.go` (next to
`LabelOwner`, `LabelRetained`):
```go
LabelSyntheticTemplate = "waas.xorhub.io/synthetic-template"
```

### Watch/informer mitigation — to ship with the label, not after

`operator/internal/controller/workspace_controller.go` already has a
`Watches(&waasv1alpha1.WorkspaceTemplate{}, mapTemplateToWorkspaces,
predicate.GenerationChangedPredicate{})` (function `SetupWithManager`)
for drift detection (`docs/adr/0001`): an admin edits a template, the
workspaces referencing it must receive the `TemplateDrifted` condition
without waiting for a periodic requeue.

A synthetic template is **immutable by construction** (never
re-edited, never shared — see § Constraints). Drift can therefore
structurally never occur on it: letting it go through this watch costs
one CREATE event + one DELETE event per direct-workspace lifecycle,
for zero benefit. Add a filter to the existing predicate (a
`predicate.NewPredicateFuncs` function or a `predicate.And` with a
check on
`obj.GetLabels()[waasv1alpha1.LabelSyntheticTemplate] != "true"`) so
that `mapTemplateToWorkspaces` isn't even invoked on these objects.
Don't touch the watch behavior for admin-authored templates.

## Admin bypass: no new code path

Project doctrine: "the bypass stays a VISIBLE, auditable
WorkspacePolicy CR — never a code path" (`helm/waas/values.yaml`,
`adminPolicy` block). **Do NOT code** a special "admin role ⇒ direct
deploy allowed" bypass in the webhook:
- New `WorkspacePolicy.spec.directDeploy` field (boolean), a policy
  field like any other.
- The bootstrap all-rights policy (`values.yaml` `adminPolicy` block,
  `gitops/governance/policies.yaml`) must include `directDeploy: true`.
- The admin keeps the existing generic bypass
  (`operator.policyBypass`) for direct kubectl access, unchanged.

## What needs to be delivered

### 1. `WorkspacePolicy` — new `directDeploy` right

`operator/api/v1alpha1/workspacepolicy_types.go` — new field on
`WorkspacePolicySpec`, right after `RemoteWorkspaces`:

```go
// DirectDeploy opts the governed users into creating a workspace
// directly from an admin-approved registry's catalog, without an
// admin-authored WorkspaceTemplate. Which images they may pick is
// still governed by the EXISTING Images/AllowedGroups gates — this
// field only lifts the "a template must exist" requirement. Absent or
// false = feature hidden and refused (fail closed), same convention
// as RemoteWorkspaces.
// +optional
DirectDeploy bool `json:"directDeploy,omitempty"`
```

`operator/pkg/policy/policy.go` — new function mirroring
`RemoteWorkspacesAllowed` (L618):

```go
// DirectDeployAllowed reports whether the resolved policy opts its
// users into direct-from-catalog workspace creation. Nil policy = denied.
func DirectDeployAllowed(pol *waasv1alpha1.WorkspacePolicy) bool {
	return pol != nil && pol.Spec.DirectDeploy
}
```

New `Reason` in the `const` block (L61):
```go
ReasonDirectDeployNotAllowed Reason = "DirectDeployNotAllowed"
```

### 2. Webhook — new gate for the synthetic template

`operator/internal/webhook/v1alpha1/workspace_webhook.go`, function
`enforce()`: right after `policy.Resolve` (L370, `pol` available),
add:

```go
if tpl.Labels[waasv1alpha1.LabelSyntheticTemplate] == "true" && !policy.DirectDeployAllowed(pol) {
	return warnings, &policy.Denial{Reason: policy.ReasonDirectDeployNotAllowed,
		Message: fmt.Sprintf("policy %q does not grant direct-from-catalog deployment", pol.Name)}
}
```

The rest of `enforce()` — `FindImage`, `ImageAllowed`,
`CheckTagDiscipline`, `CheckProtocol`, `CheckOverrides`, `CheckLimits`
— applies WITHOUT modification.

### 3. api-server — template synthesis + new endpoint

New route, `api-server/internal/server/router.go` (in the
`r.Route("/workspaces", ...)` block, next to the existing route at
L80, or mirroring the `/workspace-templates` block at L133):

```go
r.Post("/workspaces/direct", h.Workspaces.CreateDirect)
```

New type in `api-server/internal/service/`, fields:
`catalogImage string` (the exact reference chosen, must appear in
`WorkspaceImage.status.catalog.entries[].image` of an entry for which
`DirectDeployAllowed` + `policy.Images` allow it — the service MUST
re-check both points on the api-server side too, in addition to the
webhook, before even attempting creation — consistent with the
upstream validation already done for `templateRef` by
`WorkspaceService.Create`), `protocol string`,
`resources *corev1.ResourceRequirements`, `displayName string`.

`WorkspaceService.CreateDirect` logic:
1. Resolves the `WorkspaceImage` owning the chosen catalog entry (the
   one whose `status.catalog.entries[].image == in.CatalogImage`).
2. Builds a `*waasv1alpha1.WorkspaceTemplate` in memory: `Name`
   generated (e.g. `direct-<short uuid>`, same discipline as the
   existing `generateWorkspaceName`), `Labels: {synthetic-template: "true", owner: ownerID}`,
   `Spec.OS` = that of the catalog entry (default `linux`),
   `Spec.Image` = `in.CatalogImage`, `Spec.Protocols` derived from
   `WorkspaceImage.Spec.Protocols` ∩ `in.Protocol`, `Spec.Resources` =
   `in.Resources` or the `WorkspaceImage`'s default. **`Spec.Overrides`
   stays nil**: this template is never reused nor shared, override
   delegation makes no sense here.
3. `s.kube.Create` this template (the api-server SA already has
   `create` on `workspacetemplates`,
   `helm/waas/templates/api-server.yaml:19` — no new RBAC on the
   api-server side).
4. Calls exactly the same path as `WorkspaceService.Create` with
   `TemplateRef` = the generated name — factor out rather than
   duplicate.
5. If `Workspace` creation fails AFTER the template was created, delete
   the synthetic template before returning the error.

**Cleanup on deletion**: when a `Workspace` is deleted, if its
`templateRef` points at a template carrying
`labels[synthetic-template]=true`, delete that template too. Look at
`operator/internal/controller/workspace_teardown_test.go` for the
exact hook point (operator reconciler, not api-server — the workspace
can also be deleted via direct kubectl/GitOps). **Operator RBAC**:
`workspacetemplates` today only has `[get, list, watch]`
(`helm/waas/templates/operator.yaml:67-69`) — add `delete` for this
cleanup path.

**Why not an `ownerReference` instead of a hand-coded cleanup hook**:
the natural K8s idiom for this kind of cascade would be an
`ownerReference` from the synthetic `WorkspaceTemplate` to its
`Workspace` — native GC, free, covers every deletion path (api-server,
kubectl, GitOps) with no application code. **Don't do it**:
`WorkspaceTemplate` lives in `s.namespace` (platform namespace, e.g.
`waas` — `workspace_service.go`, every `client.ObjectKey{Namespace:
s.namespace, ...}` on `tpl`) whereas the `Workspace` is placed in
`EffectiveTargetNamespace()`, a namespace that differs depending on the
placement pattern. K8s `ownerReferences` don't work cross-namespace
(the GC silently ignores a reference to an object in another namespace
— no visible error, just an object that's never cleaned up). The
explicit hook above is therefore the ONLY viable option, not a style
choice — don't replace it with an `ownerReference` in a future review
without re-checking this point.

**Orphan audit**: `hack/audit-orphans.sh` today doesn't cover
`WorkspaceTemplate` (confirmed by grep: zero results). Extend it: a
`synthetic-template=true` template with no living workspace referencing
it is an orphan. Don't touch the behavior for admin-authored templates.

The script is today **manual, on-demand** (`hack/audit-orphans.sh
[--clean]`, no reference in `.github/workflows/`, `.gitlab/ci/`, or a
Helm `CronJob` — confirmed by grep, zero results) for ALL the orphan
categories it already covers (`managed-by waas-operator` objects with
no living workspace, emptied namespaces, retained volumes) — not just
the one this feature adds. An orphaned synthetic template (api-server
crash between creating the template and creating the workspace, § 3
pt. 5) therefore inherits the same latency window as everything else,
until the next manual run of the script. **Decision: do not introduce
a `CronJob` specific to this feature** — that would be inconsistent
with the existing manual posture for every other category, with
nothing specific to synthetic templates justifying separate handling
(an orphaned template exposes nothing more than an ordinary
admin-authored `WorkspaceTemplate` — same read RBAC, same non-secret
content: image ref, protocols, resources). If automating orphan sweeps
becomes a platform need, that's a separate effort touching
`audit-orphans.sh` as a whole, not a special case for this resource
type — don't anticipate it here.

**Exclusion from the existing picker — a necessary condition for
"never shared"**: `TemplateService.List` (`api-server/internal/service/template_service.go:99-109`)
today does a `s.kube.List` with no label selector in `s.namespace` —
returns EVERYTHING, including future synthetic templates. This list
feeds `GET /workspace-templates` (`router.go:134-135`), a route
accessible to any authenticated user, not just admins. Without a
filter, a synthetic template would appear in any user's "create from a
template" picker, and nothing would then prevent a second `Workspace`
from referencing it via `templateRef` — breaking the "immutable, never
re-edited, never shared" invariant stated above (§ Watch/informer
mitigation), which both the deletion cleanup above (assumes exactly 1
referencing workspace) and the repeated per-reconcile template read by
`buildPodTemplate` depend on (a second workspace would lose its
template if the first one is deleted). Add a label selector excluding
`waas.xorhub.io/synthetic-template=true` in `TemplateService.List` —
don't touch `TemplateService.Get` (used by the internal creation path,
which must keep being able to resolve a synthetic template by its
exact name).

### 4. Helm — delete RBAC

`helm/waas/templates/operator.yaml`: add `delete` to
`workspacetemplates` (§ 3, synthetic cleanup) — the only RBAC addition
in this part, the rest (status, secrets) is already covered by part A.

### 5. Frontend

- `GET /catalog` (`Governance.Catalog`, api-server): add
  `directDeployAllowed bool` (derived from `policy.DirectDeployAllowed`
  on the caller's resolved policy).
- `frontend/src/dialogs/CreateWorkspaceDialog.tsx`: **a single creation
  button, not two separate entries "from a template" / "from the
  catalog"**. Reuse part A's unified card component (§ 6 of that
  part): catalog entries (`CatalogImage.discovered`) get added as
  extra cards in the SAME grid as templates, visible only if
  `directDeployAllowed` — not a separate mode/toggle if the unified
  grid proves clear enough in practice (catalog card vs template card
  distinguished by a light icon indicating "no dedicated template"; to
  be validated by manual testing before generalizing). Selecting a
  catalog card shows protocol + size within the `WorkspaceImage`
  bounds, exactly as for a template (bounds already carried by
  `CatalogImage.min/max/defaults`, independently of any template — no
  new slider-computation logic needed, the existing `clampRange`
  applies as-is). Submission → `POST /workspaces/direct` (new
  `useCreateWorkspaceDirect` hook) instead of `POST /workspaces`.
- Namespace preview and retained-volume attachment in catalog mode:
  see § Open architecture — depends on the option chosen for
  placement.

## Constraints

- Do NOT modify any line of `enforce()` beyond the § 2 gate —
  `FindImage`/`ImageAllowed`/`CheckTagDiscipline`/`CheckProtocol`/
  `CheckOverrides`/`CheckLimits` remain single-path, shared functions,
  unchanged in signature.
- No code bypass for the admin (§ Admin bypass) — only the bootstrap
  policy changes.
- The synthetic template is immutable after creation: no code must
  ever `Update()` it once created (only deletion is a valid path).
- Resolving the `WorkspaceImage` governing an entry chosen in
  `CreateDirect` (§ 3 pt. 1) remains a match by image/registry, not
  blind trust in which entry `status.catalog` produced
  `in.CatalogImage`.

## Tests

- `operator/pkg/policy`: `DirectDeployAllowed` unit tests (nil, false,
  true).
- `operator/internal/webhook/v1alpha1`: envtest cases — synthetic
  template + policy without `directDeploy` ⇒ deny
  `DirectDeployNotAllowed`; with `directDeploy: true` ⇒ passes the
  usual gates normally.
- Filtering predicate on `Watches(&WorkspaceTemplate{})`:
  `mapTemplateToWorkspaces` not invoked for an object labeled
  `synthetic-template=true` (unit test of the predicate, no need for a
  full envtest).
- api-server: `CreateDirect` — image outside the catalog allowed ⇒ 400
  before any CR creation; policy without `directDeploy` ⇒ 403; success
  ⇒ template + workspace created, template deleted if workspace
  creation subsequently fails.
- Deletion: a direct workspace deleted ⇒ its synthetic template
  disappears (operator test, not just api-server, to cover the direct
  kubectl/GitOps path).
- `hack/audit-orphans.sh`: orphaned synthetic template case detected.
- Frontend: test that catalog cards appear in the unified grid only if
  `directDeployAllowed`, and that submission goes through
  `/workspaces/direct` for a catalog card.
- `/verify` (repo skill) on the full flow if possible: policy
  `directDeploy: true`, creating a workspace from a catalog card
  end-to-end.

## Open points (your call)

- **Namespace placement** (§ Open architecture): Option 1 (accept the
  asymmetry + extend `namespace-preview` without requiring a template)
  vs Option 2 (`WorkspaceImageRef` + `Placement` on `WorkspaceImage`,
  4-tier precedence). Starting recommendation: Option 1.
- Unified grid vs separate mode/toggle for the picker (§ 5 Frontend) —
  to be validated by manual testing, both are compatible with "a
  single creation button".
- Exact route name (`/workspaces/direct` proposed, free to change).
