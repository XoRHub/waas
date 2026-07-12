# Diagnostic — slow rendering of the workspace list

User report: the list takes a long time to display, with no
feedback. The feedback part is handled (skeleton loaders + distinct
empty/error states on `PortalPage`); this document traces the likely
platform-side causes of the latency, for a dedicated iteration.

## What a portal render actually does

1. `GET /api/v1/workspaces` — two Kubernetes LISTs (workspaces + templates,
   to enrich each row with its protocols). Reasonable and
   constant cost: **no** N+1 here.
2. `GET /api/v1/me/quota` — this is the hot spot:
   - `usageOf()` does a `GET` **per workspace** of the corresponding
     template (`internal/service/governance_service.go`) → N+1 against
     the Kubernetes API server as soon as the user (or the admin, who sees
     everything) owns many workspaces;
   - the frontend **re-polls this endpoint every 15s**
     (`useQuota → refetchInterval: 15000`), so the N+1 keeps running in a loop.
3. `GET /api/v1/catalog` + `GET /api/v1/workspace-templates` in parallel
   when opening the creation dialog — negligible.

Another structural factor: the api-server talks to the Kubernetes API
**without a cache** (direct client, no informer). Every portal request means
several round trips to kube-apiserver; on a busy control plane, the
latency is kube-apiserver's, not the database's.

## Leads (not implemented in this iteration)

- **Remove the N+1 in `usageOf`**: a single LIST of the templates then
  a map lookup (the same pattern already exists in `WorkspaceService.List`).
  Immediate gain, trivial change — recommended first.
- **Kubernetes client with cache** (informers/controller-runtime cache)
  for workspaces/templates/images/policies: turns repeated LISTs
  into in-memory reads; requires handling startup (initial sync).
- **Lengthen/gate the quota poll**: only re-fetch the quota on
  mutation (create/pause/delete already invalidate the query) + a
  long interval (60s) when idle.
- **Pagination** of `/api/v1/workspaces` for the admin (fleet) case — the
  end-user portal doesn't need it in the short term.
