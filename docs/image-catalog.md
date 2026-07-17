# Image catalog — published manifests, `catalog_entries`, visual picker

Registry-mode `WorkspaceImage` entries can surface a **published
catalog** of the images currently under their registry (display
metadata: os/app/version/icon), so the portal shows a picker of cards
with logos instead of raw references. The catalog is **purely
cosmetic**: the discovered entries are never read by
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

The discovered entries themselves (`Image`, `OS`, `App`, `Version`,
`Icon`, `DisplayName`, `SyncedAt`) live in the api-server's Postgres
`catalog_entries` table, keyed by `(workspace_image_name, image)` —
**not** in `status.catalog`, which only keeps the small,
purely-informational bookkeeping: `source` (`Fetched`/`Static`),
`lastSyncTime` and `lastSyncError` (kept even after a later success).
A display/picker list that changes periodically has no reason to be
retransmitted in full to every watcher of `WorkspaceImage` — see the
design study referenced by the migration commit for the full
reasoning.

## Sync worker (`api-server/internal/service/catalog_sync_worker.go`)

`CatalogSyncWorker` — a ticker-based background worker started
alongside `IdleSweeper`/`SessionSweeper` in `cmd/api-server/main.go` —
owns the periodic sync, not the operator: only the api-server has both
database access (to write `catalog_entries`) and the k8s access needed
to read `WorkspaceImage`/ConfigMap/Secret. For every `WorkspaceImage`
with `spec.registry` + `spec.catalog`, independently of the others (one
registry outage never blocks the rest):

- fetches (URL branch) or reads (ConfigMap/Secret branch) the manifest,
  parses it, and on success atomically **replaces** that image's rows
  in `catalog_entries` (`CatalogRepository.ReplaceEntries` — one
  registry's sync is one all-or-nothing swap) with an unknown `os`
  emptied rather than propagated, then patches the **status
  subresource** (`source`, `lastSyncTime`, clears `lastSyncError`);
- **stale-but-served**: a failed sync only updates
  `status.catalog.lastSyncError` — `catalog_entries` is left completely
  untouched, so a transient registry hiccup never empties the picker;
- syncs immediately on worker start (unlike the sweepers, which wait
  for their first tick — a 6h default interval would otherwise hide a
  broken catalog source for hours after a deploy), then every
  `apiServer.catalogSyncInterval` (Helm value, default `6h`) on success
  and failure alike. There is deliberately **no watch on the referenced
  ConfigMap/Secret**: those reads bypass the cache by existing design
  (no `watch` RBAC verb on them), so the static branch accepts the same
  propagation delay as the live one.

The status patch never bumps `metadata.generation`, so the periodic
sync never triggers `workspace_controller.go`'s
`GenerationChangedPredicate`-filtered watch on `WorkspaceImage` (fleet
drift re-evaluation, operator-side) — only a human `spec` edit does.
The api-server's RBAC grants `workspaceimages/status` `get`/`patch`
only (no `update`): it never needs to read-modify-write the whole
status, just this one patch.

## Wire format and schema — this repo is the source of truth

`shared/catalog` defines the manifest format
(`apiVersion: waas.xorhub.io/catalog/v1`), deliberately **distinct**
from the api-server's `model.DiscoveredImage`/`repository.CatalogEntry`:
the wire format follows an inter-repo file contract, the API types
follow their own compatibility cycle. It lives under `shared/` (not
`operator/` or `api-server/`) because both the operator's CRD
validation history and the api-server's `CatalogSyncWorker` need the
same parser with no cross-module dependency between the two.

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/XoRHub/waas/main/shared/catalog/schema/v1.schema.json
# (no vX.Y.Z release tag exists yet; pin one for shared files once it does — a branch is fine while iterating locally)
apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: docker.io/xorhub/firefox:1.0.0@sha256:...
    os: linux        # empty = linux
    app: firefox
    version: "1.0.0"
    icon: firefox    # dashboard-icons slug — also accepts an absolute
                     # https URL or file:<path> (see the picker section)
```

The parser is tolerant (absent optional fields = zero values) but
rejects an unknown `apiVersion` cleanly — a sync error, never a crash.
`shared/catalog/schema/v1.schema.json` is **generated from the Go
struct** (`shared/hack/gen-catalog-schema`, wired into `make generate`
and the CI `go-generated-drift` gate — it now covers `shared/**` as
well as `operator/**`), so schema and parser can't silently diverge;
published schema versions are frozen, additive-only. The `$schema` URL
is an editor convenience for whoever edits a `catalog.yaml` by hand —
`CatalogSyncWorker` never fetches it, and nothing enforces what it
points at. Pin a release tag in any `catalog.yaml` meant to be
committed or shared: published schemas are frozen and
additive-only, so a pinned tag never shifts under you. Pointing it at a
branch (e.g. `main`) is fine for local development or exploration —
handy for iterating against the latest schema — but not recommended
for shared files, since branch content can change without notice.

## Deployment recommendations (`profile`/`recommended`) — display/prefill only

A catalog entry can optionally carry a `profile` badge
(`hardened`/`normal`) and a `recommended` block —
`podSecurityContext`/`securityContext`/`volumes`/`env` hints copied from
`waas-images/HARDENING.md`'s *"To apply on the platform side"* section,
so the admin template form doesn't start from a blank page on identity
and hardening. **These fields follow the exact same regime as every
other discovered field above: purely cosmetic, never read by
`enforce()`/`buildPodTemplate`, prefill-only.** They live on the wire
format (`shared/catalog.Entry`) and on `catalog_entries`
(`profile`/`recommended` columns, the latter `JSONB` — SQLite's type
affinity stores it as text unchanged, so it round-trips there too)
exactly like `os`/`app`/`icon`; nothing on `WorkspaceImageSpec` or any
CRD carries them, and no webhook validates or requires them. Like an
unknown `os`, a `profile` outside `hardened`/`normal` is dropped to
empty by the sync worker rather than propagated to the picker.

```yaml
apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: docker.io/xorhub/ubuntu-xfce:1.1.0@sha256:...
    os: linux
    app: ubuntu-xfce
    version: "1.1.0"
    profile: hardened
    recommended:
      podSecurityContext:
        runAsUser: 1000
        runAsNonRoot: true
      securityContext:
        readOnlyRootFilesystem: true
        capabilities:
          drop: ["ALL"]
      volumes:
        - name: tmp
          mountPath: /tmp
        - name: run
          mountPath: /run
          readOnly: true
      env:
        - name: WAAS_SSH_ENABLED
          description: "Enable sshd (publickey only) — boolean '0'/'1'"
          protocols: [ssh]
          default: "0"
          requires: [WAAS_SSH_AUTHORIZED_KEYS_FILE]
        - name: WAAS_SSH_AUTHORIZED_KEYS_FILE
          description: "Path to the authorized public key — mount from a
            Secret (valueFrom.secretKeyRef), never a literal value.
            Required as soon as WAAS_SSH_ENABLED=1: the image's
            entrypoint refuses to start otherwise (fail-closed by
            design, not a bug — see
            waas-images/base/*/rootfs/etc/waas/entrypoint.d/50-sshd.sh)."
          protocols: [ssh]
```

`recommended.volumes` is deliberately **not** a `corev1.Volume` +
`corev1.VolumeMount` pair: the only case `HARDENING.md` documents is a
plain `emptyDir` mounted at a fixed path (the `/tmp`+`/run` pair needed
alongside `readOnlyRootFilesystem`), so one `name`/`mountPath`/`readOnly`
entry says that without repeating the volume/mount boilerplate twice
per entry. It never covers configMap/secret-backed mounts (e.g. an
init.d script volume) — those stay the admin's call via the free-form
`Workload.volumes`/`volumeMounts` override, never suggested by the
catalog. `env[].requires` names another hint of the *same*
recommendation that makes no sense without this one; it is purely
descriptive (lets the prefill UI group/warn together) — never
validated, never enforced, the admin can still apply one without the
other exactly like today.

The admin template form (`CatalogImageField.tsx`) offers an explicit
**"Apply catalog recommendations"** button next to the image field once
a registry-mode discovered image carrying a `recommended` block is
selected — never an automatic prefill on selection, so an admin can
never save a `securityContext` they didn't consciously see. Clicking it
expands the (collapsed-by-default) Workload YAML section so the
injected values are visible before saving, and merges `env` hints into
the template's env list by name without overwriting an already-present
entry.

The `profile` badge is scoped to this admin form: `ImageOptionCard`
only renders it when passed a `profile`, and `CatalogImageField.tsx` is
the only caller that does. The end-user create-workspace flow lists
templates, not catalog entries — it looks up the backing catalog entry
only to borrow its icon, deliberately not its profile, since a template
is an admin-curated artifact whose workload may already have diverged
from the catalog recommendation (the admin can edit the prefilled
`securityContext` after applying, or never apply it at all); resurfacing
"hardened" on the end-user picker could assert a hardening the template
no longer has.

**Explored and explicitly rejected**: letting the catalog trigger an
operator-side secret generation (e.g. an SSH keypair), addressed by env
var name or by an enumerated generator id. `env`/`recommended` stays
strictly informational; a generation mechanism, if it is ever built,
belongs on `WorkspaceTemplateSpec` itself (near
`WorkspaceProtocol.CredentialsSecretRef`) with its own webhook
validation and operator logic — never as an extension of the catalog
recommendation.

*Resolution (2026-07)*: the mechanism landed as an **implicit
level-2 default** on the protocol signal itself (ssh declared + no
explicit source ⇒ generated keypair — see
[templates-and-protocols.md](templates-and-protocols.md) §
Credentials), so the catalog needed no change at all: there is
nothing to suggest when generation is the default.

## Visibility and the portal picker

- `GET /api/v1/catalog` nests discovered entries on the parent:
  `CatalogImage.discovered` — visibility is **inherited** from the same
  `policy.AllowedImages`/`allowedGroups` gate as enforcement, no second
  filtering mechanism.
- An icon reference (`icon` in a catalog entry, or a template's
  `spec.logo` — same resolver, `frontend/src/lib/icon.ts`) takes one of
  three forms, detected by prefix:
  - **Absolute `https://` URL** — used as-is as the image source, with
    no host allow-list (any https host is accepted; plain `http://` is
    rejected for now). Useful when the wanted asset doesn't follow the
    slug convention, e.g.
    `https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/webp/longhorn.webp`.
  - **`file:<path>`** — a path internal to the frontend, resolved
    same-origin from the web root (`file:custom/longhorn.svg` →
    `/custom/longhorn.svg`); it never makes the browser leave the
    portal's origin. This is a repo convention, **not** the browser's
    `file://` scheme: the admin mounts the asset into the nginx
    container under `/usr/share/nginx/html/<path>` or bakes it into a
    custom frontend image (`FROM` the published image + `COPY`). The
    path is validated — a leading `/`, `..` traversal, an embedded
    scheme, or a backslash falls back to the OS icon without a load
    attempt.
  - **dashboard-icons slug**
    (github.com/homarr-labs/dashboard-icons, Apache-2.0), e.g.
    `firefox` — **loaded live** from the dashboard-icons CDN, no
    per-app vendoring or frontend allowlist to maintain. Because the
    value comes from untrusted catalog content, `resolveIcon` validates
    it against `^[a-z0-9][a-z0-9-]*$` before building the CDN URL; a
    rejected slug is never fetched.

  Only the two OS fallbacks are vendored (`frontend/public/icons/`,
  refreshed by `hack/vendor-icons.sh`, attribution in
  `ATTRIBUTION.md`); they are shown when the reference is
  absent/invalid or when the load fails (unknown slug, offline). Note
  the slug and https-URL forms make the end user's browser contact a
  third party (`cdn.jsdelivr.net` or the URL's host) when rendering
  catalog icons — `file:` never does; the repo ships no CSP today, but
  if one is ever introduced, those hosts must be allowed in `img-src`.
- The unified card component (`ImageOptionCard.tsx`) renders both
  existing templates (OS-icon fallback) and catalog entries in the same
  grid — one visual language for every picker.
- The admin template form's image field
  (`frontend/src/pages/admin/templates/CatalogImageField.tsx`) reuses
  the same picker as an **input helper**: a single-image catalog entry
  fills the field on selection, and with a registry-mode entry selected
  the image input itself doubles as a combobox — what is typed both is
  the value and fuzzy-filters the entry's `discovered[]` (fed by
  `GET /api/v1/admin/images`, the unfiltered admin view, not the
  policy-gated `/catalog`). `spec.image` stays a free string end to
  end: any reference can be typed or pasted regardless of catalog
  selection, which is pure frontend UI state.

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
  publishes `catalog.yaml` files for `docker.io/xorhub` (migrated from
  `ghcr.io/xorhub/waas-images` — Docker Hub has no nested path, so each
  image is its own top-level `xorhub/<image>` repo); waas (the
  reader) owns the schema. `docker.io/kasmweb` gets the same treatment
  (see `docs/kasmvnc.md` for that registry's specifics).
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
