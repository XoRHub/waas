# Prompt Fable 5 — Feature 13 (waas-fable side): create a workspace directly from an approved registry, without a WorkspaceTemplate

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context. This prompt covers
the `waas-fable` repo only — the `waas-images` side (generating and
publishing the two catalogs) is a **separate, independent prompt**,
delivered in the other repo
(`docs/studies/prompt-feature13-catalog-publishing.md`). The two sides
coordinate only via a shared catalog file format, fully specified
below (§ Catalog format) — you don't need anything else from the other
repo to deliver this one: the reconciler can be developed and tested
against a hand-crafted catalog file that follows this format, even
before `waas-images` publishes anything at all.

## Context and goal

Today, a `Workspace` always references a `WorkspaceTemplate`
(`spec.templateRef`, mandatory), admin-authored — it's the only
provisioning path. The goal: let a user (if their `WorkspacePolicy`
authorizes it) create a workspace by directly picking an image from a
registry the admin has approved **in full**
(`WorkspaceImage.spec.registry`, already existing), without an admin
having had to create a template for that exact image beforehand. The
admin, meanwhile, needs no particular policy authorization to do the
same thing — an important nuance, see § Admin bypass below.

As a bonus, a periodic routine catalogs the images available in the
approved registries (os/app/version/icon) to display a logo next to
each image in the picker — reusable later by `WorkspaceTemplate`.

**Registries in scope for this feature**: `ghcr.io/xorhub/waas-images`
and `docker.io/kasmweb` only (the two catalogs published by the
`waas-images` side). Arbitrary private registries stay out of scope —
the mechanism is generic (any `WorkspaceImage` in registry mode can
carry a `catalogURL`), but nothing requires documenting/promoting it
beyond these two registries for now.

## Central architecture decision: synthesize a WorkspaceTemplate, don't bypass existing enforcement

**Do NOT create a new field on `Workspace.Spec`** (no
`imageRef`/`catalogImageRef`). `Workspace.Spec.TemplateRef` remains the
single provisioning pointer.

Reason: `enforce()` (`operator/internal/webhook/v1alpha1/workspace_webhook.go:354-424`),
`policy.LoadOf`/`policy.PlacementValues`/`policy.ResolvedDefaultNamespace`
(`operator/pkg/policy/policy.go`) and `buildPodTemplate`
(`operator/internal/controller/workload.go`) ALL take a real
`*WorkspaceTemplate` as a parameter and use it in depth (image, OS,
protocols, resources, placement, homeSize...). Duplicating this path
for a "templateless" provisioning would double the maintenance burden
and reopen exactly the divergence risk that `policy.OwnerLoads`
already documents having fixed once ("hand-copied loops whose
vanished-template fallbacks had silently diverged").

**Chosen solution**: when the user creates a workspace "from the
catalog", the api-server **synthesizes a private, single-use
`WorkspaceTemplate`**, 1:1 with the future workspace — `spec.image` =
the exact reference chosen, `spec.protocols`/`spec.resources` derived
from the user's choice bounded by the `WorkspaceImage` — then calls
the **existing, unchanged** workspace creation path with `templateRef`
pointing at this synthetic template. The whole enforcement/provisioning
chain (webhook, quotas, `buildPodTemplate`, placement) then applies
word for word, with no duplicated code.

The synthetic template is marked by a label (not a new schema field):
- `waas.xorhub.io/synthetic-template: "true"`
- `waas.xorhub.io/owner: <owner UUID>` (already-existing label,
  `operator/api/v1alpha1/identity.go:71`, reused as-is)

This marker serves two purposes: (1) the webhook requires additional
authorization (`WorkspacePolicy.spec.directDeploy`) only for a
template marked this way; (2) cleanup on workspace deletion (§ below)
knows which templates to delete.

## Admin bypass: no new code path

The project has an explicit, already-applied doctrine: "the bypass
stays a VISIBLE, auditable WorkspacePolicy CR — never a code path"
(`helm/waas/values.yaml`, `adminPolicy` block). **Do NOT code** a
special "admin role ⇒ direct deploy allowed" bypass in the webhook.
Instead:
- The new `WorkspacePolicy.spec.directDeploy` field (boolean, see
  below) is a policy field like any other.
- The bootstrap all-rights policy (`values.yaml` `adminPolicy` block,
  and `gitops/governance/policies.yaml`) must include
  `directDeploy: true` — it's the policy resolved for the admin that
  carries the right, exactly as it already carries the absence of
  limits and the full catalog.
- The admin otherwise keeps the existing generic bypass
  (`operator.policyBypass`, K8s groups such as `system:masters`) for
  direct kubectl access — unchanged, out of scope here.

Document this choice in the commit/PR: it is a deliberate clarification
against a naive reading of "the admin doesn't need any authorization"
— in this system, EVERYONE (admin included) goes through a resolved
`WorkspacePolicy`; "no particular authorization" translates to "the
admin policy grants it by default", not a code branch.

## What needs to be delivered

### 1. `WorkspacePolicy` — new `directDeploy` right

`operator/api/v1alpha1/workspacepolicy_types.go` — new field on
`WorkspacePolicySpec`, right after `RemoteWorkspaces` (same style,
same fail-closed default):

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
`RemoteWorkspacesAllowed` (L618-620):

```go
// DirectDeployAllowed reports whether the resolved policy opts its
// users into direct-from-catalog workspace creation. Nil policy = denied.
func DirectDeployAllowed(pol *waasv1alpha1.WorkspacePolicy) bool {
	return pol != nil && pol.Spec.DirectDeploy
}
```

New `Reason` in the `const` block (L61-79):
```go
ReasonDirectDeployNotAllowed Reason = "DirectDeployNotAllowed"
```

### 2. `WorkspaceImage` — fetched catalog, waas-images + kasm registries only

`operator/api/v1alpha1/workspaceimage_types.go` — on
`WorkspaceImageSpec`, after `Resources`:

```go
// CatalogURL points at a published catalog manifest (format below)
// listing the images currently under this entry's registry, with
// display metadata (os/app/version/icon) for the portal catalog
// picker. Only meaningful when spec.registry is set (ignored on exact
// spec.image entries — ENFORCEMENT never reads this field, it is
// purely cosmetic). Absent = no automatic catalog; the registry
// approval itself still works.
// +optional
CatalogURL string `json:"catalogURL,omitempty"`
```

New status:

```go
// DiscoveredImage is one entry surfaced by a registry-mode
// WorkspaceImage's catalog sync — display metadata only, NEVER
// consulted by policy/enforcement (that stays FindImage/ImageAllowed
// against spec.image/spec.registry, unchanged).
type DiscoveredImage struct {
	// Image is the exact, pinned reference (digest recommended).
	Image string `json:"image"`
	// +optional
	OS OSType `json:"os,omitempty"`
	// App is a logical grouping slug (e.g. "firefox", "ubuntu-xfce") —
	// distinct images of the same app across versions share it.
	// +optional
	App string `json:"app,omitempty"`
	// +optional
	Version string `json:"version,omitempty"`
	// Icon is a dashboard-icons (github.com/homarr-labs/dashboard-icons,
	// Apache-2.0) slug, e.g. "firefox". The frontend resolves it against
	// a LOCALLY VENDORED subset (see § 7) — never fetched live from
	// GitHub — falling back to an OS icon when absent or unknown.
	// +optional
	Icon string `json:"icon,omitempty"`
	// +optional
	DisplayName string `json:"displayName,omitempty"`
}

type WorkspaceImageStatus struct {
	// Catalog is the last known set of discovered images (registry-mode
	// entries only). Stale-but-served: a failed sync never clears it.
	// +optional
	Catalog []DiscoveredImage `json:"catalog,omitempty"`
	// CatalogSource says where Catalog came from: "Fetched" (a live sync
	// succeeded, ever) or "Bundled" (seeded from the operator's embedded
	// snapshot because no fetch has EVER succeeded — airgap day-0 case).
	// Empty = never synced and nothing bundled for this entry.
	// +optional
	CatalogSource string `json:"catalogSource,omitempty"`
	// LastSyncTime is when Catalog was last written: the real sync time
	// for "Fetched", the operator's build date for "Bundled" (so an
	// admin can tell a permanently-airgapped catalog is stale-by-design).
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
	// LastSyncError is the most recent fetch failure, kept even after a
	// later fallback-to-bundled succeeds, so admins can see WHY.
	// +optional
	LastSyncError string `json:"lastSyncError,omitempty"`
}
```

Add `// +kubebuilder:subresource:status` to the `WorkspaceImage` root
marker (it doesn't have one today — check
`operator/api/v1alpha1/workspaceimage_types.go:146-153` before adding;
this is a genuinely additive, backward-compatible change per
[ADR 0002](../adr/0002-crd-evolution.md), but confirm no existing code
assumes status is absent/unused on this type before wiring the
subresource — grep `WorkspaceImage{}.Status` and `.Status =` across
`operator/` and `api-server/` first).

### 3. Catalog format (contract shared with the waas-images repo)

YAML file, with an explicit format version so a future change is never
silently misinterpreted:

```yaml
apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: ghcr.io/xorhub/waas-images/ubuntu-xfce:1.1.0@sha256:...
    os: linux
    app: ubuntu-xfce
    version: "1.1.0"
    icon: linux
    displayName: "Ubuntu 24.04 — XFCE"
  - image: ghcr.io/xorhub/waas-images/firefox:1.0.0@sha256:...
    os: linux
    app: firefox
    version: "1.0.0"
    icon: firefox
```

A tolerant parser: unknown `apiVersion` ⇒ rejects cleanly (a sync
error, NOT a crash), absent optional fields ⇒ zero value (empty `os`
is treated as `linux` on the frontend side, not an error).

### 4. `WorkspaceImageReconciler` — new controller

`WorkspaceImage` currently has NO reconciler at all (a passive object,
only read by the webhook/policy) — this is the first one. Suggested
file: `operator/internal/controller/workspaceimage_catalog.go`, model
to follow: `namespace_janitor.go` for the shape of a standalone
`Reconciler` + `workspace_controller.go` for the self-requeue pattern
(`ctrl.Result{RequeueAfter: interval}`).

Logic per `WorkspaceImage` where `spec.registry != "" && spec.catalogURL != ""`:
1. Simple HTTP `GET` (`net/http`, no new dependency — NOT
   `crane`/`go-containerregistry`, the catalog is a static file, not a
   registry scan) on `spec.catalogURL`, reasonable timeout (5-10s).
2. Success + parse OK → `status.catalog` = the parsed content,
   `status.catalogSource = "Fetched"`, `status.lastSyncTime = now`,
   `status.lastSyncError = ""`.
3. Failure (network, non-200 HTTP, parse) → **never clear an
   already-populated `status.catalog`** (stale-but-served);
   `status.lastSyncError` updated in all cases. If `status.catalog` is
   empty AND an embedded snapshot exists for this entry name (§ 5) →
   seed from the snapshot, `status.catalogSource = "Bundled"`.
4. Requeue after `RequeueAfter: interval` (success or failure — same
   cadence). `interval` comes from an operator env variable (see § 8).
5. Watch on `WorkspaceImage`: editing `spec.catalogURL` retriggers
   immediately (no need to wait for the next requeue).

This fetch NEVER blocks workspace creation: it's a separate, purely
cosmetic reconciler, `enforce()`/`FindImage` never read it.

### 5. Embedded fallback (airgap day-0)

Direct precedent to follow: `kasmvnc_defaults.yaml` embedded via
`//go:embed` (`operator/internal/controller/kasm_config.go` — read it
for the exact convention). New:
`operator/internal/catalog/embedded/` with two files in the § 3
format: `waas-images.yaml` and `kasmweb.yaml`, updated by hand at each
operator release (no automation here — it's a snapshot frozen at build
time, documented as such in a comment at the top of each file). The
reconciler must know how to associate a `WorkspaceImage` with ITS
embedded snapshot — the simplest is an exact-`spec.registry` mapping
(`ghcr.io/xorhub/waas-images` → `waas-images.yaml`,
`docker.io/kasmweb` → `kasmweb.yaml`) hard-coded in the reconciler; a
`WorkspaceImage` with another registry + `catalogURL` simply has no
fallback (behavior: stays empty until a fetch succeeds, which is
correct — the embedded fallback only makes sense for the two
registries the platform knows about by construction).

### 6. Webhook — new gate for synthetic template

`operator/internal/webhook/v1alpha1/workspace_webhook.go`, function
`enforce()` (L354): right before or after `policy.Resolve` (L370), add:

```go
if tpl.Labels["waas.xorhub.io/synthetic-template"] == "true" && !policy.DirectDeployAllowed(pol) {
	return warnings, &policy.Denial{Reason: policy.ReasonDirectDeployNotAllowed,
		Message: fmt.Sprintf("policy %q does not grant direct-from-catalog deployment", pol.Name)}
}
```

(Adjust the exact order depending on where `pol` is available —
`Resolve` must have run before this.) The rest of `enforce()` —
`FindImage`, `ImageAllowed`, `CheckTagDiscipline`, `CheckProtocol`,
`CheckOverrides`, `CheckLimits` — applies WITHOUT modification: that's
the whole benefit of the real synthetic template.

Add the label constant in `operator/api/v1alpha1/identity.go` (next to
`LabelOwner`, `LabelRetained`, L71/L80):
```go
LabelSyntheticTemplate = "waas.xorhub.io/synthetic-template"
```

### 7. api-server — template synthesis + new endpoint

New route, `api-server/internal/server/router.go` (next to
`/workspace-templates`, L126):

```go
r.Post("/workspaces/direct", h.Workspaces.CreateDirect)
```

Dedicated `CreateWorkspaceInput`-like struct (new type in
`api-server/internal/service/`), fields: `catalogImage string` (the
exact reference chosen, must appear in
`WorkspaceImage.status.catalog[].image` of an entry for which
`DirectDeployAllowed` + `policy.Images` allow it — the service MUST
re-verify both points on the api-server side too, in addition to the
webhook: a clear error message before even attempting creation,
consistent with how `WorkspaceService.Create` already validates
`templateRef` upstream, L184-196), `protocol string`,
`resources *corev1.ResourceRequirements`, `displayName string`.

Logic of the new `WorkspaceService.CreateDirect`:
1. Resolves the `WorkspaceImage` owning the chosen catalog entry (the
   one whose `status.catalog[].image == in.CatalogImage`).
2. Builds a `*waasv1alpha1.WorkspaceTemplate` in memory: `Name`
   generated (e.g. `direct-<short uuid>`, same discipline as the
   existing `generateWorkspaceName`), `Labels: {synthetic-template: "true", owner: ownerID}`,
   `Spec.OS` = that of the catalog entry (default `linux`),
   `Spec.Image` = `in.CatalogImage`, `Spec.Protocols` derived from
   `WorkspaceImage.Spec.Protocols` ∩ `in.Protocol`, `Spec.Resources` =
   `in.Resources` or the `WorkspaceImage`'s default.
   **`Spec.Overrides` stays nil**: this template is never reused or
   shared, override delegation makes no sense here — everything the
   user wants is written directly into the synthesized spec.
3. `s.kube.Create` this template (the api-server SA already has
   `create` on `workspacetemplates`,
   `helm/waas/templates/api-server.yaml:19` — no new RBAC).
4. Calls exactly the same path as `WorkspaceService.Create` (L172+)
   with `TemplateRef` = the generated name — factor rather than
   duplicate (extract the "from an already-resolved template" part of
   `Create` into an internal function shared by both entrypoints).
5. If `Workspace` creation fails AFTER the template has been created,
   delete the synthetic template before returning the error (no
   leftover on failure).

**Cleanup on deletion**: when a `Workspace` is deleted, if its
`templateRef` points at a template carrying
`labels[synthetic-template]=true`, also delete that template. Look at
`operator/internal/controller/workspace_teardown_test.go` for the
exact hook point (operator reconciler, not api-server — the workspace
can also be deleted directly via kubectl/GitOps, so the cleanup MUST
live on the operator side, not just in the API DELETE endpoint).

**Orphan audit**: `hack/audit-orphans.sh` today does not cover
`WorkspaceTemplate` (search `grep -n workspacetemplate` — zero
results, confirmed). Extend it: a `synthetic-template=true` template
with no living workspace referencing it is an orphan, to be detected
like the rest of the sweep. Don't touch the behavior for
admin-authored templates (never orphans by construction).

### 8. Helm — sync interval, status RBAC

`helm/waas/values.yaml`: new key `operator.catalogSyncInterval`
(default `6h`, same spirit as `apiServer.eventsPollInterval`). Wired
into an operator env variable, read by the new reconciler.

RBAC: `helm/waas/templates/operator.yaml:67` already has
`workspacetemplates, workspaceimages, workspacepolicies` — add
`workspaceimages/status` with `update;patch` if not already covered by
the generic verb (check what `+kubebuilder:rbac` generates once the
status subresource marker is added, regenerate with `make manifests`).

### 9. Frontend

- `GET /catalog` (`Governance.Catalog`, api-server): add
  `directDeployAllowed bool` (derived from `policy.DirectDeployAllowed`
  on the caller's resolved policy) + the
  `WorkspaceImage.status.catalog` entries of the allowed catalog
  images (reuse `policy.AllowedImages` for filtering — same gate as
  the rest).
- `frontend/src/dialogs/CreateWorkspaceDialog.tsx`: second mode "From
  a catalog", visible only if `directDeployAllowed`. Selecting a
  catalog entry (image+logo), then protocol + size within the
  `WorkspaceImage`'s bounds — same spirit as the existing template
  picker (L62, L156, L268-271), not a new design pattern.
- New icon resolution component, e.g.
  `frontend/src/lib/icon.ts`: `resolveIcon(slug?: string, os?: string): string`
  — returns the path of a locally vendored asset
  (`/icons/<slug>.svg`), falling back to `/icons/os-<linux|windows>.svg`
  if `slug` is absent or not vendored. **No network fetch at runtime**
  (same airgap reasoning as the catalog itself — a browser in an
  isolated environment can't hit an external CDN either). Used by
  `SessionCard`/`WorkspaceCard`
  (`frontend/src/components/SessionCard.tsx`,
  `frontend/src/sections/WorkspacesSection.tsx:70-182`) and the new
  picker.
- Icon vendoring: `dashboard-icons`
  (github.com/homarr-labs/dashboard-icons, Apache-2.0, `svg/<slug>.svg`
  structure, license compatible with plain copying + attribution
  notice) — manual/scripted copy of a subset (only the slugs
  referenced by the two known catalogs + `linux`/`windows` as
  fallback) into `frontend/public/icons/`, with a
  `frontend/public/icons/ATTRIBUTION.md` file citing the Apache-2.0
  license and source repo. Refreshed occasionally by hand (no
  automatic sync pipeline in scope for this feature) — add a short
  script `hack/vendor-icons.sh` that takes a list of slugs and
  downloads them from the jsDelivr CDN
  (`https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/<slug>.svg`)
  to make future refreshes easier, but do NOT run it at app runtime.

### 10. Regeneration

`make manifests generate docs-params generate-types` — CRD YAML
(`helm/waas/crds/waas.xorhub.io_workspaceimages.yaml`,
`waas.xorhub.io_workspacepolicies.yaml`), generated TS types
(`frontend/src/types.gen.ts`), parameter docs if the protocol registry
is touched (probably not here).

## Constraints

- [ADR 0002](../adr/0002-crd-evolution.md): additive only, no
  renamed/retyped field, `v1alpha1` stays `v1alpha1`.
- Don't modify ANY line of `enforce()` beyond the § 6 gate —
  `FindImage`/`ImageAllowed`/`CheckTagDiscipline`/`CheckProtocol`/
  `CheckOverrides`/`CheckLimits` must remain single-path, shared
  functions, unchanged in their signature.
- No code bypass for the admin (§ Admin bypass) — only the bootstrap
  policy changes.
- The catalog fetch must NEVER be able to block or delay creating/using
  a workspace — it's a separate reconciler, not a synchronous call in
  `enforce()` or `buildPodTemplate`.
- No new Go dependency for the fetch (no
  `go-containerregistry`/`crane` — an HTTP `GET` is enough, the
  catalog is a static file published by CI, not a registry to scan).

## Tests

- `operator/pkg/policy`: unit tests for `DirectDeployAllowed` (nil,
  false, true).
- `operator/internal/webhook/v1alpha1`: envtest cases — synthetic
  template + policy without `directDeploy` ⇒ deny
  `DirectDeployNotAllowed`; with `directDeploy: true` ⇒ passes the
  usual gates normally (catalog/quota still apply).
- New reconciler: successful fetch, failed fetch with an
  already-populated catalog (stale-but-served), failed fetch day-0
  with/without embedded snapshot (bundled vs. empty), parsing an
  unknown `apiVersion` (clean rejection).
- api-server: `CreateDirect` — image outside the allowed catalog ⇒ 400
  before any CR creation; policy without `directDeploy` ⇒ 403; success
  ⇒ template + workspace created, template deleted if workspace
  creation fails afterward.
- Deletion: a deleted direct workspace ⇒ its synthetic template
  disappears (operator test, not just api-server, to cover the
  direct kubectl/GitOps path).
- `hack/audit-orphans.sh`: orphaned synthetic template case detected.
- Frontend: Vitest on `resolveIcon` (known slug, unknown slug, absent,
  OS fallback); test of the new dialog mode hidden/shown depending on
  `directDeployAllowed`.
- `/verify` (repo skill) on the full journey if possible: policy
  `directDeploy: true` + registry `WorkspaceImage` with `catalogURL`
  pointing at a local test file (e.g. served by a small HTTP server in
  the test, or a `file://` file if the reconciler supports it —
  otherwise an `httptest.Server` is enough for Go tests, manual dev
  verification can point at a temporarily hosted file).

## Open points (your judgment call)

- Exact route/endpoint name (`/workspaces/direct` proposed, free to
  change if a clearer name emerges once the code is in front of you).
- Should `WorkspaceImage.status.catalogSource`/`lastSyncError` be
  exposed in `kubectl get workspaceimage -o wide` via a printcolumn?
  Useful for admin debugging, not strictly necessary — your call.
- The `hack/vendor-icons.sh` script: sophistication level is free
  (hard-coded slug list vs. CLI argument) — it's not a runtime
  execution path, the bar is low.
