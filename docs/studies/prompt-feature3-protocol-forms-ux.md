# Fable 5 Prompt — Feature 3: toggles for booleans + dropdowns for options with a default, in the protocol parameter forms

Paste this document as-is as an implementation prompt. It assumes you (Fable 5) have no prior conversation context.

## Repo context

WaaS drives VNC/RDP/SSH sessions via guacd. All guacd connection parameters (color, audio, clipboard, keyboard layout, etc.) come from a **single registry** on the Go side, `operator/pkg/params/params.go`: each `Param` has a `Kind` (`string`/`bool`/`int`/`enum`), a `Tier` (`ui`/`advanced`/`platform`), a `Default` (documents guacd's own default value, never sent as-is — an empty value means "let guacd apply its own default"), and for `enum` an `Enum []string`. This registry is exposed as-is to the frontend via `GET /api/v1/meta/protocols` and rendered by a single form component — **any change to this component automatically applies to every screen that displays protocol parameters**, not just Connection Settings (see "Scope" below).

## What already exists (know this before coding)

Per-parameter rendering is **entirely centralized** in `frontend/src/components/ParamField.tsx`:

```tsx
switch (meta.kind) {
  case 'enum': return <select>...<option value="">({meta.default})</option>...meta.enum.map(...)</select>;
  case 'bool': return <select>...<option value="">({meta.default})</option><option value="true">true</option><option value="false">false</option></select>;
  case 'int':  return <input type="number" placeholder={meta.default} .../>;
  default:     return <input placeholder={meta.default} .../>;  // string
}
```

- **`bool` is already a tri-state `<select>`** (empty = inherits the guacd default, `true`, `false`) — never a checkbox. It's this `<select>` that needs to become a visual toggle.
- **`enum` is already a dropdown** with the default as the first, empty option — nothing to do here, it's the model to follow for the rest.
- **`int`/`string` (without enum) only show the default as a `placeholder`** — grayed-out text that disappears as soon as the user types, and that doesn't visually distinguish "field empty = default applied" from "field empty = nothing typed yet".

`ParamField` is called by `ProtocolParamsForm` (`frontend/src/components/ProtocolTabs.tsx:163-219`), itself used by:

- `ConnectionSettingsDialog.tsx` (the explicit target of this feature),
- `CreateWorkspaceDialog.tsx` (protocol section at creation),
- `RemoteWorkspaceDialog.tsx` (remote machine parameters),
- `TemplatesPage.tsx` (admin template editor, plus the "user-overridable" checkbox per parameter),
- `SessionOverlay` (in-session settings).

**Scope — a decision to make with full awareness:** the request is about "the connection settings menus related to each protocol." Technically, `ParamField`/`ProtocolParamsForm` have no per-screen variant — changing the rendering of `Kind` applies everywhere the component is mounted. Our recommendation: treat this as an improvement to the shared component (so visible everywhere) rather than forking the rendering per screen — a `bool` parameter means the same thing everywhere, there's no reason for it to be a checkbox here and a select elsewhere. If you prefer to limit the effect to Connection Settings only, you'll need to add a variant prop to `ParamField`/`ProtocolParamsForm` and own it as an explicit choice (document it).

## What needs to be delivered

### A. Boolean parameters become toggles

Replace the `case 'bool'` rendering in `ParamField.tsx` with a visual toggle/switch component instead of the current `<select>`.

**Non-trivial point of attention**: the current `<select>` is **tri-state** (empty/true/false), not binary — "empty" explicitly means "no preference, guacd applies its own default" (e.g. `read-only` defaults to `false`, `disable-copy` defaults to `false`, `ignore-cert` on RDP defaults to `true`). A classic toggle is binary (on/off) and cannot natively represent this third "inherited" state. Two possible directions, for you to decide:

1. **3-position toggle** (segmented control: "Default" / "On" / "Off") — preserves the current semantics exactly, a bit more visual work. In that case it might be better to consider this a dropdown with the right values — what do you think?
2. **Binary toggle** whose initial visual state reflects the registry's `Default` (e.g. `disable-copy` defaulting to `false` → toggle visually OFF when the value is empty), plus a small "reset to default" affordance next to it (consistent with the existing `live` badge, `ParamField.tsx:85-89`) to explicitly return to the empty/inherited state rather than sending a hardcoded `"false"`. This is closer to the "toggle" intuition but requires clearly distinguishing visually "inherited = false" from "explicitly set = false" (otherwise information is lost: an admin who wants to _force_ `false` on a parameter whose guacd default is `true`, like `ignore-cert`, must be able to do so explicitly).

In both cases: change nothing about the value sent to the backend (always `""`/`"true"`/`"false"`), nor the `ParamMeta`/`meta.kind === 'bool'` contract on the server-side validation (`operator/pkg/params`, webhook, `api-server` Connect) — this is a rendering-only change.

### B. Parameters with a default become dropdowns

For every parameter with `kind !== 'enum'` whose `meta.default` is non-empty, replace the current `<input>`/`<input type="number">` (placeholder only) with a control that **lists the default as an explicitly selectable option**, following the model already used for `enum`/`bool` (`<option value="">({meta.default})</option>`).

Parameters actually concerned today (extracted from the registry, to give you the real scope before coding — don't invent them, re-read `operator/pkg/params/params.go` if the registry has changed):

- `int` with a default: `font-size` (6–48, default 12, SSH), `scrollback` (0–100000, default 1000, SSH advanced), `backspace` (1–255, default 127, SSH advanced).
- `string` with a default: `terminal-type` (default `linux`, SSH advanced). `clipboard-encoding` has a default but is already `enum`.

**Tension to arbitrate**: a pure `<select>` only makes sense over a finite, reasonable set of values. For `scrollback` (0–100000 range), listing every possible value in a dropdown is absurd. Our recommendation: **hybrid** dropdown — a dropdown that offers "(default: X)" as the first selectable choice and possibly a few common values, PLUS the ability to type a free value within the `min`/`max` bounds (for example an `<input list="...">` with a `<datalist>`, which keeps the native keyboard while providing a real dropdown list — or a small "select or custom" component with a "Custom…" option that switches to the numeric input). For `terminal-type` (free string), a real dropdown of common `TERM` values (`linux`, `xterm`, `xterm-256color`, `vt100`, `screen`) + a "Custom…" option is reasonable since guacd doesn't impose an enumeration but real-world usage is heavily concentrated on a handful of values.

Document your choice in the component (comment above the `meta.kind` switch, next to the existing comment that already explains the kind→widget mapping).

## Constraints to respect

- Zero contract change on the Go side (`operator/pkg/params`, webhook validation, `Connect` validation on `api-server`) — this feature is strictly a frontend rendering improvement on data already exposed by `GET /api/v1/meta/protocols`.
- `tsc -b` with no errors, `strict: true`, zero `any` (constraints already held across the whole repo, `docs/studies/audit-2026-07.md` §Frontend).
- Vitest tests on the new rendering (`ParamField.test.tsx` if the file already exists, otherwise create it next to `ParamField.tsx` following the convention of the repo's other component tests) — at minimum: a bool toggle correctly reflects `value`/`meta.default` in the 3 states (inherited/true/false), a field with a default properly exposes the "(default)" option and returns the right value to the parent.
- i18n: every new string (e.g. "Custom…", the 3-state toggle labels) goes through `frontend/src/i18n/locales/{en,fr}.json`.
- Don't forget `TemplatesPage.tsx`: the admin editor adds a slot per parameter (`renderParamExtra`, the "user-overridable" checkbox) — check that your new rendering doesn't break the layout there (columns, alignment) since you're touching a component shared by this screen too.

## Open points (your call)

- 3-state toggle vs. binary toggle + explicit reset for booleans (§A) — both are defensible, decide and document.
- Exact shape of the hybrid dropdown for `int`/`string` with a default (§B) — `datalist`, select+"Custom…", or other: choose the solution most consistent with the rest of the existing Tailwind design system (`fieldClass` in `ParamField.tsx:3-4`).
- Scope (shared component vs. per-screen variant) — recommendation above, to confirm or revise if you identify a screen where the new rendering would genuinely be inappropriate (e.g. `TemplatesPage.tsx` where the admin might want to force a genuinely free value without a dropdown).
