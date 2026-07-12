# Diagnostic — pause/resume doesn't scale the workloads

User report: pausing a workspace (or resuming it) doesn't
scale the pod to 0/1. The uptime/downtime crons were suspected of
the same ailment — rightly so: they go through the same mechanism.

## Verified chain

1. **UI → api-server**: `POST /pause|/resume` → `SetPaused`
   (`api-server/internal/service/workspace_service.go`) does correctly
   write `spec.paused` + the `waas.xorhub.io/manual-state-at` annotation on the CR.
   The api-server's namespace Role has `update` on `workspaces`. ✅
2. **Reconcile trigger**: `spec.paused` changes → the CR's generation
   is incremented → the `GenerationChangedPredicate` lets the event
   through. ✅
3. **Scale logic**: `ensureDeployment`/`ensureStatefulSet`
   (`operator/internal/controller/workload.go`) compute
   `spec.replicas = 0|1` and call `r.Update(ctx, existing)`. The logic
   is correct. ✅
4. **Operator RBAC**: ❌ **root cause**. The ClusterRole (generated
   *and* Helm chart) only granted
   `create, delete, get, list, watch` on `deployments`/`statefulsets`
   (same for `virtualmachines` for the `spec.running` toggle on Windows VMs).
   The scale's `r.Update()` was rejected **Forbidden**; the reconcile
   exited with an error before `patchStatus`, so it retried in
   backoff forever with the displayed status unchanged.

The uptime/downtime crons end up at the same `ensureWorkload(down)`:
same missing verb, same failure. A single fix covers both bugs.

## Why the tests didn't catch it

The controller tests use controller-runtime's *fake* client,
which enforces no RBAC at all: `TestReconcilePausedScalesToZero…` passed
while the real cluster refused the update. The Helm chart reproduces
the kubebuilder markers by hand — nothing tied the two together.

## Fix

- `update` added to the `+kubebuilder:rbac` markers on
  `deployments;statefulsets` and `virtualmachines`
  (`workspace_controller.go`), `config/rbac/role.yaml` regenerated
  (`make manifests`), the chart's ClusterRole aligned
  (`helm/waas/templates/operator.yaml`).
- **Anti-regression guard** (`internal/controller/rbac_test.go`):
  - every `(group, resource, verb)` of the generated role must be covered by
    the chart's ClusterRole — the manual mirror can no longer drift;
  - `update` on all three workload kinds is explicitly checked.
- **Proof of scale via the crons**
  (`internal/controller/workspace_schedule_test.go`, clock injected via
  `WorkspaceReconciler.Now`): downtime edge → replicas 0 + phase
  `Stopped` + requeue exactly at the next edge; uptime edge →
  replicas 1; a **missed tick** (controller down at the edge's time)
  caught up at the next reconcile — state derives from the last edge,
  not from tick observation; manual pause during an uptime
  window (rule B: it wins until the next opposite edge); manual resume during
  a downtime window; schedule timezone respected regardless of the
  controller's clock; schedule override takes priority over the
  template.

## Rollout

The fix is purely RBAC: `helm upgrade` (or ArgoCD sync) is enough, no
workspace restart needed. Workspaces stuck in
pause/resume converge on the first reconcile after the upgrade.
