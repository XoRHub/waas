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
| `Failed` | admission/governance denial or crash | none/blocked |
| `Terminating` | being deleted | tearing down |

`Paused` and `Stopped` share the same scale-to-0 mechanism; they differ
only in *why* the workspace is down (manual vs schedule), so the UI can
show the right action (resume vs "next uptime at …").

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
