# ADR 0001 — Template convergence at scale-up boundaries

**Status**: accepted (2026-07-08). **Decider**: platform owner, on
post-audit decision (T7).

## Context

Historically, `ensureDeployment`/`ensureStatefulSet` were
**create-only** on the podTemplate: editing a `WorkspaceTemplate`
(image, env, resources, mounts, kasmvnc config) never reached existing
workspaces — invisible drift, image patches not propagated.
The alternative "converge on every edit" (pure GitOps) has a
show-stopping flaw for desktops: editing a template would **kill every
user's running session**.

## Decision

Convergence **only at scale-up boundaries**:

- A fingerprint of the desired podTemplate (`waas.xorhub.io/pod-template-hash`,
  sha256 of the built template) is compared against the live workload on
  every reconcile.
- **Workload at 0 replicas (paused, scheduled stop) or transitioning
  to it**: the podTemplate converges freely — no session can be
  killed. Resume therefore always starts from the up-to-date shape.
- **Running workload**: drift is **reported, never applied** —
  condition `TemplateDrifted=True` (reason
  `TemplateChanged`), `TemplateDrifted` event emitted on transition,
  `DRIFTED` column in `kubectl get workspace`, "update pending"
  badge (explanatory tooltip) next to the status on the portal
  card.
- Workload of kind `Pod`: convergence via recreation (pause/resume),
  drift reported the same way.
- Windows/KubeVirt path: drift detection out of scope
  (unstructured spec, path not tested e2e).

**Accepted behavior change**: the kasmvnc config
(`spec.kasmvncConfig`) used to converge mid-session (immediate
rollout). It now falls under the general doctrine: the ConfigMap file
converges immediately, the POD picks it up at its next boundary — doctrine
consistency takes priority over the original promise of the kasm
phase 2.

## Consequences

- Template edits propagate on their own through pauses/resumes
  and scheduled stop windows (scheduled workspaces converge at the
  latest at the next window).
- A workspace never suspended can drift for a long time: this is
  visible (condition/badge) and the admin can force it via pause/resume.
- The semantics' tests live in
  `operator/internal/controller/kasm_config_test.go`
  (`TestKasmConfigBoundaryConvergence`).

## Addendum (2026-07-10) — manual reload and runtime overrides

The doctrine covers BOTH sources of drift — edited template **and**
workspace overrides (the fingerprint hashes the podTemplate built from
both). Runtime reconfiguration of an instantiated workspace
(`PATCH /api/v1/workspaces/{id}/overrides`: env, nodeSelector,
tolerations, resources) therefore produces the same
report-never-apply drift as a template edit.

Added to this is a **manual reload**: instead of waiting for the
next boundary, the user can force ONE, immediately.
`POST /api/v1/workspaces/{id}/reload` stamps the one-shot annotation
`waas.xorhub.io/reload-requested-at`; the reconciler applies the
pending podTemplate onto the running workload (the Recreate
strategy — or the StatefulSet's single-replica rolling update, or bare
Pod recreation — guarantees a stop before restart, never two
pods on the RWO home), emits the `WorkloadReloaded` event then
consumes the annotation. A request with no pending drift, or on a
stopped workspace, is consumed without a restart. A reload touches
NEITHER `spec.paused` NOR `waas.xorhub.io/manual-state-at`: it cannot
interfere with scheduler rule B (docs/workspace-lifecycle.md).
On the portal side, the "update pending" badge becomes clickable
(confirmation: the desktop restarts, unsaved work is
lost). Tests: `operator/internal/controller/workload_reload_test.go`.

Detection itself is triggered by a watch on
`WorkspaceTemplate` (spec/generation only) that re-enqueues
workspaces stamped with the edited template: a running workspace has
no periodic requeue, without this watch the drift would only be
evaluated on the occasion of a fortuitous event. `WorkspaceImage`s
are watched similarly (they feed into the podTemplate: architecture
affinity, pull secret) — since image→template resolution is
catalog-global (best registry prefix match), an edit re-enqueues the
entire fleet of the namespace, no-op when nothing changes. Test:
`template_watch_test.go`.
