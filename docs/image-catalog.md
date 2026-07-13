# Image catalog — published manifests, `WorkspaceImage.status.catalog`, visual picker

Registry-mode `WorkspaceImage` entries can surface a **published
catalog** of the images currently under their registry (display
metadata: os/app/version/icon), so the portal shows a picker of cards
with logos instead of raw references. The catalog is **purely
cosmetic**: `status.catalog` is never read by
`enforce()`/`FindImage`/`ImageAllowed` — approval and policy gating are
unchanged, and a catalog sync can never block or delay workspace
creation.

## Where the catalog lives: inside `WorkspaceImage`, not a separate CR

A separate `WorkspaceCatalog` CR was considered and rejected: it would
always have been 1:1 with a `WorkspaceImage` (no multi-registry
aggregation, no curated view distinct from approval), and its only
imagined benefit — a K8s-RBAC-separable "catalog editor" role — doesn't
match the platform's authorization model, which is **application-level,
not native K8s RBAC** (`WorkspacePolicy` itself is enforced by the
webhook/api-server, not by RBAC verbs). The fields are grouped under
nested structs (`spec.catalog`, `status.catalog`) rather than flat
fields so a future split, if a real RBAC need ever appears, stays
mechanical. Do not reintroduce a separate CR without an architecture
review.

## `spec.catalog` (`operator/api/v1alpha1/workspaceimage_types.go`)

- **`from`** — exactly one of three sources (CEL-enforced, never zero,
  never two):
  - `url`: fetched live over HTTP(S), periodically;
  - `configMapKeyRef`: read from a ConfigMap key in the platform
    namespace (key defaults to `catalog.yaml`) — the common static,
    GitOps-managed case, since the content isn't secret;
  - `secretKeyRef`: same but from a Secret (key **required**, no naming
    convention is assumed for Secrets) — for admins who want the
    manifest content itself access-controlled.
  The ConfigMap/Secret variants use local `Catalog*Source` types, not
  `corev1` selectors. There is **no fallback or priority** among the
  three: an admin picks one source, not a source-with-a-plan-B. An
  embedded `//go:embed` snapshot was also ruled out (not generic, not
  GitOps-refreshable, not inspectable).
- **`auth.bearerToken.secretRef`** — optional fetch credential, only
  meaningful with `from.url` (CEL rejects it on static sources); names
  an Opaque Secret holding the token under the `token` key. Nested by
  method so a future `basicAuth`/`mTLS` is a sibling field, never a
  reinterpretation.

`status.catalog` carries `entries` (`DiscoveredImage`: image, os, app,
version, icon, displayName), `source` (`Fetched`/`Static`),
`lastSyncTime` and `lastSyncError` (kept even after a later success).

## Sync reconciler (`operator/internal/controller/workspaceimage_catalog.go`)

The first — and only — `WorkspaceImage` reconciler. Per entry with
`spec.registry` + `spec.catalog`:

- fetches (URL branch) or reads (ConfigMap/Secret branch) the manifest,
  parses it, converts entries to `DiscoveredImage` (an unknown `os` is
  emptied rather than propagated), and patches the **status
  subresource**;
- **stale-but-served**: a failed sync updates `lastSyncError` but never
  clears already-populated `entries`;
- requeues after `operator.catalogSyncInterval` (Helm value, default
  `6h`) on success and failure alike. There is deliberately **no watch
  on the referenced ConfigMap/Secret**: the operator's ConfigMap/Secret
  reads bypass the cache by existing design (no `watch` RBAC verb), so
  the static branch accepts the same propagation delay as the live one.

Status writes never bump `metadata.generation`, so the periodic sync
never triggers `workspace_controller.go`'s
`GenerationChangedPredicate`-filtered watch on `WorkspaceImage` (fleet
drift re-evaluation) — only a human `spec` edit does. The two watches
on the same GVK belong to two independent controllers; no conflict.

## Wire format and schema — this repo is the source of truth

`operator/pkg/catalog` defines the manifest format
(`apiVersion: waas.xorhub.io/catalog/v1`), deliberately **distinct**
from `DiscoveredImage`: the wire format follows an inter-repo file
contract, the CRD type follows the `v1alpha1`/ADR 0002 cycle.

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/xorhub/waas-fable/<tag>/operator/pkg/catalog/schema/v1.schema.json
# (pin a release tag for shared files; a branch is fine while iterating locally)
apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: ghcr.io/xorhub/waas-images/firefox:1.0.0@sha256:...
    os: linux        # empty = linux
    app: firefox
    version: "1.0.0"
    icon: firefox    # dashboard-icons slug
```

The parser is tolerant (absent optional fields = zero values) but
rejects an unknown `apiVersion` cleanly — a sync error, never a crash.
`operator/pkg/catalog/schema/v1.schema.json` is **generated from the
Go struct** (`hack/gen-catalog-schema`, wired into `make generate` and
the CI `go-generated-drift` gate), so schema and parser can't silently
diverge; published schema versions are frozen, additive-only. The
`$schema` URL is an editor convenience for whoever edits a
`catalog.yaml` by hand — the reconciler never fetches it, and nothing
enforces what it points at. Pin a release tag in any `catalog.yaml`
meant to be committed or shared: published schemas are frozen and
additive-only, so a pinned tag never shifts under you. Pointing it at a
branch (e.g. `main`) is fine for local development or exploration —
handy for iterating against the latest schema — but not recommended
for shared files, since branch content can change without notice.

## Visibility and the portal picker

- `GET /api/v1/catalog` nests discovered entries on the parent:
  `CatalogImage.discovered` — visibility is **inherited** from the same
  `policy.AllowedImages`/`allowedGroups` gate as enforcement, no second
  filtering mechanism.
- Icons are dashboard-icons slugs
  (github.com/homarr-labs/dashboard-icons, Apache-2.0) resolved against
  a **locally vendored** subset (`frontend/public/icons/`, refreshed by
  `hack/vendor-icons.sh`, attribution in `ATTRIBUTION.md`) — never
  fetched live; `resolveIcon` falls back to an OS icon for absent or
  unknown slugs.
- The unified card component (`ImageOptionCard.tsx`) renders both
  existing templates (OS-icon fallback) and catalog entries in the same
  grid — one visual language for every picker.

## Relationship to the `waas-images` repo

The desktop-image build tree left this monorepo on 2026-07-10 (history
preserved via `git filter-repo`; the published registry coordinates in
`gitops/governance/images.yaml` were deliberately untouched — they
address published artifacts, independent of where sources live). What
remains in this repo:

- the dev loop resolves the sibling clone through the Makefile variable
  `WAAS_IMAGES_DIR` (documented at its use site) and fails with an
  actionable message when absent;
- the catalog **format contract** above: waas-images (the producer)
  publishes `catalog.yaml` files for `ghcr.io/xorhub/waas-images`;
  waas-fable (the reader) owns the schema. `docker.io/kasmweb` gets the
  same treatment (see `docs/kasmvnc.md` for that registry's specifics).
  An example ConfigMap-source manifest lives under
  `gitops/governance/examples/`.

## Planned follow-up: direct deploy (NOT implemented)

Creating a workspace straight from a catalog entry, without an
admin-authored `WorkspaceTemplate`, is designed but **not built**. The
settled decisions, kept on record for whoever implements it:

- the api-server will **synthesize a private, single-use
  `WorkspaceTemplate`** (labeled `waas.xorhub.io/synthetic-template`)
  rather than adding a templateless provisioning path —
  `enforce()`/`buildPodTemplate`/placement all take a real template and
  must stay single-path;
- gated by a new `WorkspacePolicy.spec.directDeploy` right (fail-closed,
  same convention as `remoteWorkspaces`); the admin gets it through the
  bootstrap all-rights policy CR, **never a code bypass**;
- synthetic templates are excluded from the template-drift watch (label
  predicate) and from `TemplateService.List`, and cleaned up by the
  operator on workspace deletion — a cross-namespace `ownerReference`
  cannot do it (the K8s GC silently ignores those);
- open at design time: namespace placement for a templateless workspace
  (accept the template-tier skip vs a 4-tier precedence through
  `WorkspaceImage`).

`ImageOptionCard.tsx` is already generic enough that this flow only has
to feed it data.
