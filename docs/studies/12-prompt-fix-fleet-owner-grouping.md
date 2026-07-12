# Fable 5 Prompt — Fix: admin fleet, group workspaces by user (except the admin's own)

Paste this document as-is as an implementation prompt. It assumes that
you (Fable 5) have no prior conversation context.

## Repo context

`FleetPage.tsx` → `WorkspacesFleet()` (`frontend/src/pages/admin/FleetPage.tsx:108-169`)
displays **all** workspaces (across all owners, for an
admin) in a flat table, in whatever order the API returns — no
sorting or grouping. The "owner" column (L129, L144) displays the raw
`ws.ownerId` (`font-mono text-xs`), not a human-readable name.

- **Backend**: `WorkspaceHandler.List` →
  `WorkspaceService.List(ctx, actor)` (`workspace_service.go:127-150`) —
  if `actor.Role != admin`, filters by label `ownerLabel: actor.ID`
  (L129-131); otherwise no restriction, the admin receives the entire
  namespace. This is the complete, ungrouped list that `useWorkspaces()`
  receives (`hooks/useApi.ts:34-37`, `GET /api/v1/workspaces`).
- **Model**: `model.Workspace` (`model.go:172-...`) only has
  `OwnerID string` (json `ownerId`) — **no** resolved
  username, unlike `model.RemoteWorkspaceAdmin.OwnerUsername`
  (`model.go:253`, json `ownerUsername,omitempty`) which already exists for
  the "remote" tab of the same page.
- **Pattern to replicate**: `RemoteWorkspaceService.AdminList`
  (`remote_workspace_service.go:459-490`) already resolves exactly this
  need for remote workspaces — a `usernames map[string]string` map
  populated on the fly via `s.users.FindByID(ctx, rw.OwnerID)` (avoids
  redundant lookups when several rows share the same owner),
  then consumed by `RemoteFleet()` on the frontend side with the
  `rw.ownerUsername || rw.ownerId` fallback (`FleetPage.tsx:206`).
  `WorkspaceService` already has access to `s.users` (already used in
  `Create`, `workspace_service.go:158`) — the same pattern applies
  as-is.
- **Identifying the admin's own workspaces**: compare
  `ws.ownerId === user.id`, `user` coming from `useAuthStore((s) => s.user)`
  on the frontend side (already this pattern in
  `ConnectionSettingsDialog.tsx:31,52`).

## What needs to be delivered

1. **Backend**: add `OwnerUsername string \`json:"ownerUsername,omitempty"\`` to `model.Workspace`, and resolve it in
`WorkspaceService.List` only for the admin branch (no need to pay an extra
lookup when a non-admin user only sees their own workspaces — they
already know their own name). Reuse the per-request cache
map, like `AdminList` — don't do a lookup
   per row without caching.
2. **Generate types**: `make generate-types` (regenerates
   `frontend/src/types.gen.ts`, drift-checked in CI) rather than editing
   `types.gen.ts` by hand.
3. **Frontend, `WorkspacesFleet()` (`FleetPage.tsx:108-169`)**:
   - Get the current user (`useAuthStore((s) => s.user)`).
   - Partition `workspaces.data.data` into two: workspaces where
     `ownerId === user?.id` (the admin themself) and the rest.
   - **Decided — no folder for the admin's own workspaces**: their
     own workspaces stay in a flat section, not grouped
     ("to be organized by the admin themself" = no imposed
     folding/organization on top, exactly like a normal user sees
     their own without folders).
   - **Decided — other users' workspaces are grouped
     by owner**: one section/disclosure per `ownerId`,
     the header showing `ownerUsername || ownerId` (same fallback as
     `RemoteFleet`), groups sorted alphabetically on that same label.
     Keep the current table columns as-is inside
     each group (no column redesign); the "owner" column
     becomes redundant inside a group but don't remove it without
     necessity — document your choice if you do remove it
     (e.g. to avoid visually repeating the same name on every
     line of a group).
   - **Two separate views vs. a single page with two sections**: the
     request allows either; **by default**, prefer a single page
     with two stacked sections ("My workspaces" then "By
     user," each user group collapsible) rather than two
     separate tabs — this avoids a navigation round-trip for
     content that stays the same resource (the same `useWorkspaces()`).
     If while implementing it you judge that a separate tab is clearly
     more readable (e.g. large volume), document why in
     the commit rather than silently deciding.
4. Keep the deletion behavior (`remove.mutate`, L152-163)
   identical regardless of the group/section where the row appears.

## Constraints

- Don't touch `RemoteFleet()` or `VolumesFleet()` — this part only
  concerns the "workspaces" tab of the fleet.
- Don't touch the server-side filtering for non-admins
  (`workspace_service.go:129-131`) — the admin continues to receive the
  full unfiltered list, only the `OwnerUsername` enrichment
  is added.
- i18n: new keys under `admin.fleetPage.*` (en/fr) for the
  section labels ("My workspaces" / "By user" or
  the equivalent chosen).

## Tests

- Go: `WorkspaceService.List` as an admin returns
  `OwnerUsername` populated for an existing owner, empty (`omitempty`) if
  the user has since been deleted (lookup error — don't
  fail the whole `List` for a not-found owner, reproduce the
  best-effort behavior of `AdminList`); as a non-admin
  user, the field doesn't need to be populated (document whether you
  leave it empty rather than paying the lookup).
- Vitest, new `FleetPage.test.tsx` (doesn't exist yet): the logged-in
  admin's workspaces appear in the flat section;
  another owner's workspaces appear grouped under their name (or
  their id if `ownerUsername` is absent); deletion works from both
  sections.
- `go build ./...` + Go tests on `api-server`; `tsc -b` + vitest on
  `frontend`.

## Open points (your arbitration)

- Two sections on one page vs. two tabs (arbitration given
  above, arbitrable if you find a strong signal while implementing it).
  => a tab would be best to clearly separate the admin's workspaces from the
  rest of the cluster, organized by username and not user UUID
- Whether to keep the "owner" column inside the groups. What do you propose?
</content>
