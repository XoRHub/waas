# Diagnostic — VNC/RDP "connection closed": netpol on placed namespaces

> Since the `workspaces.namespace` default switched to the release
> namespace, the CR namespace and platform namespace are **identical by
> default** — this specific bug class (the two namespaces silently
> diverging) becomes much less likely under default configuration. It
> remains possible for any installation that sets
> `workspaces.namespace` explicitly to something other than the release
> namespace; the diagnostic below therefore remains valid in that case.

**Symptom.** Any session to a **placed** in-cluster workspace (dedicated
namespace) immediately drops to "connection closed". guacd logs
`Unable to connect to VNC server` (VNC) or `RDP server closed/refused
connection: Server refused connection (wrong security type?)` (RDP — a
generic freerdp message for a plain ECONNREFUSED, don't be distracted
by "security type"). **Non-placed** workspaces (CR namespace)
work fine, which mimicked a per-protocol bug.

**Diagnostic chain** (reproducible as-is):

1. workspace `Status.Address` vs the real Service: consistent (same
   `computeName`/`computeNamespace` helpers on both sides);
2. Service endpoints: healthy, and the kubelet (probes) connects
   fine;
3. from the guacd pod: `nc -z <svc>.<ns>.svc.cluster.local 5901` →
   **connection refused** on every port;
4. from a pod in the CR namespace: **success** — the discriminant.
   Under k3s, the NetworkPolicy controller (kube-router) rejects with
   REJECT, hence "refused" rather than a timeout;
5. the placed namespace's `waas-default-ingress` netpol only admitted
   the CR namespace (`waas-workspaces`) — not `waas`, where
   guacd/wwt run.

**Root cause.** The operator was running with an empty `PlatformNamespace`:
the live Deployment predated commit 0fa8a9d (which added the
`WAAS_PLATFORM_NAMESPACE` env var to the chart), because the Helm
release had never been upgraded — `make dev-reload` reloaded the
images but did not re-render the chart. Since bootstrap is create-only,
every namespace created during that window kept its bogus netpol
forever.

**Fixes** (none is sufficient on its own):

- the operator **falls back to its own namespace** (serviceaccount
  mount) when the env var is missing — no more silently broken mode;
- the `waas-default-ingress` netpol is **desired-state**: re-synced
  on every reconcile as long as it carries the managed-by label (a netpol
  taken over by an admin — label removed — is never rewritten). Existing
  broken namespaces self-heal (event
  `IngressPolicyHealed`);
- `make dev-reload` now includes `dev-deploy` (helm upgrade);
- delivery gate: `make smoke` establishes a real session per
  protocol (`docs/smoke-connections.md`) — this class of regression can
  no longer pass an iteration.
