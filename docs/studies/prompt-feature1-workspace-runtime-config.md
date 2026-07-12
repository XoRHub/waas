# Fable 5 Prompt — Feature 1: runtime reconfiguration of a workspace + pending-change indicator

Paste this document as-is as an implementation prompt. It assumes you (Fable 5) have no prior conversation context — everything needed is below or in the referenced files.

## Repo context

WaaS is a K8s-native "workspace as a service" platform (remote VNC/RDP/SSH/KasmVNC desktops provisioned via a Go controller-runtime operator). Components: `operator/` (CRDs `Workspace`/`WorkspaceTemplate`/`WorkspacePolicy`/`WorkspaceImage` + reconcilers + webhooks + the shared governance library `operator/pkg/policy`), `api-server/` (chi REST backend, consumes `operator/api/v1alpha1` + `operator/pkg/*`), `frontend/` (React 19 + strict TS + react-query), `wwt/` (websocket proxy).

First read `docs/adr/0001-template-boundary-convergence.md` — this is the core doctrine this feature relies on: an edited `WorkspaceTemplate` (or a `Workspace`'s overrides) only converge onto the live workload (Deployment/StatefulSet/Pod) at scale-up boundaries (pause→resume, scheduled stop→resume). In-session, drift is **signaled, never applied**. This mechanism already exists and works for both sources of drift (template AND workspace overrides), since `buildPodTemplate` (`operator/internal/controller/workload.go:36+`) consumes both to build the drift fingerprint (`podTemplateFingerprint`, compared in `ensureDeployment`/`ensureStatefulSet`/`ensurePod`).

## What already exists (know this before coding)

**Overrides governance (already complete, do not duplicate):**
- `operator/api/v1alpha1/workspace_types.go`: `WorkspaceOverrides` already carries `Env`, `SecurityContext`, `PodSecurityContext`, `Volumes`, `VolumeMounts`, `NodeSelector`, `Tolerations`, `Protocol`, `Schedule`, `Labels`, `Annotations`. `WorkspaceSpec.Resources *corev1.ResourceRequirements` is a separate field (non-nil = override, presence is tested, never values — see the `specClaims` comment in `operator/pkg/policy/overrides.go`).
- `operator/pkg/policy/overrides.go` is THE single registry mapping each JSON field to an `OverridableField` (`FieldEnv`, `FieldNodeSelector`, `FieldTolerations`, `FieldResources`, etc.) and `CheckOverrides` computes usage via reflection — **zero governance code for you to write**, the admission webhook (`operator/internal/webhook`) automatically revalidates every update of the `Workspace` CR, including an update made after creation.
- The frontend already computes the gating pattern at `CreateWorkspaceDialog.tsx:174-177`:
  ```ts
  const policyAllows = (field) => !q?.allowedOverrides || q.allowedOverrides.includes(field);
  const canOverride = (field) => isAdmin || ((template?.allowedOverrides?.includes(field) ?? false) && policyAllows(field));
  ```
  `q` comes from `useQuota()` (policy-level `allowedOverrides`), `template` from `useTemplates()` (template-level `allowedOverrides`). **Reuse this pattern as-is** to gray out disallowed fields — it's exactly the mechanism requested ("only if the WorkspaceTemplate allows it, in which case the options must be grayed out").
- `Workspace.templateRef` already exists in `frontend/src/types.gen.ts:190` — so a `ConnectionSettingsDialog` can resolve the template via `useTemplates().data?.data.find(t => t.name === workspace.templateRef)` exactly like creation does.

**Existing UI to reuse (do not reinvent):**
- Resources editor (bounded CPU/memory sliders): `CreateWorkspaceDialog.tsx:329-443`.
- Env editor (list of name/value rows, add/remove): `CreateWorkspaceDialog.tsx:444-500`.
- There is **no UI today** for `nodeSelector`/`tolerations` (nowhere in the repo — only editable as raw YAML in the admin template editor, `TemplatesPage.tsx`). This is the only truly new piece visually: a key/value editor for `nodeSelector` (same UX as the env rows) and a small list editor for `tolerations` (`{key, operator, value, effect, tolerationSeconds}` per row — cf. `corev1.Toleration`).
- Current `ConnectionSettingsDialog.tsx`: a single level of tabs, one per protocol (`ProtocolTabs`/`ProtocolParamsForm`, `frontend/src/components/ProtocolTabs.tsx`).

**The drift badge already exists, partially:**
- Backend: `model.Workspace.TemplateDrifted bool` (`api-server/internal/model/model.go:199-203`), fed by the `drifted` value returned by `ensureWorkload`/`ensureDeployment` etc.
- Frontend: clickable-but-non-actionable amber badge in `SessionCard.tsx:90-104`, with a static tooltip (`portal.drift.badge`/`portal.drift.full` in `frontend/src/i18n/locales/{en,fr}.json:106-109`). The current text ("This workspace's template has been updated...") covers ONLY the template case — needs generalizing since the Feature below will also allow a workspace to drift via its own overrides.
- No manual trigger today: the only way to force convergence is to suspend then resume the workspace via `useWorkspaceAction()` (`POST /api/v1/workspaces/{id}/pause` then `.../resume`, `frontend/src/hooks/useApi.ts:170-177`), which stamps `AnnotationManualStateAt` — relevant to scheduler rule B (`docs/workspace-lifecycle.md`), but a manual pause+resume sequence from the frontend isn't a clean "reload": it changes the persistent pause intent and the annotation that drives conflict resolution with the cron schedule, whereas the user just wants to force a one-off convergence without touching their schedule.

## What needs to be delivered

### A. "Workspace" tab in Connection Settings — runtime reconfiguration

The "connection settings" menu for a workspace (`ConnectionSettingsDialog.tsx`) must gain a level of tabs above the current protocol tabs: **"Connection"** (what already exists — the VNC/RDP/SSH tabs) and **"Workspace"** (new).

The "Workspace" tab allows editing, on an already-instantiated workspace:
- environment variables (`overrides.env`),
- placement (`overrides.nodeSelector` + `overrides.tolerations` — this is the requested "nodePlacement"),
- resources (`spec.resources`, CPU/memory).

Each field group is actionable only if `canOverride(field)` is true for the field in question (`FieldEnv`, `FieldNodeSelector`, `FieldTolerations`, `FieldResources` — maps directly onto the `operator/pkg/policy/overrides.go` registry); otherwise grayed out, with an explanation (the `portal.fixedSizing` pattern already exists on `PortalPage.tsx` for the "resources not allowed at creation" case — draw inspiration from it for the "not allowed by this template/policy" label).

**Backend — new endpoint to create** (nothing like this exists today, only `Create`/`Get`/`Delete`/`Pause`/`Resume`/`Connect`/volumes exist on `WorkspaceHandler`):
- `PATCH /api/v1/workspaces/{id}/overrides`, body `{ env?, nodeSelector?, tolerations?, resources? }`.
- New method `WorkspaceService.UpdateOverrides(ctx, actor, id, in)`: `fetchByID` (already checks ownership), applies the supplied fields onto `ws.Spec.Overrides`/`ws.Spec.Resources` (full replacement of the supplied field, no partial merge — consistent with the "presence = override" semantics already in place), `s.kube.Update(ctx, ws)`. The webhook automatically redoes the `CheckOverrides` check (as for `SetPaused`): reuse the `policyDenial(err)` helper already used in `SetPaused` (`workspace_service.go:352-378`) to return a clean 403 `[ReasonCode]` on the API side if the field is in fact not allowed (defense in depth, the frontend must not be the only line of defense).
- Audit: follows the same principle as the `workspace.overrides_applied` audit mentioned for creation (only the names of the modified fields, **never env values** — existing constraint to respect).

No change to `operator/pkg/policy` or the webhook is needed: it's the same CR, the same Update admission path that `Pause`/`Resume` already use.

### B. Pending-change icon + manual reload, next to the "running" status

On a workspace's card (`SessionCard.tsx`), when a drift is pending (`target.templateDrifted`, whether the cause is an edited template **or** a change made via Feature A above), the icon must:
1. Be visible next to the status badge (already the case — extend the existing badge, do not create a second one).
2. On hover, show a **bulleted** tooltip explaining: what is going to change and why (generalize the current text which only talks about the template), and that it will apply automatically on the next scale-down/scale-up transition (pause/resume or scheduled stop — text already correct, keep it).
3. Be clickable to trigger an **immediate manual reload** (forced scale-down then scale-up), with confirmation ("the desktop will restart, unsaved work will be lost").

**Backend — suggested mechanism** (feel free to adjust if you find something cleaner, but stay within the idiom already established by the repo: annotations as a one-shot action signal consumed by the reconciler — see `AnnotationManualStateAt`, `waas.xorhub.io/delete-home`, the `waas.xorhub.io/cleanup` label):
- `POST /api/v1/workspaces/{id}/reload` (new route/handler/service method, next to `Pause`/`Resume`). **Do not touch `spec.paused` or `AnnotationManualStateAt`** — a reload must not interfere with the scheduler's conflict resolution (rule B, `docs/workspace-lifecycle.md`) nor with the user's pause intent.
- Stamp a dedicated annotation (e.g. `waas.xorhub.io/reload-requested-at=<RFC3339>`) on the CR.
- In the reconciler (`operator/internal/controller/workload.go`), when this annotation is more recent than the last known application and the workload is running (`!paused`), force a one-off convergence boundary: scale to 0 then 1 (Deployment/StatefulSet) or recreate (Pod) — reuse the path already taken by the `wasDown || want == 0` branch of `ensureDeployment` (`workload.go:~240`), then clear the annotation once applied. Emit a K8s Event (`WorkloadReloaded`) consistent with the other already-instrumented transitions (`Provisioning`/`Ready`/`Paused`/`Stopped`/`TemplateDrifted`).
- Add a test in the style of `operator/internal/controller/kasm_config_test.go` (`TestKasmConfigBoundaryConvergence`) for this new path.

**Frontend:**
- New hook `useReloadWorkspace()` (same shape as `useWorkspaceAction`, `frontend/src/hooks/useApi.ts:170-177`), `POST /api/v1/workspaces/{id}/reload`.
- "Reload" is a workspace-only capability (remote workspaces have no template drift): add it to the `SessionTarget`/`capabilities` model (`frontend/src/lib/target.ts`) following the rule documented in `docs/frontend-capabilities.md` ("How this shapes future features" — validation matrix to update), rather than hardwiring it into `SessionCard`.
- Update `portal.drift.badge`/`portal.drift.full` (en/fr) to cover both causes of drift, and add the keys for the new flow (confirmation, success, error).

## Constraints to respect

- The repo has **no TODO/FIXME** and prefers it that way (audit `docs/studies/audit-2026-07.md`) — deliver complete or not at all, no stubs.
- Tests mandatory: Go (`UpdateOverrides` service + handler + the new reconciler path) and frontend (vitest — the hook, the `canOverride` gating, the badge). The repo measures coverage per area; do not lower the existing bar.
- Clean `gofmt`, `tsc -b` with no errors (`strict: true`, zero `any`), no new `console.log`.
- Update existing documentation rather than creating a new isolated one: `docs/adr/0001-template-boundary-convergence.md` (additive note on manual reload), `docs/workspace-lifecycle.md`, `docs/frontend-capabilities.md`.
- i18n: every visible string goes through `frontend/src/i18n/locales/{en,fr}.json`, never hardcoded text in components.

## Open points (your call)

- Should the tooltip distinguish "it's the template that changed" from "it's your own settings that changed"? The current backend signal is a single boolean (`TemplateDrifted`) that doesn't make this distinction. Generic text ("this workspace's configuration has changed") covers the functional need without additional backend work; distinguishing the two is a nice-to-have, not a blocker.
- Exact shape of the `tolerations` mini-editor (one row per toleration with the 4-5 fields, or a minimal JSON textarea as a fallback): both are defensible, choose whichever is consistent with the rest of the page.
