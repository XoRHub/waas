# Fable 5 Prompt — Feature 7: thematic sections for protocol params + userParams/expertUserParams split

Paste this document as-is as an implementation prompt. It assumes you (Fable 5) have no prior conversation context.

## Repo context

The guacd connection parameters (VNC/RDP/SSH) come from a single Go-side registry, `operator/pkg/params/params.go`. Some are clearly semantically related to each other (e.g. `enable-audio`/`audio-servername`, or the image-quality params) but are today rendered as a flat list on the frontend, with no thematic grouping at all. Separately, the CRD exposes a `userParams` mechanism (a list of names that may be overridden at connect-time) which could benefit from being split into two combinable tiers rather than a single flat list.

## What already exists (know this before coding)

**The `Param` registry** (`params.go:51-70`): `Name`, `Protocols []string`, `Kind`, `Enum`, `Min/Max *int`, `Default`, `Tier`, `Live bool`, `Description`.

**`Tier` has three values** (`params.go:23-38`, doc-comment):

- `TierUI = "ui"` — rendered in portal forms.
- `TierAdvanced = "advanced"` — per the code comment, "settable in the CR/template only... or the template editor's advanced section — never in end-user forms". **Verify this point while coding**: `ParamField.tsx:270-279` (`tieredParams`) sorts `tier:['ui']` → `simple`, `tier:['advanced']` → `advanced`, and `ProtocolTabs.tsx:163-219` (`ProtocolParamsForm`) always shows `simple`, `advanced` behind a "show advanced params" toggle (lines 185, 207-216) — **in the portal's end-user form, not only in the template editor**. So there is a tension between the registry's comment (advanced = never in an end-user form) and the code's real behavior (advanced = accessible in the end-user form behind a toggle) — clarify and align the two before building on top of it, or explicitly document that this is the intended behavior.
- `TierPlatform = "platform"` — never settable by the template nor the user, banned by `ValidateTemplateParams` (line 505), `ValidateUserParamNames` (line 523), `ValidateUserOverrides` (line 540). `TierAdvanced` is treated **identically to `TierUI`** in these three validation functions — the ui/advanced distinction today only exists in **rendering**, not in validation policy.

**`GET /api/v1/meta/protocols`** (`api-server/internal/handler/meta_handler.go:23-39`) serves `params.ForProtocol(proto)` (params.go:416-434, sorted ui first then advanced then platform) as-is, `Tier` included.

**`userParams` on the CRD side** — `WorkspaceProtocol` (`operator/api/v1alpha1/workspacetemplate_types.go`):

- `Params map[string]string` (line 227, json `params,omitempty`): the template's locked/default values.
- `UserParams []string` (line 232, json `userParams,omitempty`): **a list of parameter NAMES** that the user may override at connect-time — not values. Any name absent from this list stays locked to `Params`.
- This fine-grained filter operates **under** a broader right: `TemplateOverrides.AllowedFields []OverridableField` (line 249), with `FieldProtocolParams = "protocolParams"` (line 42) which allows or forbids any connect-time param tweak at all — cf. `connectTimeRights` (`operator/pkg/policy/overrides.go:60-62`), comment: "connect-time guacd parameter tweaks, enforced by the api-server on /connect (template userParams stays the fine-grained filter)".

**`UserParams` consumption chain**: `api-server/internal/service/template_service.go` (lines 84, 251, 259, 373, DTO + `ValidateUserParamNames`); `workspace_service.go:552` (`params.ValidateUserOverrides(protocol, in.Params, entry.UserParams, isAdmin)` — admins bypass the list); `workspace_service.go:929` (copies `entry.UserParams` into the served model). **The operator does not need `UserParams` to build guacd params** — it's strictly an api-server-side guard rail; the webhook/operator only validates `Params` (locked values) via `ValidateTemplateParams`.

**The `pkg/policy` exhaustiveness test** (`overrides_registry_test.go:24`, `TestOverrideRegistryIsExhaustive`) covers `WorkspaceOverrides`/`WorkspaceSpec` fields — **a new `expertUserParams` field on `WorkspaceProtocol` will not make it fail** (it's neither one nor the other), but you still need to add dedicated test coverage for the merge/priority logic on the api-server side, and update the description of `FieldProtocolParams` in `overrides.go:61` to mention the new split.

**Frontend — no thematic grouping today**: `ProtocolTabs.tsx` = one tab per protocol, `ProtocolParamsForm` = a flat grid (`simple` always visible, `advanced` behind a toggle), no "Display"/"Audio"/"Clipboard"/"Security" section.

**Confirmed groupings (exact registry names, do not invent others)**:

- **Audio (VNC)**: `enable-audio` (line 116, ui), `audio-servername` (line 121, advanced).
- **Display/quality (VNC/RDP)**: `color-depth` (line 96, ui, enum 8/16/24/32), `swap-red-blue` (line 101, ui), `cursor` (line 106, ui, enum local/remote), `force-lossless` (line 111, ui), `resize-method` (line 145, ui, RDP). **No `dpi` param** in the registry (handled on the platform side via a doc comment `workspacetemplate_types.go:225`, not a `Param` entry — don't add it to this group).
- **Clipboard**: `disable-copy`/`disable-paste` (lines 84-92, shared, ui, live), `clipboard-encoding` (line 136, VNC, advanced), `normalize-clipboard` (line 235, RDP, advanced).
- **Security/session**: `read-only` (line 79, shared, ui), `security`, `ignore-cert`, `console`, `disable-auth` (RDP, some `TierPlatform` — check which ones are actually editable before grouping them with the others).

## What needs to be delivered

### A. Thematic sections in the params form

Add a categorization field driven by the registry (e.g. `Category string` on `Param`, values like `"display"`, `"audio"`, `"clipboard"`, `"security"`) — not a hardcoded grouping logic on the frontend, to stay consistent with the "registry = single source of truth" architecture already in place for `Kind`/`Tier`. Assign a category to every existing param (use the confirmed groupings above as a base). Make `ProtocolParamsForm` render per category (a subheading per section), keeping the `simple`/`advanced` distinction **within** each section rather than replacing it — decide and document whether the "show advanced" toggle stays global or becomes per-section.

### B. Combinable `userParams` / `expertUserParams`

Add a second field `ExpertUserParams []string` (json `expertUserParams,omitempty`) alongside `UserParams` on `WorkspaceProtocol`. The two lists are **combinatorial, not exclusive**: the effective set of names overridable at connect-time is the union of both. `expertUserParams` is **priority** in case of a conflicting rule on the same name present in both lists — specify and code this notion of priority (for example: if a name appears in `expertUserParams`, apply the "advanced"/less restrictive validation rules to it even if it also appears in `userParams` with stricter rules; document exactly what "priority" changes in practice in your implementation, this isn't obvious as stated since these are lists of names, not per-name rules).

Wire this new field into the same chain as `UserParams` today: `template_service.go` (DTO + name validation), `workspace_service.go:552` (`ValidateUserOverrides` must account for the union of both lists), `workspace_service.go:929` (exposure in the served model).

## Constraints to respect

- Zero contract change for existing templates that only use `userParams` — `expertUserParams` is an optional additional field, not a replacement.
- Add an exhaustiveness test for the new categorization (§A), following the model of `overrides_registry_test.go`/the D1 protocol-enum test already present in the repo (`docs/studies/audit-2026-07.md` §D1) — every `Param` must have a non-empty `Category`, to prevent a new param from being added without a category.
- Dedicated Go test for the `userParams`/`expertUserParams` union/priority logic (§B) — don't just reuse the existing `ValidateUserOverrides` tests, add cases explicitly covering overlap between the two lists.
- Vitest test for the per-section rendering (grouped `ProtocolParamsForm`).
- Update `overrides.go:61` (description of `FieldProtocolParams`) and regenerate `docs/guacd-parameters.md` (`make docs-params`) if `Category` appears there.
- Dev environment: update at least one template in `hack/dev/templates-dev.yaml` to illustrate `expertUserParams` alongside `userParams`, so the split is manually testable.

## Open points (your call)

- Exact name of the new CRD field (`expertUserParams` as proposed by the initial request) — check that it doesn't collide with a term already used elsewhere in the overrides registry before locking it in.
- Precise semantics of "priority" in case of overlap (§B) — several valid interpretations, decide and document it clearly in the CRD field comment and in `overrides.go`.
- Does the "advanced" toggle stay global to the protocol or become per-section (§A) — both are defensible, the second option is more consistent with the grouping but more visual work.
  DECISION: organized by section, that does seem more coherent indeed
