# Prompt Fable 5 — Audit 2: order 10 (C15, C17) + findings C16, C18

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context.

## Source

`docs/studies/20-report-audit2-organisation-doublons-securite.md` is
the full audit report (2026-07-11). Read it first in its entirety,
especially the §4 findings table (rows C15 through C20) and the §5
action plan — this prompt saves you from redoing the file:line
research for each finding, but §4 gives the full context and reasoning
behind each one.

Line numbers cited below date from when this prompt was written
(2026-07-12) — check them before editing, they may have shifted by a
few lines since (`23-prompt-audit2-remediation-small-batch.md` was
implemented right before this one and may have touched adjacent
files).

## Scope of this prompt

The action plan (§5) places **C15 and C17 in order 10** ("the two real
frontend test/refactor efforts, in order of experienced risk", effort
M each) and files **C16 and C18** under the plan's last line, "along
the way", with a "Debatable" verdict — no dedicated effort in the
report's original intent. This prompt groups them into one explicit
effort anyway, but **respects the nuance the report gives C16 and
C18** rather than forcing uniform treatment:

- **C15** (`DesktopPane.tsx` into tested hooks) and **C17**
  (`TemplatesPage.tsx` split up) are handled in full, in this order
  (the report specifies it: "in order of experienced risk").
- **C16**: only `GovernancePage.tsx` is handled here — that's exactly
  what the report says ("GovernancePage yes … the other 3 after C15,
  along the way of future edits"). `SplitViewPage.tsx`,
  `ProfilePage.tsx`, `UsersPage.tsx` stay out of this prompt's scope,
  deliberately — don't touch them.
- **C18**: handled as a mechanical extraction of responsibility (not a
  redesign), following the pattern already established in the repo
  (`workload.go`/`placement.go` on the operator side,
  `workspace_events.go`/`workspace_resize.go` on the api-server side)
  — see the dedicated section for the exact scope.
- **Out of scope**: C25 (a11y), C26 (guacamole-common-js doc), C27
  (OIDC against a real IdP) — these are other "along the way" lines of
  the plan, not requested here, don't handle them.

One commit per section (C15, C17, C16, C18), never a catch-all commit
— these are changes of a different nature (testable frontend refactor,
page split, added tests, backend extraction).

---

## C15 — `DesktopPane.tsx`: extract the logic into testable hooks

`frontend/src/components/DesktopPane.tsx` is 376 lines, including a
**single ~240-line `useEffect`** (lines 100-339) mixing: the
Guacamole/WebSocket tunnel connection, the KasmVNC iframe branch,
desktop→browser clipboard (`client.onclipboard`, lines 206-228),
browser→desktop clipboard (paste/focus listeners, lines 230-271),
mouse (276-299), keyboard (301-305), and resize (`ResizeObserver` +
`createSessionResizer`, lines 307-315). This is the file where the
last 3 clipboard fixes were manually verified for lack of a safety
net (repo memory: fixes 13/14).

The pattern to follow already exists partially in the repo, but only
for *pure* logic:

- `frontend/src/lib/clipboard.ts` (56 lines, tested by
  `clipboard.test.ts`) exports `ClipboardSync` — echo-guard
  deduplication between what the client just sent/received. The
  actual wiring (DOM event listeners, `navigator.clipboard`
  read/write, hookup to the Guacamole `client`) stays inline in the
  `useEffect`, not testable in isolation.
- `frontend/src/lib/sessionResize.ts` (52 lines, tested by
  `sessionResize.test.ts`) exports `createSessionResizer(...)` —
  debounce + resize POST. The React wiring (`ResizeObserver`,
  lifecycle) stays inline (line 307-315).

`frontend/src/hooks/` already exists (`useApi.ts`, `useEscape.ts`,
`useEvents.ts`, `useProtocolSwitch.ts`) but contains no desktop-pane
related hook.

### What's expected

Extract two custom hooks, exactly as named by the report:

1. **`frontend/src/hooks/useClipboardBridge.ts`** — encapsulates the
   logic of lines 206-271 (both clipboard directions) building on the
   already-existing `ClipboardSync` rather than duplicating it.
   Signature is up to you, but it must at minimum take the Guacamole
   client as input (or a minimal interface covering
   `onclipboard`/`createClipboardStream` — don't expose the whole
   `Guacamole.Client`, just what clipboard needs, to keep the hook
   testable without mocking the entire object) and return what
   `DesktopPane` needs to keep working (e.g. a
   `setClipboard`/`sendClipboard` function if `useImperativeHandle`
   (lines 79-98) still depends on it after extraction — check what
   `useImperativeHandle` exposes today and preserve that external
   contract, don't break the `ref`'s API).
2. **`frontend/src/hooks/useSessionResize.ts`** — encapsulates the
   wiring at lines 307-315 (`ResizeObserver` + lifecycle of
   `createSessionResizer`) reusing `createSessionResizer` from
   `lib/sessionResize.ts` without duplicating its debounce logic.

For each: write a test file (`*.test.ts` or `.test.tsx` depending on
whether the hook needs the DOM) using `@testing-library/react`
`renderHook` (check that the dependency is already used elsewhere in
the repo — otherwise look at how the existing hooks in
`frontend/src/hooks/` are already tested, if they are, to stay
consistent with the local pattern). Test at minimum: clean mount/
unmount (cleanup called), and for clipboard the echo-guard behavior
already covered by `ClipboardSync` but now exercised through the hook
rather than re-testing it twice.

**What you must NOT do**: don't try to also extract the connection
(tunnel, `Guacamole.Client`, mouse, keyboard) into a hook — the report
only names `useClipboardBridge` and `useSessionResize`, and the
connection itself is riskier to isolate (state shared with the rest of
the effect). Keep it inline in `DesktopPane.tsx` for this prompt.

### Verification

- `cd frontend && npm run typecheck && npm test`.
- Unchanged observable behavior: `DesktopPane` must keep exposing
  exactly the same `ref` API (`disconnect`, `reconnect`,
  `setClipboard`, `sendClipboard`, `readRemoteClipboard`,
  lines 79-98) — if you change this surface, document why in the
  commit.
- If you have a k3d dev environment available (`hack/dev/`), a quick
  manual test of clipboard + resize on a real VNC/RDP session is
  recommended (the Guacamole canvas doesn't verify well via headless
  screenshot — repo memory of previous attempts); otherwise unit tests
  + `npm run typecheck` are sufficient for this prompt.

---

## C17 — `TemplatesPage.tsx`: split by section, on the PortalPage model

`frontend/src/pages/admin/TemplatesPage.tsx` is **896 lines**. The
list (`TemplatesPage`, lines 65-165) is reasonable; most of the weight
is `TemplateDialog` (lines 205-795, **591 lines**), a multi-`fieldset`
form: identity (314-380), description (382-392), resources (394-424),
protocols (426-609, the densest section, with
`ProtocolTabs`/`ProtocolParamsForm`), conditional kasmvncConfig
(611-641), env vars (643-666, with the local `EnvRow` sub-component at
the end of the file, lines 799-896), user overrides (668-707),
placement (709-765), schedule (767-773), advanced YAML workload
(775-792).

The precedent to replicate is `PortalPage` (July): the page was
reduced from 1617 to **100 lines**
(`frontend/src/pages/PortalPage.tsx`), the rest extracted into
`frontend/src/sections/` (`QuotaBanner.tsx` 62 l.,
`RemoteWorkspacesSection.tsx` 131 l., `VolumesSection.tsx` 75 l.,
`WorkspacesSection.tsx` 182 l.) — PortalPage imports these sections and
passes them its props/callbacks, keeping the "active tab"/"which
dialog is open" state at the page level.

### What's expected

Split `TemplateDialog` into sub-components, one per `fieldset` (or a
logical grouping if two `fieldset`s are too coupled to separate
cleanly — your judgment call, document it if you group them).
Obvious candidates given the structure above: identity+description,
resources, protocols (probably the biggest, keep it alone),
kasmvncConfig, env vars (with `EnvRow`), overrides, placement,
schedule, advanced workload.

Location: unlike `PortalPage`, `frontend/src/sections/` looks like a
folder dedicated to composing the portal page (4 files, all named
around the portal domain) — don't mix `TemplatesPage`'s sections into
it. Create a local folder, e.g.
`frontend/src/pages/admin/templates/` (your call on the exact name,
but stay consistent with the naming convention already observed on
the backend side for this same kind of extraction — one file per
responsibility, explicit name).

Constraints:

- The state (`input`, `workloadText`, `activeProto`, the
  `set`/`patchActive`/`addProtocol`/`removeProtocol` helpers, lines
  222-269) remains the single source of truth in `TemplateDialog` —
  sub-components receive their values and callbacks as props, they
  don't duplicate local state for the same data (same logic as
  `WorkspacesSection`/`VolumesSection` receiving their callbacks from
  `PortalPage`).
- `onSubmit` (271-284) and validation (`validateWorkload`,
  `validateKasmVNCConfig`, lines 24-46) stay in `TemplateDialog` or in
  a shared module if you prefer, but don't move them into a
  sub-component in a way that would make the submission flow less
  readable.
- `TemplatesPage.test.tsx` (174 lines) already exists — make it pass
  without a deep rewrite if possible (it likely tests observable
  behavior, not internal structure); add targeted unit tests on at
  least the 2-3 most complex sub-components you extract (protocols,
  kasmvncConfig) following the same harness as existing page tests
  (`renderWithProviders`/`signIn`/`createApiMock`, see
  `FleetPage.test.tsx` or `TemplatesPage.test.tsx` for the pattern).

### Verification

- `cd frontend && npm run typecheck && npm test`.
- Strictly identical form behavior (same fields, same validations,
  same submitted payload) — this is a split, not a functional
  rewrite.

---

## C16 — Tests for `GovernancePage.tsx` (the only page handled in this prompt)

`frontend/src/pages/admin/GovernancePage.tsx` is 546 lines, 0%
coverage, no `*.test.tsx` file. It composes 3 imported sub-sections
(lines 55-57, exact names to check in the file at execution time):
catalog (enable/disable images), policies (raw YAML editing), usage
(per-user consumption). It's the only one of the 4 zero-coverage pages
the report explicitly prioritizes: "it edits the policy, a silent
regression touches governance" — the other 3 (`SplitViewPage`,
`ProfilePage`, `UsersPage`) are **out of this prompt's scope**, don't
touch them.

### What's expected

Write `frontend/src/pages/admin/GovernancePage.test.tsx`, with the
same harness as the existing admin page tests
(`renderWithProviders`/`signIn`/`createApiMock`, direct model:
`FleetPage.test.tsx` or `TemplatesPage.test.tsx`). Before writing the
tests, read `GovernancePage.tsx` in full (and the 3 sub-components it
imports — check whether they live in the same file or are already
separate) to identify the real flows; cover at minimum:

- Initial render of the 3 sections with data mocked via
  `createApiMock`.
- Catalog: enabling/disabling an image triggers the right mocked API
  call.
- Policies: editing + submitting a valid YAML triggers the right API
  call; submitting an invalid YAML shows an error without calling the
  API (behavior to verify in the code — don't assume, look at how
  `YamlEditor` already handles this case elsewhere, e.g. in
  `TemplateDialog`/`TemplatesPage.tsx` for the YAML validation
  pattern).
- Usage: correct rendering of the mocked consumption data.

### Verification

`cd frontend && npm run typecheck && npm test`; check that the added
file actually raises `GovernancePage.tsx`'s coverage above 0%
(`npm run test -- --coverage` with the flags already in place since
C14, or equivalent — check the exact command in
`package.json`/CI).

---

## C18 — Extract the lifecycle/status blocks from the catch-all files

`operator/internal/controller/workspace_controller.go` (1109 lines)
and `api-server/internal/service/workspace_service.go` (1188 lines)
keep growing despite well-split neighbors. The report proposes "no
dedicated effort" but "impose the rule + extract along the way of the
next feature (lifecycle/status for the controller)" — this prompt does
this extraction now, as a **mechanical code move, not a redesign**:
same signatures, same behavior, tests green before/after.

`remote_workspace_service.go` (600 lines) is **out of scope**: unlike
the other two, its read structure is already coherent around a single
responsibility (remote workspace CRUD + Wake-on-LAN) — no obvious
sub-group to extract from it, don't touch it in this prompt.

### 18.1 — `operator/internal/controller/workspace_status.go` (new)

The repo already has two satellite files of `workspace_controller.go`
giving the exact pattern to follow — look in particular at
`placement.go`'s header (lines 3-8): a package comment at the top of
the file explaining the extracted responsibility. Replicate this
convention.

Move into `workspace_status.go` the contiguous status/conditions block
(`workspace_controller.go` lines 894-969 at the time of writing —
check the exact lines before cutting/pasting): `patchStatus`,
`setUnready`, `setCondition`, `setDriftCondition`,
`hasDriftCondition`, `setTypedCondition`. Add a package comment at the
top explaining its role (e.g.: "Status: this file owns patching
WorkspaceStatus and its conditions."), on the `placement.go` model.

**Don't move** the inline phase computation within `Reconcile` (the
two symmetric down/running blocks, lines ~300-415) — it's too coupled
to the rest of `Reconcile` for a safe mechanical extraction; let
`Reconcile` keep calling the helpers now in `workspace_status.go`,
don't touch its control structure.

### 18.2 — `api-server/internal/service/workspace_lifecycle.go` (new)

The repo already has `workspace_events.go` (116 lines, `Events`
method) and `workspace_resize.go` (107 lines,
`WithPodExecutor`/`Resize`) as satellites of `workspace_service.go`,
named `workspace_<feature>.go`, methods on `*WorkspaceService`. Follow
this convention.

Move into `workspace_lifecycle.go` the coherent group of lifecycle
actions identified by the research done ahead of this prompt:
`SetPaused` (lines 388-440), `UpdateOverrides` +
`updateOverridesSummary` (441-531), `Reload` (532-556) — check the
exact lines before cutting/pasting, they date from when this prompt
was written. Keep the signatures identical (methods on
`*WorkspaceService`, same names, same visibility).

If `workspace_resize_test.go` exists for `workspace_resize.go` but no
dedicated test exists for `Events`/`SetPaused`/
`UpdateOverrides`/`Reload` (check), don't add them in this prompt —
the scope here is extraction, not added coverage (that would fall
under a different finding).

### 18.3 — The rule, documented

The report asks to "impose no new responsibility in these files". Add
a package comment at the top of `workspace_controller.go` and
`workspace_service.go` (same spirit as `placement.go`) stating that
any new responsibility must live in a dedicated satellite file
(`workspace_<feature>.go` on the api-server side, a file named by
responsibility on the operator side), not be added to these two
files. Keep it brief — one or two sentences, not an essay.

### Verification

- `cd operator && go build ./... && go test ./...`.
- `cd api-server && go build ./... && go test ./...`.
- Expected diff: essentially moving identical code blocks between
  files (+ 2 new file headers, + 2 rule comments) — if the diff shows
  logic changes beyond the move, you've stepped outside this prompt's
  scope, back out.

---

## General constraints

- Never weaken an existing gate (Trivy thresholds, coverage ratchets,
  security gates) to make CI pass more easily.
- Each section (C15, C17, C16, C18) is independent of the other three
  — handle them in the document's order if possible (it's the report's
  risk order), but nothing stops a different order if you have a good
  reason.
- One commit per section, never a catch-all commit.
- Green build/tests before considering a section done:
  `cd frontend && npm run typecheck && npm test` for C15/C16/C17;
  `go build ./... && go test ./...` in `operator/` and `api-server/`
  for C18.
- If a file or line cited here has already changed enough to make an
  instruction obsolete, adapt to the real code rather than following
  the prompt to the letter — document the discrepancy in the commit
  message.
