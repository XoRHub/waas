# Fix — read-only visibility of kasmvncConfig on the user side

*2026-07-10 — implemented. The user SEES the KasmVNC config that
applies to their workspace; editing remains strictly admin-only
(Templates page), no new write path is opened.*

## Problem

For a kasmvnc protocol, the parameter registry (`operator/pkg/params`)
is deliberately empty — the config doesn't go through
userParams/guacd. As a result: the creation dialog
(`CreateWorkspaceDialog`), the connection settings
(`ConnectionSettingsDialog`), and the session overlay displayed the
generic `portal.noTunableParams` message ("no tunable parameters on
this template"). Misleading: a config does exist, and really applies to
the container (readOnly, resolution, DLP…), it's just invisible.

## Two different values depending on context

1. **Creation (workspace not yet born)**: the only value that exists is
   the admin's **raw text** (`template.kasmvncConfig`). It already
   reached the frontend unfiltered (TemplateService.List doesn't take
   an Actor) — purely a display gap.
2. **Existing workspace**: the value that matters is the **effective
   merged content** that the operator materializes in the
   per-workspace ConfigMap (admin config + clipboard layer derived from
   the WorkspacePolicy, `ensureKasmConfig`/`applyClipboardPolicy`).
   That's the file mounted in the pod, not the template's raw field.

## Implementation

### api-server — reading the effective ConfigMap

- `WorkspaceService.EffectiveKasmVNCConfig(ctx, actor, id)`: goes
  through `fetchByID` (same scope as `Get`: owner or admin, 404 with no
  existence leak), then reads the ConfigMap at
  `{ns: ws.EffectiveTargetNamespace(), name: ws.EffectiveWorkloadName()}`,
  key `kasmvnc.yaml`. **No naming convention re-derived**: these are the
  same exported CRD methods that the operator uses via
  `computeName`/`computeNamespace`. Missing ConfigMap (non-kasmvnc
  template, or not yet reconciled) → clean 404.
- Endpoint `GET /api/v1/workspaces/{id}/kasmvnc-config` → `{config}`. A
  dedicated endpoint rather than a field on the workspace response: the
  ConfigMap read only costs something when the UI actually needs it,
  not on every List/Get (3–15s portal polling).
- No symmetric mutation, deliberately.

### frontend — read-only display, never editing

- `KasmVNCConfigView` (ProtocolTabs.tsx): read-only block (`<pre>`,
  opacity-based colors to work in both light dialogs AND the dark
  overlay), two subtitle variants: `template` ("your policy will be
  layered on top at startup") and `effective` ("the config actually
  applied: template + policy"). Empty config → "the image's default
  values apply" (KasmVNC merges the user config on top of its defaults,
  cf. Feature 12).
- `ProtocolParamsForm`: new prop `kasmvncConfig?: {content, variant}`.
  If protocol is kasmvnc **and** the prop is supplied → the viewer
  replaces `noTunableParams`. Prop absent → unchanged behavior (remote
  kasmvnc machines, RemoteWorkspaceDialog, keep the message: their
  config isn't managed by WaaS). The `noTunableParams` i18n key isn't
  reused: new keys `portal.kasmvncManagedConfig*` (en/fr).
- Wiring:
  - `CreateWorkspaceDialog` → `template.kasmvncConfig` (raw, variant
    `template`);
  - `ConnectionSettingsDialog` → new hook `useWorkspaceKasmVNCConfig`
    (effective, variant `effective`);
  - `SessionOverlay` → same hook, fetched only when the panel is open
    on an in-cluster kasmvnc session; dedicated section above the
    reconnection parameters.

## Tests

- api-server: `TestEffectiveKasmVNCConfig` — owner OK, admin OK, other
  user = 404 (no leak), workspace with no ConfigMap = clean 404,
  addressing via the CRD helpers.
- frontend: `ProtocolParamsForm.test.tsx` (content shown in place of
  `noTunableParams`, effective variant, empty state, remote case
  unchanged); `SessionOverlay.test.tsx` (section shown with the
  endpoint's content on kasmvnc, neither fetch nor section on guacd
  protocols).
