# Fable 5 Prompt — Feature 12: `kasmvncConfig` editable from the admin panel, with a merged platform default (explicit wins)

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context.

## Repo context

`WorkspaceTemplate.spec.kasmvncConfig` (`operator/api/v1alpha1/workspacetemplate_types.go:327-339`)
is an opaque YAML string, never parsed against a schema — materialized
as a ConfigMap and mounted read-only at
`<homeMountPath>/.vnc/kasmvnc.yaml`
(`operator/internal/controller/kasm_config.go`). Feature 11
(`docs/studies/08-prompt-feature11-kasmvnc-governance-gap.md`, delivered
2026-07-10) added a partial merge: the controller unconditionally stamps
3 clipboard DLP keys derived from
`WorkspacePolicy.Clipboard` over the admin's `kasmvncConfig`
(`applyClipboardPolicy`, `kasm_config.go:80-104`), and the validation
webhook refuses to let an admin write these 3 keys themselves
(`workspacetemplate_webhook.go:93-113,146-167`).

This prompt fixes two gaps not addressed by Feature 11:

1. **No admin form exists for this field.** The backend plumbing is
   complete end to end — CRD → `TemplateInput`
   (`api-server/internal/service/template_service.go:53-55,220,364`) →
   `model.WorkspaceTemplate` (`api-server/internal/model/model.go:307-308`)
   → `types.gen.ts:357-359` — but `frontend/src/pages/admin/TemplatesPage.tsx`
   references `kasmvncConfig` **nowhere**: today it's a gitops/kubectl-only
   field (confirmed in `kasm-images-feasibility.md` line ~236, Feature 11
   note).
2. **No platform default exists.** If the admin leaves
   `kasmvncConfig` empty, the effective ConfigMap contains ONLY the
   3-key clipboard DLP block — no baseline configuration
   (resolution, name, etc.) is ever applied. The CRD field comment
   already lies about this: *"Empty = no mount, the image
   default applies"* (`workspacetemplate_types.go:336`) — this has been false
   since Feature 11, the mount is **unconditional** as soon as a
   `kasmvnc` protocol is declared (`workload.go:94-121`,
   `kasmConfig` is never `""` for a kasmvnc template since the
   clipboard block is always stamped onto it). This prompt makes this comment
   true by giving it real content, instead of simply fixing it.

## What already exists (to know before coding)

- **`applyClipboardPolicy(rawConfig, copyAllowed, pasteAllowed)`**
  (`kasm_config.go:85-104`): parses `rawConfig` into a `map[string]any`,
  stamps 3 paths via `setNested` (`kasm_config.go:110-121`, overwrites
  any non-map value encountered on the path), remarshals. Two
  independent call sites do exactly the same computation:
  `ensureKasmConfig` (`kasm_config.go:126-173`, produces the ConfigMap)
  and the volume construction in `workload.go:94-121` (computes the
  hash that rolls the pod). Keep this duplication in mind — the 3-layer
  merge you're introducing must remain identical in both places.
- **Webhook**: `tpl.Spec.KasmVNCConfig != "" && !seen[kasmvnc]` → refusal
  (`workspacetemplate_webhook.go:95-96`); `policyManagedClipboardKeys`
  (`:150-167`) refuses to let the admin write the 3 clipboard-DLP keys
  or `runtime_configuration.allow_client_to_override_kasm_server_settings`
  themselves.
  **Don't change this logic** — the platform default introduced here
  must also never write these keys other than through
  `applyClipboardPolicy` (otherwise the webhook and the controller diverge).
- **User-facing mount is already read-only**: in `workload.go`, the
  ConfigMap's `VolumeMount` has `ReadOnly: true` (subPath on the home
  volume). This is already the state requested by *"on the user side
  the ConfigMap is RO only, informational"* — don't regress it, there's
  nothing to add here, just verify that none of the tasks below introduces
  a user write (API or UI) on this field.
- **Protocol-conditional field pattern in the admin editor**:
  `exposeAudioPort` is already a field conditioned on a specific protocol
  in `TemplatesPage.tsx` (`currentProto.exposeAudioPort`,
  line ~491-492, passed to `ProtocolParamsForm`). `kasmvncConfig` is instead
  a **template-level** field, not per-protocol (a single
  string in `WorkspaceTemplateSpec`, not in `WorkspaceProtocol`) —
  the pattern to reuse is therefore the validation-side guard
  (`kasmvncConfig requires a kasmvnc protocol entry`), not the
  per-protocol state mechanism of `exposeAudioPort`.

## What needs to be delivered

### A. A platform default layer, merged at reconcile time — NOT persisted in the CR

**Decided**: the default lives as a **Go constant**, applied
only at the moment the controller computes the effective content
(same place as the current clipboard stamp) — neither a CRD schema
default (`+kubebuilder:default`), nor a mutating webhook that would write
the default into `spec.KasmVNCConfig` at creation time. Reason to document in
the commit: a default materialized in the CR (CRD or mutating webhook)
freezes into each template at the time of its creation — a later
change to the platform default would never again reach templates
already created, and every GitOps template would carry the full default
blob even when the admin has nothing to override, which breaks the
"the CR only carries what deviates" model. The merge at reconcile time, on the
other hand, propagates a default change to every template that hasn't
overridden the key in question, via the same hash/rollout mechanism that
already exists (`annotationKasmConfigHash`).

1. **Decided — content source**: `defaultKasmVNCConfig` must
   reproduce the default `kasmvnc.yaml` **exactly as shipped by the
   kasmweb/\* image itself**, not a made-up selection of directives. In
   a live session (k3d dev, cf.
   `docs/studies/02-prompt-feature8-makefile-dev-bootstrap.md` for the
   bootstrap), start a kasmvnc workspace and fetch the container's default
   config file (likely `/etc/kasmvnc/kasmvnc.yaml`
   and/or `~/.vnc/kasmvnc.yaml` before any WaaS mount — check which one
   is authoritative; the official doc cited in Feature 11
   (`kasmweb.com/kasmvnc/docs/latest/configuration.html`) documents the
   file hierarchy in case of ambiguity). Copy it verbatim as a Go
   constant rather than guessing or reducing it to a "minimal"
   subset — it's a file already validated by the upstream editor, reproducing
   it faithfully is less risky than extracting a partial
   excerpt. The clipboard-DLP block it may contain doesn't
   need to be manually removed: layer 3 (policy) overwrites it anyway
   on the 3 managed keys (§2 below), regardless of its
   content in layer 1.
2. Replace `applyClipboardPolicy` with a **3-layer** merge, in
   this increasing priority order:
   `defaultKasmVNCConfig` (base) → the admin's `tpl.Spec.KasmVNCConfig`
   (overrides **key by key**, not a replacement of the entire document —
   a key absent from the template inherits the default, a key present in
   both wins on the template side) → the 3 clipboard-DLP keys derived from
   the policy (always forced last, unchanged behavior).
3. Write the merge as a generic recursive function over
   `map[string]any` (two input maps, the second winning key by key
   when descending into sub-maps; any non-map value — scalar
   or list — in the priority layer entirely replaces the corresponding
   value in the base layer, no list merging). Keep the
   explicit style already in place (`setNested`, no external
   deep-merge library). Rename/reorganize `applyClipboardPolicy` as
   you like as long as the two call sites (`kasm_config.go:143`,
   `workload.go:104`) use the same function with the 3 layers
   in the same order.
4. Verify that a template whose `kasmvncConfig` is empty now
   gets a ConfigMap containing the default + the clipboard block (not
   just the clipboard block as today), and that a template
   that only overrides a single default key (e.g. just the resolution)
   keeps the rest of the default intact — this is the behavior that
   the current CRD field comment already wrongly claims.

### B. The rollout must follow the reload routine already in place — no new mechanism

A change to `kasmvncConfig` (or to the platform default introduced in
§A) must roll the affected pods following **exactly** the
already-wired circuit, without adding a parallel one:

1. `WorkspaceReconciler.SetupWithManager` already watches
   `WorkspaceTemplate` (`workspace_controller.go:1040-1041`,
   `Watches(&waasv1alpha1.WorkspaceTemplate{}, ...mapTemplateToWorkspaces)`):
   any template edit (hence any `kasmvncConfig` edit) already re-enqueues
   every workspace derived from it (`mapTemplateToWorkspaces`,
   `workspace_controller.go:1057-1073`, filters on `spec.templateRef`).
2. The merged content (§A) already feeds `annotationKasmConfigHash`
   on the pod template (`workload.go`, right after computing
   `kasmConfig`) — different content changes the hash, changes
   the annotation, hence changes the pod template, hence triggers the rollout
   of the Deployment/StatefulSet via the generic mechanism already in place
   (not specific to kasmvnc).
3. **Your task here is therefore NOT to add a watch or a trigger**:
   it's to verify, with an integration/reconcile test (or in a
   live session), that moving to 3 layers (§A) hasn't broken anything in
   this chain — in particular that the hash does change when only the
   default layer changes (e.g. a future bump of `defaultKasmVNCConfig`)
   AND when only the admin layer changes, not just when the
   policy layer changes (the only case already tested today). If you
   find that the 3-layer merge introduces a case where the hash doesn't
   move even though the effective content has changed (e.g. a key-ordering
   bug in the remarshaled YAML producing an unstable hash), that's
   a bug to fix within this task, not a new mechanism to
   build.

### D. Editable field in the admin panel (`TemplatesPage.tsx`)

1. Add a `<textarea>` for `kasmvncConfig` in the template
   form, disabled/hidden as long as no `kasmvnc` protocol is
   present in `protocols` (same guard as the webhook — doesn't depend
   on `activeProto`, it's a test on the entire list of protocols
   of the template, not on the active tab).
2. **Decided — help text content** (i18n, `en.json`/`fr.json`,
   keys under `admin.templatesPage.*` like the rest of the file). It
   must cover two points, no more:
   - **Propagation**: this field is merged (key by key, the template
     wins over the platform default) then propagated as-is into the
     user's workspace — materialized as a ConfigMap and mounted
     read-only in the container; the 3 clipboard-DLP keys remain
     refused here and derived from the policy (the webhook already
     returns an explicit message on any attempt — no client-side
     duplicated validation, just don't surprise the admin about the
     error response).
   - **Link to the docs**: reference the official KasmVNC documentation
     of available directives (`kasmweb.com/kasmvnc/docs/latest/configuration.html`,
     already cited in Feature 11) directly in the help text or in a
     link next to the textarea, so the admin knows where to look for
     valid key names without guessing.
3. **Decided — no merge preview in the UI.** The textarea remains
   the editor of the raw override layer, not a rendering of the
   final merged state; this isn't requested and would add a surface (it
   would require calling the controller or duplicating the merge on the
   api-server/frontend side). If you find it's trivial once §A
   is done (e.g. an endpoint that just exposes the merged YAML read-only),
   document it as an option rather than implementing it without being
   asked to.

### E. Fix comments that became false

- `workspacetemplate_types.go:336`: replace *"Empty = no mount, the
  image default applies"* with a description faithful to the 3-layer
  merge introduced in §A (empty = the platform default + the policy
  stamp apply; non-empty = key-by-key override on top of the
  same default).
- `kasm_config.go:20-39` (package comment at the top of the file):
  mention the 3rd layer (platform default) in addition to the two
  already documented (admin config + clipboard policy).

### F. Tests

- Go, on the merge function: empty admin → default + clipboard
  only; admin overriding just one key → rest of the default preserved;
  admin defining a key absent from the default → kept as-is;
  priority order verified on a case where both default AND admin define the
  same key (admin wins) then where both policy AND admin target the same
  clipboard key (already forbidden at admission by the webhook — test that the
  merge at reconcile stays defensively correct should an
  invalid object already exist in storage prior to a webhook hardening,
  without making it a new API guarantee).
- Go: both call sites (`ensureKasmConfig`,
  volume construction in `workload.go`) produce identical content
  for the same inputs (avoids the duplication regression
  mentioned in "What already exists").
- Go, on §B (rollout): a change to `defaultKasmVNCConfig` alone
  (simulable in a test by injecting a variant) and a change to
  `tpl.Spec.KasmVNCConfig` alone each produce a different
  `annotationKasmConfigHash` — not just a clipboard policy
  change, the only case covered before this prompt.
- Vitest: the `kasmvncConfig` textarea appears/disappears depending on the
  presence of the `kasmvnc` protocol in the list; round-trip
  save/reload of the field; i18n keys present in both locales.

## Constraints to respect

- The platform default is **never** persisted into
  `spec.KasmVNCConfig` — neither via a mutating webhook, nor via a CRD
  schema default. This is the central arbitration of this prompt, don't relitigate it.
- The 3 clipboard-DLP keys remain forbidden to the admin in
  `kasmvncConfig` (webhook unchanged) and remain stamped last,
  after the default AND after the admin override.
- Don't touch the guacd path — this prompt is strictly kasmvnc.
- `go build ./...` + Go tests on `operator` (and `api-server` if you
  touch the DTO, which shouldn't be necessary — the plumbing
  is already complete there); `tsc -b` + vitest tests on the frontend.
- Complete i18n (`en.json` and `fr.json`) for any new string.
- Regenerate `docs/guacd-parameters.md` only if you touch it
  (a priori not concerned by this prompt, `kasmvncConfig` stays outside the
  `params.go` registry).

## Open points (your arbitration)

The substantive arbitrations are settled above (default source,
merge priority, non-persistence in the CR, reused rollout
routine, help text content). Only one implementation choice
with no behavioral impact remains:
- Exact name of the 3-layer merge function and file location
  (`kasm_config.go` vs. new `kasm_defaults.go`) — choose whatever
  seems most consistent with the current organization of the
  `controller` package (a priori: a new file if the
  `defaultKasmVNCConfig` constant is large, otherwise stay in
  `kasm_config.go` next to the function it feeds).
</content>
