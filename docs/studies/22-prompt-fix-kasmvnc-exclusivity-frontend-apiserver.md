# Prompt Fable 5 — Fix: reflect `kasmvnc` exclusivity on the frontend and api-server

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context.

## Context: the rule already exists in the operator, nowhere else

The admission webhook (`operator/internal/webhook/v1alpha1/workspacetemplate_webhook.go:93-98`)
now rejects any `WorkspaceTemplate` that declares `kasmvnc` alongside
another protocol (`vnc`/`rdp`/`ssh`):

```go
// kasmvnc is exclusive: it bypasses guacd, and its generated-password
// mechanism and the vnc/rdp one both inject VNC_PW under the same
// pod-copy Secret name — only one connection stack per template.
if seen[string(waasv1alpha1.ProtocolKasmVNC)] && len(seen) > 1 {
    return nil, v.deny(tpl, "protocol kasmvnc cannot be combined with vnc/rdp/ssh: it bypasses guacd and must be the template's only protocol")
}
```

This rule **is replicated nowhere on the API/UI side**:

- `api-server/internal/service/template_service.go`, function
  `specFromInput` (the pre-check that exists precisely to prevent a
  k8s admission error from surfacing as a raw 500 to the admin — see
  the comment on line 285-287 about `kasmvncConfig`) **already
  replays** most of the webhook's guards (params registry, audio
  port, `defaults > 1`, `kasmvncConfig`) but **not** this one, and
  **not** the "protocol declared twice" guard either (webhook lines
  58-61, absent from `specFromInput`). Today, an admin who triggers
  either case via the API gets the raw k8s admission refusal instead
  of a clean `apierror.BadRequest`.
- The frontend (`frontend/src/pages/admin/TemplatesPage.tsx`) already
  builds `unusedProtocols` (line 242) so the "+ Add a protocol" menu
  (`ProtocolTabs.tsx:123-150`) never offers a protocol that's already
  configured — but nothing today prevents adding `kasmvnc` alongside
  `vnc`, nor adding `vnc` alongside `kasmvnc`. The only current
  safety net is the raw webhook error message displayed as-is at the
  bottom of the dialog (`TemplatesPage.tsx:285`:
  `{save.isError && <p ...>{save.error.message}</p>}`) — this already
  works as a fallback (no change required there), but this prompt aims
  to spare the admin the server round-trip needed to discover the
  rule.

## What needs to be delivered

### 1. api-server — mirror the two missing guards

In `api-server/internal/service/template_service.go`, function
`specFromInput`, loop over `in.Protocols` (lines 246-273):

- Add the "declared twice" guard, same scheme as the webhook
  (`seen := map[string]bool{}`, `seen[p.Name]` before adding):
  `apierror.BadRequest(fmt.Sprintf("protocol %q is declared twice", p.Name))`.
- After the loop, at the same spot as the `defaults > 1` check (line
  274-276) or right after, add the kasmvnc exclusivity guard, same
  condition and same message text as the webhook to stay consistent
  across the whole stack:
  `apierror.BadRequest("protocol kasmvnc cannot be combined with vnc/rdp/ssh: it bypasses guacd and must be the template's only protocol")`.

Use `waasv1alpha1.ProtocolKasmVNC` (already imported, cf. usage on
line 292) rather than a `"kasmvnc"` literal — follow the convention
already in place in this file (see line 261
`string(waasv1alpha1.ProtocolVNC)`, line 292
`string(waasv1alpha1.ProtocolKasmVNC)`).

Complete the comment on lines 243-244 ("Protocols: same registry gate
as the admission webhook") if needed so the list of mirrored guards
stays accurate — don't rewrite it entirely.

### 2. Frontend — prevent the combination at the picker level

In `frontend/src/pages/admin/TemplatesPage.tsx`, next to the
`unusedProtocols` calculation (line 238-242):

- If `kasmvnc` is already part of `protocols`, `unusedProtocols` must
  be empty (no other protocol can be offered).
- If `protocols` contains at least one protocol other than `kasmvnc`
  (i.e. `vnc`/`rdp`/`ssh`), `kasmvnc` must be removed from
  `unusedProtocols` even if it's present in the registry
  (`availableProtocols`).
- `vnc`+`rdp`+`ssh` must remain freely combinable with each other,
  with no behavior change.

Implement this as an additional filter on `unusedProtocols` (not a
separate new validation function called on submit — the existing
pattern for "only one default" and "no duplicate" is already
structural, do the same here rather than adding a validator called in
`onSubmit`). Keep the `'kasmvnc'` literal — it's already the pattern
used in this file (`DEFAULT_PORTS` line 58 already excludes it the
same implicit way, `protocols.some(p => p.name === 'kasmvnc')` line
596-626); there's no TS type listing known protocols (confirmed:
`WorkspaceProtocol.name` and `TemplateProtocolInput.name` are plain
`string`s, protocols are driven by the `/api/v1/meta/protocols`
registry), so no new abstraction to introduce for a single special
case.

Don't touch `frontend/src/components/ProtocolTabs.tsx`: it's a shared
component (also used by
`frontend/src/dialogs/ConnectionSettingsDialog.tsx`,
`RemoteWorkspaceDialog.tsx` and `CreateWorkspaceDialog.tsx`), and
`kasmvnc` is never offered by `RemoteWorkspaceDialog.tsx` anyway (its
`unused` list is hard-wired to `['ssh', 'vnc', 'rdp']`, line 51 —
remote workspaces don't support kasmvnc, that stays out of scope
here). The filtering must therefore live only in `TemplatesPage.tsx`,
specific to the template form.

**Don't handle the "existing template already in violation" case on
the UI side**: a pre-existing template that already combines
`kasmvnc` and a guacd protocol (grandfathering, cf. the previous
operator prompt) will display normally when edited — the filter only
applies to *addable* protocols, it doesn't remove anything from the
already-loaded list. If the admin saves without touching it, the
webhook will block it at `ValidateUpdate` and the raw error will
surface via `save.error.message` (`TemplatesPage.tsx:285`, already in
place, nothing to change). That's the intended behavior, not a bug to
fix.

### 3. i18n — spell out the rule in the existing help text

`frontend/src/i18n/locales/en.json:271` (`protocolsHint` key, under
the form's "Protocols" legend) and its mirror
`frontend/src/i18n/locales/fr.json:271`: the current text only
mentions the guacd-centric behavior. Add a sentence stating that
`kasmvnc` bypasses guacd and cannot coexist with any other protocol
(rejected at admission) — same angle as the doc already updated in
`docs/templates-and-protocols.md` by the previous operator prompt
(check its exact wording to stay consistent, "Protocols" section,
lines ~38-63).

## Constraints

- Don't duplicate guards that are already correct (`defaults > 1`,
  audio port, `kasmvncConfig`) — this prompt strictly adds the two
  missing api-server guards and the frontend filtering, nothing else.
- api-server error messages must remain text-identical to the webhook
  (same substring `"cannot be combined with vnc/rdp/ssh"` /
  `"is declared twice"`) so a future cross-layer consistency test
  remains possible and the admin sees the same vocabulary everywhere.
- `vnc`+`rdp`+`ssh` combined (without `kasmvnc`) must continue to pass
  unchanged, both on the api-server and frontend sides — check that no
  existing test on this case breaks.
- Don't introduce a new component, hook, or type just for this case —
  the `TemplatesPage.tsx` file already has the right context
  (`protocols`, `availableProtocols`) in the right place.

## Tests

- `api-server/internal/service/template_service_test.go`: add a
  dedicated test function (same style as
  `TestTemplateInputValidatesExposeAudioPort` line 10 and
  `TestTemplateInputValidatesKasmVNCConfig` line 61, reusing or
  adapting the same `base(...)` helper) covering:
  - `kasmvnc` + `vnc` → rejected, message containing `"cannot be combined"`.
  - `kasmvnc` + `rdp` → rejected.
  - `kasmvnc` + `ssh` → rejected.
  - `kasmvnc` + `vnc` + `rdp` + `ssh` → rejected.
  - `kasmvnc` alone → accepted.
  - `vnc` + `rdp` + `ssh` (no kasmvnc) → accepted.
  - a protocol declared twice (e.g. `vnc` + `vnc`) → rejected,
    message containing `"declared twice"`.
- `frontend/src/pages/admin/TemplatesPage.test.tsx`: extend the file
  (the `base(protocol, kasmvncConfig?)` helper at line 23 only builds
  a single protocol — you'll need to either extend it to accept
  several protocols, or build the input directly in the new test)
  with a new `describe` covering:
  - with `kasmvnc` already configured, the "+ Add a protocol" menu
    must offer neither `vnc`, `rdp`, nor `ssh` (or doesn't appear at
    all if those were the only protocols in the mocked registry).
  - with `vnc` already configured, the menu must not offer `kasmvnc`
    (but may offer `rdp`/`ssh`).
  - mock `/api/v1/meta/protocols` with at least `vnc`, `rdp`, `ssh`,
    `kasmvnc` for these tests (the current mock at line 12-16 returns
    `[]` by default — pass an explicit list in the new tests without
    changing the file's global mock for the other tests).
- `go build ./...`, `go test ./api-server/...`,
  `cd frontend && npm test` (or whichever vitest command is already in
  place in this repo).

## Open points (your judgment call)

- If you prefer exposing an explanatory tooltip on the "+" when no
  protocol can be offered because of this rule (rather than the
  currently silent, invisible menu when `unusedProtocols` is empty,
  cf. `ProtocolTabs.tsx:123` `{onAdd && (addable?.length ?? 0) > 0 && ...}`),
  document your choice — this isn't explicitly requested here, the
  sentence added to `protocolsHint` (point 3) is considered sufficient
  by default.
- Exact location of the "declared twice" guard within `specFromInput`
  (inside the loop vs. via a map built beforehand) — both work, pick
  whichever reads best next to the `defaults` count already
  accumulated in the same loop.
