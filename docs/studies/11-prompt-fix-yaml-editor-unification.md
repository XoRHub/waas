# Fable 5 Prompt — Fix: a single YAML component, the one from the "workload (advanced)" field is authoritative

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context.

## Repo context

The frontend today has **three different implementations** for
"display/edit YAML," with no convergence:

1. **`YamlEditor`** (`frontend/src/components/YamlEditor.tsx:86-161`) —
   the most polished component: a transparent textarea overlaid on a
   mirror with line-by-line syntax highlighting (`highlight()`,
   L42-78), a line-number gutter, highlighting of lines in error, and
   live validation via `parseYaml()` (L21-37, AST parse with
   the `yaml` lib, `YamlIssue[]` anchored to the line + a `validate?`
   callback for semantic validation on top of syntax). Used today at
   only two places: the "workload (advanced)" field of
   `TemplatesPage.tsx` (section commented `/* ----------------
workload (advanced) ---------------- */`, L749-765, with
   `validateWorkload` L23-34 which checks that it's a YAML mapping and
   that `kind` is `Deployment`/`StatefulSet`/`Pod`) and the policy field of
   `GovernancePage.tsx:488`. **Don't touch these two usages**, they
   are already the reference.
2. **A raw `<textarea>`** for the admin `kasmvncConfig` field, **in
   the same file** `TemplatesPage.tsx:606-613` — no highlighting,
   no gutter, no live validation, while 150 lines further down this
   same file uses `YamlEditor` for the workload field.
3. **`KasmVNCConfigView`** (`frontend/src/components/ProtocolTabs.tsx:182-209`)
   — the user-facing read-only view: a plain
   `<pre className="max-h-48 overflow-auto ...">{config}</pre>`
   (L200-203), no highlighting or numbering. Two variants
   `'template' | 'effective'` only drive the help text
   above (L196-198), not the rendering of the block itself. Used in
   `CreateWorkspaceDialog.tsx:447-451` (variant `'template'`, raw text
   of the template, the workspace doesn't exist yet) and in
   `ConnectionSettingsDialog.tsx:204-208` (variant `'effective'`, the
   merged content read via `useWorkspaceKasmVNCConfig`, cf.
   `docs/studies/10-prompt-feature12-kasmvncconfig-admin-default-merge.md`
   for the 3-layer merge on the controller side).

**Decided**: `YamlEditor` is the authoritative component. Usages 2 and
3 must converge on it — not the other way around, and not a fourth
implementation.

## What needs to be delivered

1. **Add a read-only mode to `YamlEditor`** (`YamlEditor.tsx`): a
   `readOnly?: boolean` prop that sets the HTML `readOnly` attribute on
   the `<textarea>` (L135-147) — keep native selection/scroll/copy-
   paste, just block typing. The `onChange` prop stays required in the
   signature (no API refactor), read-only callers will pass a
   no-op (`() => {}`) — that's the simplest choice, don't complicate the
   signature for a case that doesn't need to be optional. Keep the
   gutter and highlighting active in read-only mode (that's the whole
   point of switching to this component); the error panel
   (L149-158) only shows if the caller passes a `validate`, which will
   remain the default for the two read-only views below (no `validate`
   passed = no panel, unchanged behavior for them).
2. **Admin `kasmvncConfig` (`TemplatesPage.tsx:606-613`)**: replace the
   `<textarea>` with `<YamlEditor value={input.kasmvncConfig ?? ''}
onChange={(text) => set({ kasmvncConfig: text })} rows={8} validate={...} />`.
   Write `validateKasmVNCConfig` on the same model as
   `validateWorkload` (L23-34) but lighter: only check that
   the parsed content is a YAML mapping (an object, not a scalar or a
   list) when the text isn't empty — **don't duplicate** the
   validation of the 3 forbidden clipboard-DLP keys (webhook
   `workspacetemplate_webhook.go:150-167`): Feature 12 already settled
   this point ("no client-side duplicated validation, just don't
   surprise the admin about the error response") — the webhook already
   returns an explicit message on any attempt, leave this error path
   as-is.
3. **User read-only view (`KasmVNCConfigView`,
   `ProtocolTabs.tsx:182-209`)**: replace the `<pre>` (L200-203) with
   `<YamlEditor value={config} onChange={() => {}} readOnly rows={...} />`,
   inside the same `if (config.trim() !== '')` — keep the italic
   "empty" message (L204-205) unchanged for the empty case, don't
   pass an empty string into `YamlEditor` for that case. Compute
   `rows` dynamically from the content's line count, clamped
   to stay visually close to the old `max-h-48` (~12rem; with
   `lineHeight: 1.25rem` in `YamlEditor`, that corresponds to about 9-10
   visible lines before internal scroll) rather than an arbitrary
   fixed line count — a template with 3 lines of config shouldn't
   display an empty 10-line frame.
4. The two help texts per `variant` (L196-198) and the title
   (L192-194) remain unchanged — only the content rendering area
   changes component.

## Constraints

- Don't add a new dependency (no Monaco/CodeMirror) — the
  CSP forbids CDNs, `YamlEditor` is already 100% local, that's
  precisely why it's the reference.
- Don't touch the two usages already on `YamlEditor` (`TemplatesPage.tsx`
  workload, `GovernancePage.tsx`).
- i18n: no new string expected (the existing help texts don't
  move) unless you add an error message for
  `validateKasmVNCConfig` — in that case, a key under
  `admin.templatesPage.*` in `en.json`/`fr.json`, like the rest of the
  file.

## Tests

- `YamlEditor.test.ts`: the `readOnly` mode does block typing
  (the attribute is set) without breaking the highlighting/gutter rendering.
- `TemplatesPage.test.tsx`: the `kasmvncConfig` field stays
  functional in a save/reload round-trip once migrated onto
  `YamlEditor` (the existing test probably typed into a
  `<textarea>` by role/testid — adapt the selector, don't remove
  the test).
- `ProtocolTabs.test.ts` (or a new test if none exists for
  `KasmVNCConfigView`): the non-empty rendering does go through `YamlEditor`
  in read-only mode (no `onChange` triggered by a simulated user
  keystroke), the empty rendering keeps the italic message.
- `tsc -b` on `frontend`.

## Open points (your arbitration)

- Exact default `rows` count for the read-only view (precise clamp
  formula) — give priority to not displaying a frame
  disproportionate to the actual content. => arbitration, 7 rows, but the user
  must be able to scroll inside it
</content>
