# Home volumes: retention, reuse, quotas

## Model

**The PVC is the source of truth** — no SQL table, no dedicated CRD.
A "retained volume" is a WaaS-managed home PVC whose workspace
no longer exists, identified by its labels:

| Key | Role |
|---|---|
| `app.kubernetes.io/managed-by: waas-operator` | platform-managed object |
| `waas.xorhub.io/owner: <uuid>` | user ownership (quota key + dashboards) |
| `waas.xorhub.io/retained: "true"` | detached from a deleted workspace |
| annotation `waas.xorhub.io/origin-workspace` | provenance (display name) |
| annotation `waas.xorhub.io/retained-at` | detachment date (RFC3339) |

Per-workspace labels (`waas.xorhub.io/workspace`, `…/workspace-namespace`)
are removed on detachment: they would point to a dead CR.

## Lifecycle

```
creation ──► home PVC "<workload>-home" (live labels)
   │
workspace deletion (DELETE /workspaces/{id}?keepVolume=…)
   │
   ├── keepVolume=true (DEFAULT) ── finalizer: DETACH
   │     retained=true + provenance; the volume remains the property
   │     of the user and CONTINUES to count against their storage
   │     quota
   │     │
   │     ├── reuse: creation with spec.homeVolumeName
   │     │     ("start from an existing volume") — the webhook checks:
   │     │     same owner, volume retained, same target namespace
   │     │     (a PVC is namespaced: it can only be reattached where it
   │     │     was left). The operator re-labels the volume as live.
   │     │
   │     └── deletion: user dashboard (Volumes tab) or
   │           admin view (Fleet → Volumes) — audited (volume.deleted,
   │           via=admin for the admin), never without confirmation.
   │
   └── keepVolume=false (EXPLICIT opt-in from the dialog) ── finalizer:
         deletes the PVC along with the workspace. The
         waas.xorhub.io/delete-home="true" annotation is the only path
         to deleting it with the workspace: without it, it is retained.
```

Accepted exception: `lifecycle.maxLifetime` (policy) deletes the
workspace **and** its volume on expiry — reclaiming storage is
precisely the contract of a TTL (unchanged behavior, documented in
`docs/governance.md`).

`cleanup: DeleteWhenEmpty` (placement) remains compatible: a namespace
hosting a retained volume is never deleted (the waas PVC
retains it). It's the **namespace janitor** (internal operator
reconciler) that reclaims the namespace once the volume is finally
deleted — the PVC deletion event re-triggers it, there's no
need for a workspace to still exist (see
`docs/workspace-deletion.md`).

## Template metadata on home volumes (`spec.homeVolume`)

`WorkspaceTemplate.spec.homeVolume.labels/annotations` are stamped on
the home PVC — the driving use case is enrolling home volumes into
Longhorn recurring backup jobs, which are driven by labels on the PVC:

```yaml
spec:
  homeVolume:
    labels:
      recurring-job.longhorn.io/source: enabled
      recurring-job-group.longhorn.io/backup-daily: enabled
```

Semantics — deliberately different from the namespace metadata
(create-only) and the workload metadata (converges by rollout):

- **Synced in place on every reconcile**: editing the template enables
  backup on workspaces provisioned long ago, without touching them.
  PVC metadata is outside the pod-template fingerprint — enabling a
  backup never restarts a desktop.
- **Removals propagate.** The operator records the keys it stamped in
  the `waas.xorhub.io/template-meta` ledger annotation on the PVC
  (compact JSON, keys sorted); a ledgered key gone from the template is
  removed at the next reconcile. Keys an admin set by hand are never in
  the ledger and never touched. A corrupted ledger never fails the
  reconcile — it is rewritten whole on the next convergence.
- **Denylist as everywhere** (`pkg/metakeys`): reserved domains are
  rejected at admission and re-filtered at reconcile; platform labels
  always win. `longhorn.io` is deliberately NOT denied (frozen by a
  test in `pkg/metakeys`).
- **Retained volumes keep their metadata and ledger** — a detached
  volume still holds the user's data, it is exactly the one to keep
  backing up. On re-adoption (any template), the ledger makes the
  convergence exact: the old template's keys are removed, the new one's
  stamped.
- **Admin-only**: no overridable field — PVC labels drive platform
  machinery (backup, DR), not user preference.

Accepted limitation: a **retained volume whose template evolves
afterwards is not re-synced** — no workspace reconciles it. Its
metadata freezes as detached; the next adoption converges it. There is
deliberately no out-of-workspace sync mechanism.

## Quotas

Retained volumes count against the policy's `limits.aggregate.storage`
**exactly as on the admission side**: the webhook, the reconciler
re-check and `GET /me/quota` all go through `policy.RetainedVolumeLoads`
(storage-only loads, `Detached=true` — never counted in
`maxWorkspaces` nor in compute). The home page shows `used.storage /
limits.storage` from the server, with a "of which X retained" breakdown. A
volume adopted at creation is counted at its actual size (that of the
PVC, not the template's homeSize).

## API

- `DELETE /api/v1/workspaces/{id}?keepVolume=true|false` (absent = true)
- `GET /api/v1/volumes` / `DELETE /api/v1/volumes/{ns}/{name}` (owner)
- `GET /api/v1/admin/volumes` / `DELETE /api/v1/admin/volumes/{ns}/{name}`
- `POST /api/v1/workspaces` accepts `homeVolumeName`

RBAC: the operator detaches/adopts (updates the PVC); the api-server
lists and deletes (ClusterRole `…-api-server-volumes`, get/list/delete only).

## Migrating older volumes

Home PVCs left behind by workspaces deleted BEFORE this
feature don't carry the `retained` label: invisible to dashboards
and quota. To onboard them:

```sh
kubectl label pvc <name> -n <ns> waas.xorhub.io/retained=true
kubectl label pvc <name> -n <ns> waas.xorhub.io/workspace- waas.xorhub.io/workspace-namespace-
kubectl annotate pvc <name> -n <ns> waas.xorhub.io/retained-at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
```

(the existing `waas.xorhub.io/owner` label is authoritative for ownership).
