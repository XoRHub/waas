# Cross-repo audit — catalog in DB + deployment recommendations (waas ⇄ waas-images)

- **Date**: 2026-07-16
- **Scope**: the two cross-repo features landed early/mid July 2026 —
  (1) discovered catalog moved from `WorkspaceImage.status.catalog.entries`
  to the `catalog_entries` table (`waas` commits `3581fffc095e`,
  `54b10b34303c`, `e0546fec63d2`, `f0a00dd53d1e`, `85489c4566f7`), and
  (2) `profile`/`recommended` badge + prefill (`waas` commits
  `0600e4179ff0`, `82a0b3b7eafd`, `8397caeddaab`, `a26cd7d79049`;
  `waas-images` commits `99b81a7bc6ed`, `7e2aca0743a4`, `ac98296e22ad`).
- **Method**: read-only verification of both working trees at
  `waas@58b7c1c6b56a` (main) and `waas-images@ac98296e22ad` (main), plus
  live URL checks against GitHub (curl, `git ls-remote`). Every claim below
  cites `repo` + `file:line`; anything not verifiable statically is marked
  as such. No file in either repo was modified.

---

## Feature 1 — discovered catalog: CRD status → `catalog_entries`

### Verified conforme

- **Schema/table/worker chain is complete and single-pathed.**
  `waas shared/catalog/catalog.go:24-107` defines the wire format;
  `waas api-server/internal/service/catalog_sync_worker.go:123-164`
  (syncOne) writes to the table via
  `waas api-server/internal/repository/catalog_sql.go:26-50`
  (ReplaceEntries, delete+insert in one transaction so a picker read
  never sees an empty catalog mid-sync). The only read path back out is
  `imageToModel` (`waas api-server/internal/service/governance_service.go:689-727`),
  used by all four call sites that project a `CatalogImage`
  (`governance_service.go:108`, `:275`, `:367`, `:388`) — no second,
  divergent read path exists (`grep model.DiscoveredImage{` matches only
  `governance_service.go:715` outside tests).
- **Fail-soft doctrine holds**: any fetch/parse/read failure only patches
  `status.catalog.lastSyncError` and never touches existing rows
  (`waas api-server/internal/service/catalog_sync_worker.go:130-136`).
- **DB round-trip is really tested on both backends** — the migration
  comment (`waas api-server/migrations/20260715100000_add_catalog_recommendation.up.sql:1-10`)
  is backed by an actual test: `TestCatalogRepositorySuite`
  (`waas api-server/internal/repository/suites_test.go:269-328`) inserts an
  entry with `Profile: "hardened"` and a JSON `Recommended`, reads it back,
  and asserts both the JSON validity and that absent fields stay zero. It
  runs through `forEachBackend`
  (`waas api-server/internal/repository/backends_test.go:26-40`: sqlite
  always, postgres when `WAAS_TEST_PG_URL` is set), and CI does set it for
  the api-server module (`waas .github/workflows/ci-go.yml:106`) against a
  real postgres service container (`ci-go.yml:69-80`).
- **api-server RBAC for the new split is present and correctly scoped**:
  `waas helm/waas/templates/api-server/roles.yaml:29-31` grants
  `workspaceimages/status` `get, patch` — exactly what
  `syncOne`'s `Status().Patch` needs, with the comment correctly stating
  the entries themselves go to Postgres.
- **CI drift gate covers all of `shared/**`**: the `shared` path filter is
  `go-global` + `'shared/**'` (`waas .github/workflows/ci.yml:96-99`), and
  `DRIFT` is set from `F_OPERATOR`/`F_SHARED`
  (`waas .github/workflows/ci.yml:201-204`) — a future `shared/`
  sub-package is covered, not just `shared/catalog/`.
- **Vendored schema is byte-identical today**:
  `waas shared/catalog/schema/v1.schema.json` and
  `waas-images ci/schema/v1.schema.json` diff clean (verified 2026-07-16).
- **The re-sync mechanism answers the "additive change without a tag"
  question correctly**: `waas-images ci/sync_schema.sh:17-26` fetches
  `waas`'s schema from **`main`** (`WAAS_RAW_BASE=.../XoRHub/waas/main`),
  not from a pinned tag or submodule, and
  `waas-images .github/workflows/catalog-schema-sync.yml:28-33` runs it
  weekly (Monday 07:00) plus `workflow_dispatch`. An additive-only schema
  change on `waas` `main` with no tag bump is therefore picked up within a
  week; the PR is never auto-merged
  (`catalog-schema-sync.yml:8-15`, `:52-78`) and the candidate schema is
  re-validated against both catalogs by `build.yml`'s own `catalog` job on
  that PR (stated in `catalog-schema-sync.yml:12-15`; the actual PR-time
  run is CI behavior not replayable statically). Idempotency is keyed on
  the `waas` commit SHA (`catalog-schema-sync.yml:56-65`).
- **Operator's generated role is clean**: `waas
  operator/config/rbac/role.yaml:90-99` grants `workspaceimages` only
  `get, list, watch`; the file contains no `workspaceimages/status` rule
  (its only status rule is `workspaces/status`, line 113), and no operator
  Go file references `workspaceimages/status` or writes a WorkspaceImage
  status any more (grep over `operator/` returns nothing) — consistent
  with the reconciler removal in `f0a00dd53d1e`.

### Écarts trouvés (details in the final section)

- The **Helm** operator role still grants `workspaceimages/status`
  (`waas helm/waas/templates/operator/roles.yaml:77-81`) — residual, see
  finding 1.
- `docs/image-catalog.md:146` contradicts the actual migration about
  JSONB — see finding 4.

---

## Feature 2 — `profile`/`recommended` (badge + prefill)

### Verified conforme

- **Producer derivation is automatic, not hand-written**
  (`waas-images ci/generate_catalog.py`): `profile` is a total mapping
  from the variant's build profile (`PROFILE_WIRE_NAME`, line 67);
  `recommended` comes from two fixed constants
  (`RECOMMENDATION_STANDARD` lines 73-89, `RECOMMENDATION_DEV` lines
  99-106) plus per-protocol env hints driven by each variant's `smoke:`
  block (`_ENV_HINTS_BY_PROTOCOL` lines 114-162, `env_hints` lines
  165-175). Recomputed every run, never copied from the previous catalog
  (comment lines 258-264). Unit tests exist
  (`waas-images ci/tests/test_generate_catalog.py`, added in
  `99b81a7bc6ed`).
- **The constants match `HARDENING.md`**: the standard block
  (`runAsNonRoot`, `runAsUser/fsGroup: 1000`,
  `seccompProfile: RuntimeDefault`, `allowPrivilegeEscalation: false`,
  `capabilities.drop: [ALL]`, `readOnlyRootFilesystem: true`, emptyDir on
  `/tmp` and `/run`) mirrors `waas-images HARDENING.md:89-92` verbatim;
  the dev block's three expressible exceptions
  (`allowPrivilegeEscalation: true`, `readOnlyRootFilesystem: false`, no
  `drop: ALL`) mirror `waas-images HARDENING.md:119-125`. The generated
  `catalog-waas-images.yaml` is consistent: all 7 standard images are
  `hardened`, `devtools-dev` is `normal` with exactly the dev block.
- **Every published `env[].name` resolves to a variable actually read by
  the images' entrypoints** — no dangling hint:
  - `WAAS_RDP_ENABLED` — `waas-images base/ubuntu/rootfs/usr/local/bin/waas-entrypoint:48,81,144` (and fedora Dockerfile:128);
  - `RDP_AUTH_ENABLED` — `waas-entrypoint:44-46`;
  - `WAAS_SSH_ENABLED` / `WAAS_SSH_AUTHORIZED_KEYS_FILE` —
    `waas-images base/{ubuntu,fedora}/rootfs/etc/waas/entrypoint.d/50-sshd.sh:19,29`;
  - `WAAS_AUDIO_ENABLED` — `waas-entrypoint:112-113`.
  The reverse direction is a deliberate curated subset: entrypoints read
  more `WAAS_*` vars than the hints advertise (`WAAS_VNC_ENABLED`,
  `WAAS_SSH_PORT`, `WAAS_STARTUP`, …) but none of those is claimed
  anywhere as a recommendation, and the hint table's own comment
  (`generate_catalog.py:108-113`) documents how it is kept in sync.
  Protocol coverage is right too: the three OS desktops
  (VNC+RDP+SSH `smoke:` blocks) carry all five hints; app images carry
  only the VNC audio hint.
- **`catalog-kasmweb.yaml` carries no `profile`/`recommended`** (0
  occurrences), as its generator has no local doctrine to derive from —
  and the whole `waas` chain treats absence as zero values end to end
  (`shared/catalog/catalog.go:54-58` `omitempty`/pointer;
  `catalog_sql.go:88-96` NULL storage;
  `governance_service.go:733-742` nil on empty; frontend badge hidden on
  empty `ImageOptionCard.tsx:101`).
- **Producer-side contract enforcement exists**:
  `waas-images ci/validate_catalog.py` validates both catalogs against the
  vendored schema (Draft 2020-12, `additionalProperties: false`, enums —
  including the `profile` enum) and `build.yml:561` runs it in CI.
- **Go → wire TS mapping is field-complete**:
  `shared/catalog.Entry{Profile, Recommended}` →
  `repository.CatalogEntry{Profile string, Recommended json.RawMessage}`
  (`waas api-server/internal/repository/repository.go:114-121`) →
  `model.DiscoveredImage{Profile, Recommended *DeploymentRecommendation}`
  (`waas api-server/internal/model/model.go:394-447` — all five
  `EnvHint` fields including `Requires` and `Default` are mirrored on
  `RecommendedEnvVar`, `RecommendedVolume` mirrors name/mountPath/readOnly)
  → `frontend/src/types.gen.ts:485-553`. The `podSecurityContext`/
  `securityContext` fields degrade to `unknown` in TS **by explicit,
  documented tygo mapping** (`waas api-server/tygo.yaml`, `type_mappings`
  block: "the frontend only merges these structurally into Workload YAML,
  it never reads individual fields") — deliberate, not a loss.
- **`imageToModel` reads the two new fields on the same path as
  `os`/`app`/`icon`** (`waas api-server/internal/service/governance_service.go:714-724`)
  — one loop, one repository call, no separate or forgotten path.
- **Frontend merge behavior matches the doc claim**
  (`docs/image-catalog.md:209-211`): `applyRecommendation`
  (`waas frontend/src/pages/admin/templates/TemplateDialog.tsx:73-103`)
  merges env by name and never overwrites an already-present entry
  (lines 95-99: `filter(e => !existingNames.has(e.name))`), while
  `podSecurityContext`/`securityContext`/`volumes` are set unconditionally
  — explicitly reasoned in the code comment (lines 67-72) and consistent
  with the doc's "injected values are visible before saving" framing
  (the Workload section is force-expanded, line 102). The button is
  rendered only when the discovered entry backing the *current field
  value* carries a recommendation
  (`CatalogImageField.tsx:99`, `:138-147`) — never fired by selection.
- **The apply button is the only UI path that lands a `recommended` into
  a template.** The template Workload is otherwise a free-form YAML
  textarea (`TemplateDialog.tsx:59-63`, `:143-156`) — any other content an
  admin gets in there is hand-typed/pasted, which is by definition an
  explicit gesture; there is no template-duplication or YAML-import
  feature in `TemplatesPage.tsx` (grep clean), and the raw admin API is
  out of the UI's scope. The documented "never an automatic prefill"
  requirement holds.
- **i18n**: exactly 4 keys added, present in both locales at the same
  lines and namespace (`waas frontend/src/i18n/locales/en.json:269-272`,
  `fr.json:269-272`, all under `admin.templatesPage.*` like the rest of
  the template-form strings). The French is a real adaptation
  ("Appliquer les recommandations du catalogue", "Durci"), not copied
  English.
- **Badge style follows the app's existing pill convention**:
  `ImageOptionCard.tsx:103-107` (`rounded px-1.5 py-0.5 text-[10px]`,
  amber for the highlighted state) matches the pills already in
  `ProtocolsFieldset.tsx:211` (amber-100/amber-700),
  `UsersPage.tsx:65` and `TemplatesPage.tsx:73` (slate `rounded px-1.5
  py-0.5`) — no isolated style.
- **`EnvHint.Requires` round-trips**: published by the producer
  (`generate_catalog.py:140`), typed on the wire
  (`shared/catalog/catalog.go:105`), mirrored in the model
  (`model.go:445`) and in TS (`types.gen.ts:551`).

### Écarts trouvés (details in the final section)

- `profile` is never validated consumer-side and an unknown value renders
  as the "Normal" badge — finding 2.
- The doc-claimed "never overwrites an already-present env entry"
  behavior has no test — finding 5.
- The badge is de facto admin-only, and that scoping is documented
  nowhere — finding 6.

---

## Écarts trouvés (gravité décroissante)

> **Security-relevant first**, per the audit brief. None of these is
> exploitable today; findings 1 and 2 are the two with any security
> texture at all.

### 1. Residual RBAC: the Helm chart still grants the operator `workspaceimages/status` (security hygiene)

- `waas helm/waas/templates/operator/roles.yaml:77-81` grants the
  **operator** `workspaceimages/status` `get, patch, update`, under a
  comment ("Catalog sync results (status.catalog)") describing a job the
  operator no longer has: the catalog reconciler was removed in
  `waas` commit `f0a00dd53d1e`, no operator Go file references
  `workspaceimages/status` or patches a WorkspaceImage status any more,
  and the operator's own generated role
  (`waas operator/config/rbac/role.yaml:90-99`, status rules line 113)
  correctly has no such grant. `status.catalog` is written exclusively by
  the api-server's `CatalogSyncWorker` now
  (`waas api-server/internal/service/catalog_sync_worker.go:132,160`,
  granted at `waas helm/waas/templates/api-server/roles.yaml:29-31`).
- **Impact**: least-privilege violation only — a dead grant that widens
  the operator's blast radius for no function. Note the Helm grant is
  even wider (`update` on top of `get, patch`) than what the api-server
  legitimately needs. Fix side: `waas`.

### 2. `profile` flows to the UI unvalidated; an unknown value silently renders as the "Normal" badge

- The sync worker guards `os` against untrusted manifest content
  (`waas api-server/internal/service/catalog_sync_worker.go:184-189`,
  `normalizeOS`, whose own comment says "the manifest is untrusted
  content") but passes `Profile` straight through with no equivalent
  guard (`catalog_sync_worker.go:148`). `catalog.Parse` is a plain
  `yaml.Unmarshal` (`waas shared/catalog/catalog.go:112-121`); the
  `jsonschema:"enum=,enum=hardened,enum=normal"` tag on
  `Entry.Profile` (`catalog.go:54`) only feeds the *generated schema*,
  it enforces nothing at parse time. The generated TS type is
  accordingly a free `profile?: string` (`waas frontend/src/types.gen.ts:511`).
- Downstream, the badge renders for **any** non-empty string, and the
  ternary maps everything that isn't exactly `hardened` to the
  "Normal" label (`waas frontend/src/components/ImageOptionCard.tsx:101-113`).
  A third-party registry-mode catalog publishing `profile: hardened-v2`
  (or garbage) therefore shows the admin a confident **"Normal"** badge.
- Producer-side validation exists for the two catalogs `waas-images`
  publishes (`waas-images ci/validate_catalog.py`, enum-enforcing,
  run at `build.yml:561`) — but `spec.catalog.from.url` accepts any URL,
  so the enum is only enforced for the one producer that happens to be
  well-behaved.
- **Impact**: silently misleading display for an admin relying on the
  badge (the exact failure mode the audit brief asks about). Text is
  i18n-sourced and React-escaped, so no injection. Fix side: `waas`
  (a `normalizeProfile` sibling next to `normalizeOS`, or dropping
  unknown values to "").

### 3. Both live `$schema` URLs are dead: old repo name **and** a tag that never existed

- `waas gitops/governance/examples/catalog-configmap.yaml:23` pins
  `raw.githubusercontent.com/xorhub/waas-fable/v0.1.0/...` and
  `waas docs/image-catalog.md:107` shows
  `raw.githubusercontent.com/xorhub/waas-fable/<tag>/...`.
- Verified live (2026-07-16): the old-name URL is **404 with no
  redirect** (raw.githubusercontent does not follow repo renames;
  `api.github.com/repos/xorhub/waas-fable` is also 404) — so these are
  broken, not cosmetic. Worse, the corrected URL
  `xorhub/waas/v0.1.0/...` is **also 404**: the `waas` repo has zero
  tags, local and remote (`git tag -l` empty,
  `git ls-remote --tags origin` empty — release-please PRs are still
  unmerged). Only the `main` ref resolves (verified 200). The doc's own
  advice at `docs/image-catalog.md:108` ("pin a release tag for shared
  files") is currently impossible to follow.
- **Impact**: doc/example only — but it breaks silently in the exact
  advertised use (yaml-language-server editor validation just goes dark,
  no error a user would notice), and `waas-images ci/schema/README.md`
  tells humans the same editor story. Fix side: `waas` (point at `main`
  until a first release tag exists, then re-pin).

### 4. Doc contradicts the schema: "JSON text — not JSONB" vs an actual `JSONB` column

- `waas docs/image-catalog.md:146` states the `recommended` column is
  "JSON text — **not** JSONB, since this table round-trips on both the
  Postgres and SQLite backends", but the migration declares
  `recommended JSONB`
  (`waas api-server/migrations/20260715100000_add_catalog_recommendation.up.sql:12`),
  with a header comment (lines 1-10) explaining precisely why JSONB *is*
  safe on SQLite (NUMERIC affinity stores `{...}` text as-is). The
  migration's claim is the one backed by tests (see Feature 1, verified);
  the doc's is stale — it describes the design that was rejected.
- **Impact**: doc-only, but it inverts a deliberate arbitrage; the next
  person touching the column will find the doc and the migration in
  direct contradiction. Fix side: `waas` (one line in
  `docs/image-catalog.md`).

### 5. The documented "never overwrite an existing env entry" guarantee is untested

- The behavior is implemented (`waas frontend/src/pages/admin/templates/TemplateDialog.tsx:95-99`)
  and documented (`waas docs/image-catalog.md:209-211`), but the only
  apply-recommendation test starts from a template with **no** env at all
  (`waas frontend/src/pages/admin/templates/TemplateDialog.test.tsx:44-48`
  `initial`, single `it` at lines 59-84): the `filter` on
  `existingNames` is exercised solely against an empty set. The case the
  doc actually promises — "the admin already has a value for
  `WAAS_SSH_ENABLED`, applies recommendations anyway, keeps their value"
  — has no test in `TemplateDialog.test.tsx` nor
  `CatalogImageField.test.tsx` (whose 6 tests cover picker/search/apply-
  button-visibility only, lines 111-187).
- **Impact**: a regression flipping the filter (or switching to a
  map-overwrite merge) would pass CI while silently clobbering admin
  config on an explicit-but-trusted click. Fix side: `waas` (one test).

### 6. The `hardened` badge is de facto admin-only — an implicit, untraced choice

- `ImageOptionCard` renders the badge only when given `profile`; the only
  caller passing it is the admin template form
  (`waas frontend/src/pages/admin/templates/CatalogImageField.tsx:179`).
  The shared `ImagePicker` cannot forward it — `ImagePickerOption` has no
  `profile` field (`waas frontend/src/components/ImagePicker.tsx:5-14`,
  card instantiated without it at lines 99-112) — and the end-user
  create-workspace flow lists **templates**, not catalog entries
  (`waas frontend/src/dialogs/CreateWorkspaceDialog.tsx:282-297`).
  Notably that dialog *already* looks up the exact discovered entry
  backing each template to steal its icon
  (`CreateWorkspaceDialog.tsx:114-117`) — the profile is sitting right
  there and is deliberately (or accidentally) not surfaced.
  `SessionCard.tsx` imports only `AppIcon` (line 3), no badge either.
- Neither `docs/image-catalog.md` (badge mentioned only at line 137, in
  wire-format terms) nor the frontend commit message (`8397caeddaab`)
  states that the badge is scoped to the admin context.
- **Impact**: informational — possibly the right call (a template is an
  admin-curated artifact; its workload may have diverged from the
  recommendation, so re-badging it "hardened" could actively mislead),
  but that reasoning is recorded nowhere. Either document the scoping in
  `docs/image-catalog.md` or extend the badge deliberately. Fix side:
  `waas`.

### 7. Cosmetic `waas-fable` residue in prose/comments — both repos

Complete inventory of tracked-file occurrences beyond the two dead URLs
of finding 3 (grep `waas-fable` case-insensitive plus `waas_fable`/
`waasfable` variants — none of the variants matched; untracked local
files `.claude/settings.local.json` and `waas-images` worktrees
excluded). All are prose/comments where the identifier is a *name*, not
a fetched URL — nothing resolves, so nothing breaks:

- `waas shared/hack/gen-catalog-schema/main.go:3` — "waas-fable is the
  READER of that format".
- `waas docs/image-catalog.md:290` — "waas-fable (the platform repo)".
- **À traiter côté waas-images** (separate repo, own release cycle):
  - `README.md:237`, `README.md:297`
  - `AGENTS.md:87`
  - `.github/workflows/build.yml:530` (comment: "the source waas-fable
    fetches")
  - `.github/workflows/catalog-kasmweb.yml:13` (same)
  - `kasm/catalog-mapping.yaml:2` (comment referencing "waas-fable
    gitops/governance/images.yaml")
  - `base/ubuntu/Dockerfile:41,119`, `base/fedora/Dockerfile:26,102`
    (comments referencing `DefaultHomeMountPath (waas-fable)`)
- **Impact**: cosmetic. Worth a single sweep per repo so future greps
  for the real repo name don't miss these files.

### 8. Minor TOCTOU in the schema re-sync script (à traiter côté waas-images, or wontfix)

- `waas-images ci/sync_schema.sh:22` resolves the `waas` `main` SHA via
  `gh api`, then line 26 fetches the schema file in a **separate**
  unauthenticated raw request. A push to `waas` `main` landing between
  the two calls makes the SHA recorded into `ci/schema/README.md`
  (line 35's `sed`) and the PR title/body
  (`catalog-schema-sync.yml:54-76`) describe a different commit than the
  bytes actually vendored (raw.githubusercontent's own CDN cache widens
  the window slightly).
- **Impact**: cosmetic provenance skew, self-correcting on the next
  weekly run since the diff is against content, not SHA. Fetching
  `raw.githubusercontent.com/XoRHub/waas/${WAAS_SHA}/...` instead of
  `/main/` would close it in one line, if ever touched.

---

## Non-findings worth recording (checked, conforme)

These were explicit audit leads that turned out clean — recorded so the
next audit doesn't redo them:

- Schema sync diffs against `main`, weekly + manual — no pinned-tag
  staleness trap (Feature 1 section above).
- All five published `EnvHint.Name`s resolve to real entrypoint reads in
  both the ubuntu and fedora image trees (Feature 2 section above).
- JSON(B) round-trip is tested on sqlite **and** postgres, and CI runs
  the postgres leg (Feature 1 section above).
- Go→TS chain loses no field; the two `unknown`s are documented tygo
  policy, and the `profile` enum loss in TS mirrors `model.go`'s own
  (deliberate) untyped string — the *consumer-side* enum gap is finding 2,
  not a generator defect.
- `governance_service.go` has exactly one projection path, shared with
  `os`/`app`/`icon`.
- Env merge-by-name is implemented as documented (only its *test* is
  missing — finding 5).
- i18n keys complete, coherent, and idiomatic in both locales.
- `go-generated-drift` gate covers all of `shared/**`.
- Badge visual style matches existing pill conventions.
