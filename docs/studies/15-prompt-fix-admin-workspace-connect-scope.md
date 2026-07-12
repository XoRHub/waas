# Fable 5 Prompt — Fix: an admin must only see/connect to THEIR workspaces from "My Workspaces"

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context.

## Repo context

Today, an admin logged into THEIR personal "My Workspaces" page
(not the admin fleet page) sees and can connect to **all**
workspaces in the cluster, across all owners — this is a
design/security problem (traceability). The only place where an admin
should be able to _see_ other users' workspaces, without ever
connecting to them, is the admin Fleet page (reviewed in the previous
commit, `docs/studies/12-prompt-fix-fleet-owner-grouping.md`: the 3
fleet tabs already group by owner). A future feature for RW/RO
sharing initiated by the owner (traced troubleshooting) is **out of
scope** here — mention it only as an open point, don't implement it.

- **The bug is both on the visibility side (List) and the action
  side (fetchByID)**:
  - `WorkspaceService.List` (`api-server/internal/service/workspace_service.go:126-166`)
    never filters for an admin:

    ```go
    func (s *WorkspaceService) List(ctx context.Context, actor Actor) ([]model.Workspace, error) {
        isAdmin := actor.Role == string(auth.RoleAdmin)
        opts := []client.ListOption{client.InNamespace(s.namespace)}
        if !isAdmin {
            opts = append(opts, client.MatchingLabels{ownerLabel: actor.ID})
        }
    ```

    This is the same list (single route `GET /api/v1/workspaces`,
    `router.go:74`, single hook `useWorkspaces()`,
    `frontend/src/hooks/useApi.ts:34`) consumed **both** by
    the personal page (`frontend/src/sections/WorkspacesSection.tsx`,
    mounted by `PortalPage.tsx`) and `WorkspacesFleet()`
    (`frontend/src/pages/admin/FleetPage.tsx:90-92`). Nothing today
    distinguishes the two uses on the server side.
  - `WorkspaceService.fetchByID` (`workspace_service.go:930-940`),
    used by `Get`, `Delete`, `SetPaused`/`Resume`,
    `UpdateOverrides`, `Reload`, `EffectiveKasmVNCConfig`, `Events`,
    `Resize` **and** `Connect` (`workspace_service.go:543-547`), contains
    an explicit bypass for the admin:

    ```go
    if actor.Role != string(auth.RoleAdmin) && ws.Spec.Owner != actor.ID {
        // 404, not 403: don't leak the existence of other users' workspaces.
        return nil, apierror.NotFound("workspace not found")
    }
    ```

    An admin can therefore call `POST /workspaces/{id}/connect` on
    any workspace in the cluster, not just their own — this is
    the core of the bug.
- **Frontend**: `WorkspacesSection.tsx` renders `workspaces.data.data` as
  is in `FolderedGrid` (grouping by the user's _personal
  folders_, unrelated to `ownerId`), with the Open/Pause/Delete
  buttons fully active. `WorkspaceCard` opens
  `target.connectUrl` = `` `/workspaces/${ws.id}/connect` ``
  (`frontend/src/lib/target.ts:73`) with no ownership condition
  whatsoever. Once `List` is fixed, an admin will only receive their own
  workspaces on this page — the filtering must therefore be done
  server-side, not frontend.
- **Trap: `FleetPage.tsx` needs to keep seeing and deleting
  other users' workspaces** — the previous commit (study 12) has
  deliberately wired `remove.mutate` (`useDeleteWorkspace()`) to
  work on every owner's workspaces from the fleet,
  with an explicit comment:

  ```
  {/* Admin fleet delete always RETAINS the user's volume: ... */}
  onClick={() => remove.mutate({ id: ws.id, keepVolume: true })}
  ```

  If you naively remove `fetchByID`'s bypass, this admin delete
  breaks. You must therefore **separate** the "fleet-wide" list and delete
  (legitimate, infra management) from the "live session"
  access (`Connect` and, for consistency, the other by-ID actions —
  `Get`, pause, resize, overrides, reload, kasmvnc-config, events) which
  must become strictly owner-only, with no role exception.
- **Two sibling patterns already exist in the repo for exactly this
  problem** — reuse them, don't reinvent anything:
  - **Volumes** (the closest to what needs to be done here):
    `WorkspaceService.ListRetainedVolumes(ctx, actor, all bool)`
    (`volume_service.go:24-28`) — `all=false` filters by owner,
    `all=true` doesn't filter. Two distinct handlers call the same
    method: `ListVolumes` (`workspace_handler.go:110-117`, route
    `GET /api/v1/volumes`, **always** `all=false`) and
    `AdminListVolumes` (`workspace_handler.go:130-137`, route
    `GET /api/v1/admin/volumes` under `middleware.RequireAdmin`,
    `router.go:157-158`, **always** `all=true`). Same on the deletion
    side: `DeleteVolume` (user route, never admin) vs.
    `AdminDeleteVolume` (dedicated admin route, `router.go:159`). Two
    distinct frontend hooks: personal `useWorkspaces`-like vs.
    `useAdminVolumes`/`useAdminDeleteVolume` (`useApi.ts:256,264`),
    consumed respectively by the personal page and by
    `VolumesFleet()`.
  - **Remote workspaces** (the strictest, but with no admin deletion
    at all — not necessarily what we want here since the fleet delete
    is already an established gain from study 12): `List` always scoped to
    `actor.ID` (`remote_workspace_service.go:255-260`), `AdminList`
    separate with no filter (`:460`), and above all `fetchOwned`
    (`remote_workspace_service.go:536-548`) **with no role bypass at all**:

    ```go
    // Ownership is strict — remotes and their credentials are personal.
    if rw.OwnerID != actor.ID {
        return nil, apierror.NotFound("remote workspace not found")
    }
    ```

    This is the model to follow for `fetchByID`: remove the
    `actor.Role != admin &&`, keep only the ownership comparison.

## What needs to be delivered

1. **`WorkspaceService.List`**: give it an `all bool` parameter
   (signature `List(ctx, actor, all bool)`, like
   `ListRetainedVolumes`) or any equivalent variant you judge
   more readable (separate `AdminList` method, like
   `RemoteWorkspaceService`) — document your choice. In all cases:
   - the personal route `GET /api/v1/workspaces` (consumed by
     `WorkspacesSection`/`PortalPage`) must **always** filter by
     `ownerLabel: actor.ID`, admin included;
   - add a dedicated admin route `GET /api/v1/admin/workspaces`
     (new block under `router.go:150` alongside
     `admin/volumes`/`admin/remote-workspaces`, under
     `middleware.RequireAdmin`) that returns the entire namespace, with
     `OwnerUsername` resolved (reuse the logic already written in
     `workspace_service.go:146-163`, just conditioned on `all` rather
     than on `isAdmin`).
2. **`fetchByID`** (`workspace_service.go:930-940`): remove the role
   bypass, keep only `ws.Spec.Owner != actor.ID` → 404, exactly
   like `RemoteWorkspaceService.fetchOwned`. This makes strict all
   actions that go through it: `Connect`, `Get`, `SetPaused`,
   `UpdateOverrides`, `Reload`, `EffectiveKasmVNCConfig`, `Events`,
   `Resize` — an admin can no longer act on another user's
   workspace through these endpoints, regardless of the call channel (UI or
   direct API call).
3. **Admin fleet deletion**: since `fetchByID` becomes strict, the
   `Delete` via the user route (`DELETE /workspaces/{id}`) also becomes
   strict — expected, a user should only delete their own. But
   the admin fleet must keep being able to delete any workspace
   (established behavior from study 12, with volume retention). So add,
   mirroring
   `AdminDeleteVolume`/`admin/volumes/{namespace}/{name}`:
   - a separate service method, e.g.
     `AdminDelete(ctx, actor, id string, keepVolume bool)`, which skips
     `fetchByID` and fetches the workspace without an ownership check
     (the caller is already guaranteed to be admin by the route middleware);
   - a route `DELETE /api/v1/admin/workspaces/{id}` under
     `middleware.RequireAdmin` (same block as the rest of `/admin`,
     `router.go:150-160`);
   - a frontend hook `useAdminDeleteWorkspace()` (mirroring
     `useAdminDeleteVolume`, `useApi.ts:264`).
4. **Frontend `FleetPage.tsx`, `WorkspacesFleet()`
   (`FleetPage.tsx:90-...`)**: switch from `useWorkspaces()` +
   `useDeleteWorkspace()` to the new
   `useAdminWorkspaces()`/`useAdminDeleteWorkspace()` (exact mirror of
   `useAdminVolumes`/`useAdminDeleteVolume`, already used by
   `VolumesFleet()` in the same file). The owner grouping
   (study 12) doesn't change logic, only the data source
   changes hook.
5. **`WorkspacesSection.tsx`/`PortalPage.tsx`**: no code change
   expected — once `List` is fixed server-side, an admin will
   only see their own workspaces there automatically. Just check
   that no redundant frontend filter is needed.
6. **Generate types**: `make generate-types` if the admin response
   schema differs (`OwnerUsername` already exists on
   `model.Workspace`, so a priori no new type, only a
   new route in the generated client if applicable).

## Constraints

- Don't break the fleet's owner grouping (study 12): the
  admin route must keep returning `OwnerUsername` populated for each
  row.
- Don't touch `RemoteWorkspaceService` or `VolumesFleet()` — already
  compliant, out of scope for this fix.
- Don't implement the traced RW/RO sharing by the owner — just note it
  as an open point.
- The fleet deletion behavior (volume retention, resilience
  to errors) must stay identical to what study 12 delivered, just
  through a different authorization path (dedicated admin route/method
  rather than a bypass in `fetchByID`).
- Explicitly document, in the commit, the list of actions now
  strictly owner-only (`Connect`, `Get`, pause, resize, overrides,
  reload, kasmvnc-config, events) vs. those that remain
  admin-accessible via a dedicated path (fleet `List`, fleet delete)
  — this is the central distinction of this fix, don't leave it
  implicit.

## Tests

- Go: new test (or extension of `workspace_service_test.go`)
  verifying that an admin actor calling `Connect`/`Get`/`SetPaused`/etc.
  on another user's workspace receives `404 NotFound` (like
  a non-admin), while it keeps working on their own
  workspace. Extend/adapt `TestListResolvesOwnerUsernamesForAdmins`
  (`workspace_service_test.go:206-266`): the **personal** list
  (`all=false`) for an admin actor must only return their own
  workspaces; a separate test on the **admin** list (`all=true`)
  confirms it returns everything + `OwnerUsername`. Add a test for
  `AdminDelete` (works on another user's workspace, volume
  retention respected).
- Vitest: update `FleetPage.test.tsx` (created by study 12) to
  mock the new hooks `useAdminWorkspaces`/`useAdminDeleteWorkspace`
  instead of `useWorkspaces`/`useDeleteWorkspace`.
- `go build ./...` + Go tests on `api-server`; `tsc -b` + vitest on
  `frontend`.

## Open points (your arbitration)

- Exact name of the new server symbols (`List(ctx, actor, all bool)`
  in the volumes style, vs. a separate `AdminList` method in the
  remote-workspaces style) — both patterns already coexist in the
  repo, choose whichever you find more readable here and document why.
  => what do you propose?
- Future feature for RW/RO sharing by the owner, with
  traceability for troubleshooting or multi-person work sessions — not
  handled by this fix, just to keep in mind so as not to
  close the door to implementing it later (e.g. a future
  `sharedWith`/`accessGrants` field on `model.Workspace` that would
  add to the strict ownership check of `fetchByID`).
</content>
