# Fable 5 Prompt — Feature 13 v2, part A (waas-fable): published image catalog (`WorkspaceImage.status.catalog`) and unified visual picker

Paste this document as-is as an implementation prompt. It assumes
that you (Fable 5) have no prior conversation context. **This document
fully replaces**
`docs/studies/17-prompt-feature13-direct-image-deploy-waas.md`, kept
archived for decision history — don't use it as a source, an
architect review changed the catalog design (§ Central architecture
decision below details what and why).

**This document is part A of a feature split into two independent
prompts** after architect review: feature 13 v2 originally covered
both the image catalog AND the creation of a workspace without an
admin-authored `WorkspaceTemplate` ("orphan"). These two needs have
very different blast radii — the catalog never touches
`enforce()`/provisioning, orphan creation does — and the namespace
placement question for an orphan workspace (no template = no custom
placement pattern) turned out to be a real architecture question in
its own right. Hence the split:
- **Part A (this document)**: the catalog itself — fetch, status,
  JSON schema, visibility, visual picker. Shippable and useful on its
  own, including for improving the EXISTING template picker (logos),
  never depending on part B.
- **Part B** (`docs/studies/28-prompt-feature13-direct-deploy-orphan-workspace.md`):
  orphan creation itself (`WorkspacePolicy.directDeploy`,
  `WorkspaceTemplate` synthesis, `CreateDirect` endpoint, webhook
  gate). **Depends on this part A** (consumes
  `status.catalog.entries` for its picker) — the reverse isn't true.

This prompt covers the `waas-fable` repo only — the `waas-images` part
(generating and publishing the two catalogs) remains a **separate,
independent prompt**. The two parts (repos) coordinate only through
the shared catalog file format, fully specified below (§ 1 Catalog
format) — unchanged from v1, you need nothing else from the other
repo.

## Context and goal

**Catalog approved images** (os/app/version/icon) to display a picker
with logos rather than a list of raw references — a periodic fetch of
a `catalog.yaml` file published by the registry, exposed on
`WorkspaceImage.status`.

**Registries in scope**: `ghcr.io/xorhub/waas-images` and
`docker.io/kasmweb` only (the two catalogs published by the
`waas-images` part). The mechanism stays generic (any `WorkspaceImage`
in registry mode can carry a `spec.catalog`), but nothing requires
documenting/promoting it beyond these two registries.

## Central architecture decision: the catalog lives INSIDE WorkspaceImage, not in a separate CR

**Deliberate departure from a first draft** which proposed a separate
`WorkspaceCatalog` CR referenced by `WorkspaceImage.spec.catalogRef`.
Decision reached after review: `WorkspaceCatalog` would always have
been in a strict 1:1 cardinality with a `WorkspaceImage` (never
multi-registry aggregation, never a curated view distinct from
approval — both needs were explicitly ruled out), and its only real
benefit (someday allowing a "catalog editor" role distinct from the
security admin, via K8s RBAC scoped to a different resource) doesn't
hold up: the platform's entire authorization model is already
**application-level, not native K8s RBAC** (`WorkspacePolicy` itself
is not enforced by any RBAC verb on `Workspace`, only by the
webhook/api-server that resolves a policy). A future "catalog editor"
role would in any case be checked in the api-server's code before the
`PATCH spec.catalog`, exactly like the rest of governance — a separate
CR would have brought nothing to this future role, just duplicated an
object for a cardinality that remains 1:1 by construction.

**Practical consequence**: if a real need for K8s RBAC separation ever
appears (fine-grained direct kubectl/GitOps access), the split remains
possible later — grouping the fields under a dedicated struct now
(instead of flat fields) makes that future split mechanical. That's
why `Catalog` is a nested struct and not flat fields on
`WorkspaceImageSpec`/`WorkspaceImageStatus`.

`operator/api/v1alpha1/workspaceimage_types.go` — on
`WorkspaceImageSpec`, after `Resources`:

```go
// Catalog configures the periodic fetch of a published catalog
// manifest (format below) listing the images currently under this
// entry's registry, with display metadata (os/app/version/icon) for
// the portal catalog picker. Only meaningful when spec.registry is
// set (ignored on exact spec.image entries — ENFORCEMENT never reads
// this field, it is purely cosmetic). Grouped under one struct
// (rather than flat fields) so a future split into its own CRD, if a
// real need for K8s-RBAC-level separation ever appears, is a
// mechanical lift instead of a field-by-field migration — no such
// split is planned today, application-level authorization (the same
// model WorkspacePolicy already uses) covers any future need for a
// narrower "catalog editor" role. Absent = no automatic catalog; the
// registry approval itself still works.
// +optional
Catalog *ImageCatalogSpec `json:"catalog,omitempty"`
```

```go
// ImageCatalogSpec points at exactly one catalog manifest source.
type ImageCatalogSpec struct {
	// From is the catalog manifest source (format below, § 1) —
	// exactly one of URL/ConfigMapKeyRef/SecretKeyRef, mutually
	// exclusive (enforced on ImageCatalogSource below; never more than
	// one set). URL is fetched live over HTTP(S); ConfigMapKeyRef/SecretKeyRef
	// are read directly, no HTTP involved. Both are re-checked on the
	// SAME periodic cadence (operator.catalogSyncInterval, § 7) — no
	// dedicated watch on the referenced ConfigMap/Secret (§ 4 explains
	// why) — a static, GitOps-managed catalog for an admin who prefers
	// not to depend on a live registry endpoint. This is a first-class,
	// permanent choice, not a stopgap-until-network-works: an admin
	// picks ONE of the three and stays on it.
	// +kubebuilder:validation:Required
	From ImageCatalogSource `json:"from"`

	// Auth configures how the live fetch authenticates — only
	// meaningful when From.URL is set (ignored, and rejected at
	// admission if From points at ConfigMapKeyRef/SecretKeyRef instead,
	// see the XValidation on ImageCatalogSpec below the struct).
	// Nested by method (one field per auth kind) instead of a flat
	// credential reference, so a future method (basic auth, mTLS...) is
	// a pure ADDITION — a new sibling field on ImageCatalogAuth — never
	// a rename or a reinterpretation of what an existing field means.
	// Absent = unauthenticated GET, the only mode the two known public
	// catalogs (ghcr.io/xorhub/waas-images, docker.io/kasmweb) need.
	// +optional
	Auth *ImageCatalogAuth `json:"auth,omitempty"`
}
```

`+kubebuilder:validation:XValidation:rule="!has(self.auth) || self.from.url != ''",message="auth is only meaningful when from.url is set"`
on `ImageCatalogSpec` — couples `Auth` to the URL variant explicitly instead of
silently ignoring it (fail-soft doctrine covers runtime data issues, not a
config that can never do anything).

```go
// ImageCatalogSource names the catalog manifest source — exactly one
// of URL/ConfigMapKeyRef/SecretKeyRef must be set.
// +kubebuilder:validation:XValidation:rule="(has(self.url) ? 1 : 0) + (has(self.configMapKeyRef) ? 1 : 0) + (has(self.secretKeyRef) ? 1 : 0) == 1",message="exactly one of url, configMapKeyRef, or secretKeyRef must be set"
type ImageCatalogSource struct {
	// URL is the catalog manifest location, fetched live and
	// periodically (§ 7 interval).
	// +optional
	URL string `json:"url,omitempty"`

	// ConfigMapKeyRef reads the manifest from a ConfigMap key in the
	// platform workspace namespace instead of fetching it over HTTP —
	// the common case, since the content isn't secret, just a static
	// admin-provided catalog. Key defaults to "catalog.yaml" when
	// empty. Re-read periodically (operator.catalogSyncInterval, § 7),
	// not just once — no dedicated watch on the ConfigMap (§ 4 explains
	// why).
	// +optional
	ConfigMapKeyRef *corev1.ConfigMapKeySelector `json:"configMapKeyRef,omitempty"`

	// SecretKeyRef reads the manifest from a Secret key instead, in the
	// platform workspace namespace — for an admin who wants the
	// manifest content itself access-controlled. Key is REQUIRED (no
	// default, unlike ConfigMapKeyRef): no naming convention is assumed
	// for a Secret. Distinct from ImageCatalogAuth.BearerToken below:
	// that one is a fetch CREDENTIAL for a URL source, this one IS the
	// manifest content itself; the two are never the same Secret in
	// practice but nothing in the schema prevents it, and they cannot
	// both apply at once since URL/SecretKeyRef are mutually exclusive.
	// +optional
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}
```

```go
// ImageCatalogAuth holds one authentication method for the catalog
// fetch. Only BearerToken exists today — deliberately no
// mutual-exclusion XValidation yet (a single optional field has
// nothing to conflict with; that CEL rule is dead code until a second
// method exists). Add the "exactly one of" rule THE DAY a second
// method is introduced, not before (YAGNI).
type ImageCatalogAuth struct {
	// BearerToken sends "Authorization: Bearer <token>" on the fetch.
	// +optional
	BearerToken *BearerTokenAuth `json:"bearerToken,omitempty"`
}

// BearerTokenAuth names the Secret holding the bearer token used to
// authenticate the catalog fetch.
type BearerTokenAuth struct {
	// SecretRef names an existing Opaque Secret (in the platform
	// workspace namespace, same convention as
	// WorkspaceImageSpec.ImagePullSecretRef) holding the token under
	// the key "token". A missing/unreadable Secret, or one without this
	// key, is a sync failure (status.catalog.lastSyncError), never a
	// crash — same fail-soft doctrine as the rest of the reconciler
	// (see § 4).
	// +kubebuilder:validation:MinLength=1
	SecretRef string `json:"secretRef"`
}
```

New status — `WorkspaceImage` has NO `Status` today
(grep `Status` on `workspaceimage_types.go`: zero results), so this is
a net addition, not an extension:

```go
// DiscoveredImage is one entry surfaced by a catalog sync — display
// metadata only, NEVER consulted by policy/enforcement (that stays
// FindImage/ImageAllowed against spec.image/spec.registry, unchanged).
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
	// a LOCALLY VENDORED subset (see § 6) — never fetched live from
	// GitHub — falling back to an OS icon when absent or unknown.
	// +optional
	Icon string `json:"icon,omitempty"`
	// +optional
	DisplayName string `json:"displayName,omitempty"`
}

// ImageCatalogStatus is the last known result of the catalog fetch
// configured by spec.catalog.
type ImageCatalogStatus struct {
	// Entries is the last known set of discovered images. Stale-but-served:
	// a failed sync never clears it.
	// +optional
	Entries []DiscoveredImage `json:"entries,omitempty"`
	// Source says which From variant produced Entries: "Fetched"
	// (From.URL, a live sync succeeded at least once) or "Static"
	// (From.ConfigMapKeyRef/SecretKeyRef was read successfully at least
	// once). Empty = never synced yet.
	// +optional
	Source string `json:"source,omitempty"`
	// LastSyncTime is when Entries was last written: the real fetch
	// time for "Fetched", the time the ConfigMap/Secret was last read
	// for "Static".
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
	// LastSyncError is the most recent fetch/read failure, kept even
	// after a later sync succeeds, so admins can see WHY it once
	// failed.
	// +optional
	LastSyncError string `json:"lastSyncError,omitempty"`
}

type WorkspaceImageStatus struct {
	// Catalog is nil until the first sync attempt of a spec.catalog-configured
	// entry.
	// +optional
	Catalog *ImageCatalogStatus `json:"catalog,omitempty"`
}
```

Add the `Status` field to the `WorkspaceImage` root struct (today it
only has `Spec`) and `+kubebuilder:subresource:status` to its root
marker block:

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wsi
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Registry",type=string,JSONPath=`.spec.registry`
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Catalog",type=string,JSONPath=`.status.catalog.source`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type WorkspaceImage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkspaceImageSpec   `json:"spec"`
	Status WorkspaceImageStatus `json:"status,omitempty"`
}
```

This is additive and backward-compatible per
[ADR 0002](../adr/0002-crd-evolution.md) — but before adding the
subresource, grep `WorkspaceImage{}.Status` and `.Status =` across
`operator/` and `api-server/` to confirm no existing code assumes the
absence of status on this type (expected: nothing, the object is
purely read by the webhook today).

### Why this merge is safe with respect to the existing watch on WorkspaceImage

`workspace_controller.go` ALSO has a
`Watches(&waasv1alpha1.WorkspaceImage{}, mapCatalogToWorkspaces,
predicate.GenerationChangedPredicate{})` — editing a catalog entry
(arch affinity, pull secret) → drift re-evaluation of the whole
namespace fleet. Once `+kubebuilder:subresource:status` is added,
writes to `status.catalog.*` go through the `Status().Patch()`
subresource and **NEVER bump `metadata.generation`** — so the periodic
catalog sync (every 6h by default) never triggers this watch. Only a
human edit of `spec.catalog.from`/`auth` bumps the generation and
retriggers `mapCatalogToWorkspaces` for the whole fleet — behavior
already accepted today for any `WorkspaceImage.spec` edit ("catalog
edits are rare admin operations and an in-sync reconcile is a cheap
no-op", existing comment). Nothing new to mitigate here.

The two `Watches(&WorkspaceImage{}, ...)` (the one in
`workspace_controller.go` for drift, and the new reconciler from § 4
for the catalog fetch) are two independent controllers observing the
same GVK for different reasons — normal controller-runtime pattern, no
conflict.

## What needs to be delivered

### 1. Catalog format (contract shared with the waas-images repo) — unchanged

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

Tolerant parser: unknown `apiVersion` ⇒ rejects cleanly (a sync error,
NOT a crash), absent optional fields ⇒ zero value (empty `os` is
treated as `linux` on the frontend side, not an error).

### 2. Versioned catalog JSON schema — waas-fable is its sole source of truth

waas-fable is the **reader** of the catalog format (§1): so it is the
one that has authority over the schema, not waas-images (the
producer). The schema exists **ONLY in this repo** — never duplicated
or vendored on the waas-images side or elsewhere — and is referenced
from `catalog.yaml` files by an HTTPS URL pointing at this repo,
consumed by a yaml-language-server (e.g. the `redhat.vscode-yaml`
extension) for validation/autocompletion while editing:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/xorhub/waas-fable/<tag>/operator/pkg/catalog/schema/v1.schema.json
apiVersion: waas.xorhub.io/catalog/v1
images: ...
```

`<tag>` = a waas-fable release tag, never `main` — same discipline as
the rest of the project against moving references (no `:latest`).
This URL is only an editor convenience for anyone editing a
`catalog.yaml` by hand (on the waas-images side or elsewhere): the
HTTPS fetch happens in the editor of the person editing, never in the
waas-fable reconciler nor in a runtime path — `status.catalog` remains
fed only by the tolerant Go parser described above, independently of
this schema. A CI validation on the waas-images side (if that repo
wants to gate its publication on it) is a decision of its own, out of
scope for this prompt.

**The schema is not hand-written: it is generated from the canonical
Go struct**, so that it can never silently diverge from the actual
parser.

- `operator/pkg/catalog` (new package): Go struct of the wire format —
  `File{APIVersion string; Images []Entry}` — **distinct from
  `DiscoveredImage`** (§ Central architecture decision; the two types
  have different compatibility cadences, one follows the CRD
  `v1alpha1`/ADR 0002 cycle, the other an independent inter-repo file
  contract). This struct is the one the reconciler's parser (§4)
  actually `Unmarshal`s — a single definition, two consumers (parser +
  schema generator), so no synchronization fixture to maintain
  separately.
- `operator/hack/gen-catalog-schema/main.go`: small local command that
  imports `catalog.File` + a reflection-based JSON Schema generation
  library (e.g. `invopop/jsonschema`), writes
  `operator/pkg/catalog/schema/v1.schema.json`. Dependency added to
  `operator/go.mod` but never imported by `cmd/operator` — same
  pattern as `tygo` for `generate-types` (`Makefile:57`, invoked via
  `go run pkg@version`, no `go.mod` entry required for that one
  specifically, but same spirit of a generation tool unrelated to the
  shipped binary).
- One file per `apiVersion` (`v1.schema.json`), frozen once published —
  same additive discipline as the CRDs.
- **make/CI wiring reuses the existing mechanism, doesn't create a new
  one**:
  - Add the generation to the existing `generate` target
    (`Makefile:45-46`).
  - Extend the path list of the `go-generated-drift` job's
    `git diff --exit-code` (`.github/workflows/ci.yml:283-284`) to
    include `operator/pkg/catalog/schema` — exactly the mechanism that
    already gates CRD/TS-types drift, one more path in the same call,
    not a new job.

### 3. Catalog visibility: inherited, no new field

`status.catalog.entries` of a `WorkspaceImage` is only visible in the
`/catalog` API (§ 6) to users already authorized for THAT
`WorkspaceImage` by `policy.AllowedImages`/
`WorkspaceImage.spec.AllowedGroups` — the same gate as enforcement, no
separate visibility field on the catalog. Do NOT code a second
filtering mechanism.

### 4. `WorkspaceImageCatalogReconciler` — first WorkspaceImage reconciler

`WorkspaceImage` has NO reconciler today — this is the first one.
Suggested file: `operator/internal/controller/workspaceimage_catalog.go`,
model to follow: `namespace_janitor.go` for the shape of an
independent `Reconciler` + `workspace_controller.go` for the
self-requeue pattern (`ctrl.Result{RequeueAfter: interval}`).

Logic per `WorkspaceImage` with `spec.registry != "" && spec.catalog != nil`
— two branches that are **mutually exclusive** depending on
`spec.catalog.from` (exactly one of the three options, guaranteed by
the type's XValidation, § Central architecture decision):

**Branch A — `From.URL` set (live, periodic):**
1. Plain HTTP `GET` (`net/http`, no new dependency — NOT
   `crane`/`go-containerregistry`) on `from.url`, reasonable timeout
   (5-10s). If `spec.catalog.auth.bearerToken != nil`, read the Secret
   named by `auth.bearerToken.secretRef` in the platform namespace
   (uncached read, same doctrine as `pull_secret.go` — "Secret reads
   bypass the cache", existing RBAC comment
   `helm/waas/templates/operator.yaml:31-34`) and add
   `Authorization: Bearer <"token" key of the Secret>`. Missing/unreadable
   Secret, or one without the `token` key → sync failure
   (`lastSyncError`), never a crash.
2. Success + parse OK → parse the response into `catalog.File`/`catalog.Entry`
   (`operator/pkg/catalog`, § 2 — the same type as the schema
   generator, never ad-hoc parsing), then convert each `catalog.Entry`
   into a `DiscoveredImage` for `status.catalog.entries` (the two
   types are deliberately distinct, § 2 — this conversion is the only
   seam between them). `status.catalog.source = "Fetched"`,
   `status.catalog.lastSyncTime = now`, `status.catalog.lastSyncError = ""`.
3. Failure (network, non-200 HTTP, auth, parse) → **never clear an
   already-populated `status.catalog.entries`** (stale-but-served);
   `status.catalog.lastSyncError` updated, `status.catalog.source`
   unchanged.

**Branch B — `From.ConfigMapKeyRef` or `From.SecretKeyRef` set
(static, GitOps-managed, § 5):**
1. Reads the referenced key (`ConfigMapKeyRef.Key`/`SecretKeyRef.Key`,
   default `catalog.yaml` only for `ConfigMapKeyRef` — required for
   `SecretKeyRef`) in the platform namespace. Uncached read for the
   Secret case (same doctrine as `auth.bearerToken.secretRef`; the
   operator SA already has `[get, list, ...]` on `configmaps` AND
   `secrets` with no resource restriction,
   `helm/waas/templates/operator.yaml:33-40` — no new RBAC). No HTTP
   call in this branch.
2. Success + parse OK (same `catalog.File`, § 2, never a special
   format) → `status.catalog.entries` = the converted content,
   `status.catalog.source = "Static"`, `status.catalog.lastSyncTime = now`,
   `status.catalog.lastSyncError = ""`.
3. Failure (missing object, absent key, content that fails to parse) →
   **never clear an already-populated `status.catalog.entries`**
   (stale-but-served, same doctrine as branch A);
   `status.catalog.lastSyncError` updated, never a crash.

**Common to both branches:**
4. Requeue after `RequeueAfter: interval` (success or failure, both
   branches alike — branch B re-reads at the same cadence as branch A
   re-fetches, no separate immediate-refresh mechanism, see why right
   below). `interval` comes from an operator env var (§ 7).
5. Watch on `WorkspaceImage` specific to this reconciler: an edit to
   `spec.catalog` (including a branch A→B change or the reverse)
   immediately retriggers (default behavior of a
   `For(&WorkspaceImage{})` — no extra predicate needed, this
   reconciler itself filters out entries without `spec.catalog` in
   `Reconcile`).

**No watch on the ConfigMap/Secret referenced by branch B** — that
would have been the idiomatic controller-runtime mechanics for an
immediate refresh (GitOps edit → instant reconciliation instead of
waiting for `interval`), but `helm/waas/templates/operator.yaml:32,36`
explicitly documents that this operator's Secret/ConfigMap reads
**bypass the cache** ("Secret reads bypass the cache... no watch",
"Uncached reads too") — an architectural choice already in place,
RBAC without the `watch` verb on these two resources. A
`Watches(&corev1.ConfigMap{}, ...)` would go against this choice (RBAC
to extend, cache to enable for these GVKs). Branch B therefore accepts
the same propagation delay as branch A (up to
`catalogSyncInterval`, § 7) — consistent rather than a special case,
and without reopening an architecture decision already settled
elsewhere in this repo.

This reconciler NEVER blocks workspace creation: separate, purely
cosmetic, `enforce()`/`FindImage` never read it.

### 5. Catalog source: directly fetched URL OR static content — never a snapshot embedded in the binary

**Deliberate departure from two earlier drafts.** The very first
proposed embedding two frozen files via `//go:embed` (precedent:
`kasmvnc_defaults.yaml`,
`operator/internal/controller/kasm_config.go`), hard-mapped by exact
`spec.registry` (`ghcr.io/xorhub/waas-images` → one file,
`docker.io/kasmweb` → another) — ruled out: neither generic (hard
mapping limited to two registries, whereas ANY `WorkspaceImage` in
registry mode can carry a `spec.catalog`), nor GitOps-compliant
(refreshing the snapshot requires recompiling and publishing a new
operator version), nor as auditable for an admin as a directly
inspectable K8s object (`kubectl get configmap -o yaml` versus "go
read the operator's Go source"). A second draft then introduced
`ConfigMapKeyRef`/`SecretKeyRef` as a **fallback** used ONLY when the
live fetch had never succeeded (`url` remaining a separate, always
present field) — ruled out in turn: `url` and
`from.{configMapKeyRef,secretKeyRef}` could have coexisted at the same
time on the same object, with an implicit priority semantics (live
wins if it has already succeeded once) that would need to be
documented and tested separately, for a need that doesn't really exist
— an admin picks ONE source, not a source-with-a-plan-B.

**Solution adopted**: `ImageCatalogSpec.From` (§ Central architecture
decision) is an `ImageCatalogSource` struct with
`URL`/`ConfigMapKeyRef`/`SecretKeyRef` **mutually exclusive, three-way**
(exactly one of the three, never two, never zero — the type's
XValidation enforces it). No notion of "fallback" or priority among
the three: it's a source choice, a single point, not a hierarchy.
`URL` remains the periodic live fetch (§4 branch A). The
`ConfigMapKeyRef` case covers the admin who prefers a static,
GitOps-managed catalog (non-secret content, the common case);
`SecretKeyRef` exists for the admin who wants to restrict access to
the manifest content itself — **unrelated** to
`ImageCatalogSpec.Auth.BearerToken` (the live-fetch credential, only
relevant with `From.URL`, see the XValidation that couples them): the
two serve distinct roles and can point at different Secrets. Generic
(any `WorkspaceImage` in registry mode chooses its own source, not
just the two known registries), GitOps-managed for both static
variants (the ConfigMap/Secret is updated by a commit, like
`gitops/governance/images.yaml`, and the reconciler re-reads it at the
next `catalogSyncInterval` — no dedicated watch on these two
resources, §4 explains why), and zero new RBAC (the operator SA
already has
`[create, delete, get, list, update]` on `configmaps` AND `secrets`
with no resource restriction, `helm/waas/templates/operator.yaml:33-40`).

For the two known public registries (`ghcr.io/xorhub/waas-images`,
`docker.io/kasmweb`), provide an **example** ConfigMap manifest
(`ConfigMapKeyRef` variant, since the content isn't secret) under
`gitops/governance/examples/` (not applied by default — the
`spec.registry`+`spec.catalog` mode is itself already opt-in,
`gitops/governance/images.yaml` uses none of it today, all its entries
are exact `spec.image`): an admin who enables catalog mode for one of
these registries chooses their own source (`url` toward the public
registry, or copy/adapt the example ConfigMap for an airgap use case)
rather than depending on an imposed default.

### 6. Frontend — API exposure + unified visual picker

- `GET /catalog` (`Governance.Catalog`, api-server): adds the
  `WorkspaceImage.status.catalog.entries` entries of authorized
  catalog images (reuses `policy.AllowedImages` for filtering — same
  gate as everything else, § 3). **Nested shape**:
  `CatalogImage.discovered?: DiscoveredImage[]` (new field on the
  existing `CatalogImage` type, `frontend/src/types.gen.ts:435-464`) —
  not a separate array at the root of the response. Each picker card
  (§ below) needs both the discovered entry (icon/version/app) AND the
  bounds/protocols of the parent `CatalogImage`
  (`protocols`/`min`/`max`/`defaults`, already present on this type
  today) — nesting avoids any manual re-correlation on the frontend
  side between two lists.
- New icon resolution component,
  `frontend/src/lib/icon.ts`: `resolveIcon(slug?: string, os?: string): string`
  — returns the path of a locally vendored asset
  (`/icons/<slug>.svg`), falling back to `/icons/os-<linux|windows>.svg`
  if `slug` is absent or not vendored. **No network fetch at
  runtime**.
- **Unified card component**, used both for the list of EXISTING
  templates and for catalog entries (not two distinct renders): every
  template has at least a known OS (`WorkspaceTemplate.spec.os`), so
  `resolveIcon(undefined, tpl.os)` already returns a coherent fallback
  logo for a template without a catalog icon — this is what allows,
  starting with THIS part, replacing the current text `<select>` of
  `CreateWorkspaceDialog.tsx` (L267-286) with a grid of cards with
  logos, without waiting for part B. Reused by
  `SessionCard`/`WorkspaceCard`
  (`frontend/src/components/SessionCard.tsx`,
  `frontend/src/sections/WorkspacesSection.tsx`) for the same visual
  consistency. This same component is the building block for part B's
  picker (which adds "from catalog" cards next to template cards in
  the same grid) — keep the component generic enough (icon + title +
  subtitle + disabled/reason state) that part B only has to feed it
  data, never rewrite it.
- Icon vendoring: `dashboard-icons`
  (github.com/homarr-labs/dashboard-icons, Apache-2.0, `svg/<slug>.svg`
  structure) — manual/scripted copy of a subset (slugs referenced by
  the two known catalogs + `linux`/`windows` as fallback) into
  `frontend/public/icons/`, with
  `frontend/public/icons/ATTRIBUTION.md` citing the Apache-2.0 license
  and the source repo. `hack/vendor-icons.sh` (sophistication is
  optional — a hardcoded slug list is enough, this is not a runtime
  execution path) downloads from
  `https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/<slug>.svg`
  to ease future refreshes, but is NOT executed at app runtime.

### 7. Helm — sync interval, status RBAC

`helm/waas/values.yaml`: new key `operator.catalogSyncInterval`
(default `6h`, same spirit as `apiServer.eventsPollInterval`). Wired
into an operator env var.

RBAC (`helm/waas/templates/operator.yaml`):
- L67-69, `workspacetemplates, workspaceimages, workspacepolicies`
  only have `[get, list, watch]` today. Add a separate
  `workspaceimages/status` entry with `verbs: [get, update, patch]`
  (mirroring the `workspaces/status` block right below) — regenerate
  with `make manifests` once the `+kubebuilder:subresource:status`
  marker is in place, verify the generated YAML matches.
- `secrets` already has `[create, delete, get, list, update]` with no
  resource restriction (L33-34) — covers reading
  `spec.catalog.auth.bearerToken.secretRef` and
  `spec.catalog.from.secretKeyRef`, no addition needed. Same for
  `configmaps` (L38-40, same verbs) — covers
  `spec.catalog.from.configMapKeyRef`. Neither has (nor should gain)
  the `watch` verb: these two resources deliberately bypass the
  operator's cache (existing comments L32/L36) — see §4 for why this
  choice stays unchanged here.

### 8. Regeneration

`make manifests generate docs-params generate-types` — CRD YAML
(`helm/waas/crds/waas.xorhub.io_workspaceimages.yaml`), generated TS
types (`frontend/src/types.gen.ts`), and, from `generate` (now
included), `operator/pkg/catalog/schema/v1.schema.json` (§ 2).

## Constraints

- [ADR 0002](../adr/0002-crd-evolution.md): additive only, no field
  renamed/retyped, `v1alpha1` stays `v1alpha1`.
- `status.catalog` is NEVER read by `enforce()`/`FindImage`/`ImageAllowed` —
  cosmetic only.
- The catalog fetch must NEVER be able to block or delay the
  creation/use of a workspace — separate reconciler, not a synchronous
  call inside `enforce()` or `buildPodTemplate`.
- No new Go dependency for the fetch (no
  `go-containerregistry`/`crane` — a plain HTTP `GET` suffices). Does
  NOT apply to the schema-generation dependency (§ 2,
  `invopop/jsonschema` or equivalent): a `make generate` tool only,
  never imported by `cmd/operator`, so absent from the shipped binary
  and unrelated to the runtime fetch path this rule targets.
- No new `WorkspaceCatalog` CR — decision settled in § Central
  architecture decision, don't reintroduce it without going through an
  explicit architecture review.

## Tests

- `WorkspaceImageCatalogReconciler`, branch A (`From.URL`): fetch OK
  (with and without `auth.bearerToken`), fetch KO with `entries`
  already populated (stale-but-served), parsing an unknown
  `apiVersion` (clean rejection), auth with a missing Secret/no
  `token` key (sync failure, no crash — this case concerns
  `Auth.BearerToken.SecretRef`, not to be confused with branch B's
  `From.SecretKeyRef` in the test fixtures).
- `WorkspaceImageCatalogReconciler`, branch B (`From.ConfigMapKeyRef`/`SecretKeyRef`):
  read OK (both variants), read KO with `entries` already populated
  (stale-but-served), missing ConfigMap/Secret, absent key, malformed
  content (all fail-soft, never a crash). No test for an immediate
  refresh on external edit — deliberately absent (§4), branch B
  re-reads at the same cadence as branch A re-fetches.
- `ImageCatalogSource` XValidation: rejected at admission if zero or
  several of `url`/`configMapKeyRef`/`secretKeyRef` are set;
  `ImageCatalogSpec` XValidation: rejected if `auth` is set without
  `from.url`.
- Catalog schema (§ 2): `make generate` regenerates
  `operator/pkg/catalog/schema/v1.schema.json` with no diff — validated
  by the existing `go-generated-drift` job (path added to its
  `git diff --exit-code`), not a new test to write.
- Frontend: Vitest on `resolveIcon` (known slug, unknown slug, absent,
  OS fallback); the unified card component renders a template WITHOUT
  a catalog icon with the OS fallback, and a catalog entry with its
  own icon.
- `/verify` (repo skill) on the full flow if possible: `WorkspaceImage`
  with `spec.catalog.from.url` pointing at a local test file
  (`httptest.Server` for Go tests), verify that the portal picker
  correctly displays the discovered entries with logo.

## Open points (your call)

None at this stage — the point that remained open (how to add a
future catalog auth method without renaming `token`) is resolved
structurally by `ImageCatalogAuth` (§ Central architecture decision):
a future `basicAuth`/`mTLS`/etc. is added as a new field alongside
`bearerToken`, never a reinterpretation of an existing field.
