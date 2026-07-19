# Workspace lifecycle — pause, phases, scheduling

## Phases

The `Workspace` CR surfaces a coarse phase, reflected on the portal cards
and the fleet dashboard:

| Phase | Meaning | Compute |
|---|---|---|
| `Pending` | accepted, not yet reconciled | none |
| `Provisioning` | starting up, not ready | scaling up |
| `Running` | desktop up and reachable | 1 replica, ready |
| `Paused` | **user** paused it manually | scaled to 0 |
| `Stopped` | **scheduled** downtime window (see below) | scaled to 0 |
| `Failed` | admission/governance denial, or the desktop pod is stuck in a container error state (`CrashLoopBackOff`, `ImagePullBackOff`, eviction, …) — recovers to `Provisioning`/`Running` automatically once the pod comes up | none/blocked |
| `Terminating` | being deleted | tearing down |

`Paused` and `Stopped` share the same scale-to-0 mechanism; they differ
only in *why* the workspace is down (manual vs schedule), so the UI can
show the right action (resume vs "next uptime at …").

## Conditions

`status.conditions` follow the `metav1.Condition` convention (status
subresource; every condition carries the `observedGeneration` it was
evaluated against):

| Type | True when | Notable reasons |
|---|---|---|
| `Ready` | the workload reports ready | `WorkspaceReady`, `Provisioning`, `Paused`, admission denial reason codes (`ImageNotInCatalog`, `QuotaExceeded`, `PullSecretMissing`, …), container error reason codes surfaced verbatim from the kubelet (`CrashLoopBackOff`, `ImagePullBackOff`, `ErrImagePull`, `CreateContainerConfigError`, `Evicted`, …) with the last exit code / `OOMKilled` detail in the message |
| `ConnectionReady` | the desktop server ACCEPTS TCP connections on the default protocol port (operator probe) — pod readiness proves the container runs, this proves the desktop listens | `DesktopListening`, `DesktopNotListening`, `DesktopDown` |

`kubectl get workspace` shows PHASE and READY. The operator also emits
Kubernetes **Events** on the CR at every phase transition
(Provisioning/Ready/Paused/Stopped) and on every admission decision —
aggregated with the children's events in the portal's Events panel
(`GET /api/v1/workspaces/{id}/events`, refresh cadence server-driven by
`WAAS_EVENTS_POLL_INTERVAL` / `apiServer.eventsPollInterval`).
`WorkspaceTemplate`/`WorkspacePolicy` carry no status by design: both
are webhook-validated at admission, an `Accepted` condition would be
tautological.

## Pause = scale to 0, not delete

Pausing a workspace **scales its workload to 0 replicas** — it does not
delete the Deployment/StatefulSet. The whole object (labels, annotations,
spec, pod template, resources) is kept, so resume is a scale back to 1:
fast, no reconstruction, no re-admission. The home PVC is retained in
every case (it always was).

Per workload kind:

- **Deployment / StatefulSet**: `spec.replicas` is patched to 0 (pause)
  or 1 (resume) in place. The Service is kept across pause (stable
  in-cluster DNS, no endpoint churn on resume; it simply has no endpoints
  while scaled to 0).
- **Pod** (legacy bare-pod kind): a Pod has no replica count, so pause
  deletes it and resume recreates it from the same home PVC. State lives
  on the PVC, so this is equivalent for the user.
- **Windows / KubeVirt VM**: `spec.running` is toggled `false` (pause) /
  `true` (resume). The VM object and its disks are kept; KubeVirt stops
  and starts the `virt-launcher` pod.

While paused, `status.address/port/protocol` are cleared (nothing is
reachable) and the `Ready` condition is `False` with reason `Paused`.

### Migration from the previous mechanism

The previous implementation **deleted** the workload on pause and reported
phase `Stopped`. No manual migration is needed:

- A workspace paused under the old mechanism has no workload object. On
  its next reconcile the operator recreates it directly at `replicas: 0`
  (still paused) — no desktop pod is started, the home PVC is reused, and
  the phase moves from `Stopped` to `Paused`. Resuming then scales it to
  1 as usual.
- Nothing is destroyed and no data is touched; the change is transparent
  to running and to already-paused workspaces.

## Scheduled uptime / downtime

A template can plan start/stop by cron to cap resource use:

```yaml
spec:
  schedule:
    timezone: Europe/Paris        # IANA name, REQUIRED when crons are set
    uptime:   ["0 8 * * 1-5"]     # start weekdays at 08:00
    downtime: ["0 20 * * *"]      # stop every day at 20:00
```

- **Standard 5-field cron**, evaluated in the template's explicit
  timezone — the controller never uses its own TZ (validated by the
  webhook and the api-server via `operator/pkg/schedule`).
- **Downtime uses the pause mechanism** (scale to 0); the phase is
  `Stopped` (scheduled) rather than `Paused` (manual).
- **Overridable / locked** like any template option: add `schedule` to
  `overrides.allowedFields` to let creators set their own schedule at
  instantiation (intersected with the policy's allow-list, as usual).
- The operator requeues exactly at the next edge, so transitions fire on
  time; `status.nextTransition` carries the next change and is shown on
  the portal card ("⏰ next stop …") and in the detail view.

### Conflict between a manual action and the schedule (rule B)

A manual pause/resume **wins until the next scheduled edge of the
opposite kind**, then the schedule regains control:

- Manual **resume** during a downtime window → stays up until the next
  **downtime** edge (e.g. you wake it after hours; it runs until the next
  scheduled stop, not just until the next tick).
- Manual **pause** during an uptime window → stays down until the next
  **uptime** edge (e.g. "I'm done for today"; it comes back on schedule
  tomorrow morning).

The api-server stamps the manual action time in the
`waas.xorhub.io/manual-state-at` annotation; the operator never mutates
`spec.paused`, so a stale manual state simply stops winning once its
opposite edge passes. With no schedule, `spec.paused` is the only signal
(pure manual pause/resume).

## Manual reload (immediate convergence boundary)

A workspace whose configuration changed while it runs — a template edit
or a runtime override update (`PATCH /api/v1/workspaces/{id}/overrides`;
see docs/adr/0001) — normally picks the new shape up at its next
scale-up boundary. `POST /api/v1/workspaces/{id}/reload` (the portal's
clickable "update pending" badge, confirmation included) forces that
boundary NOW:

- the api-server stamps the one-shot annotation
  `waas.xorhub.io/reload-requested-at` (Running workspaces only, 409
  otherwise — a down workspace converges at its next start anyway);
- the operator applies the pending pod template mid-session (Recreate /
  the single-replica rolling update stops the old pod before the new one
  starts; a bare Pod is deleted and recreated), emits the
  `WorkloadReloaded` event and removes the annotation;
- a request with nothing pending is consumed silently, never deferred.

A reload deliberately touches **neither** `spec.paused` **nor**
`waas.xorhub.io/manual-state-at`: it is not a pause/resume and never
shifts the schedule conflict resolution (rule B above).

