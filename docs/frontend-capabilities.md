# Frontend — "one component, capabilities"

Target model resulting from the convergence iteration: in-cluster
workspaces and remote workspaces share **the same components**,
parameterized by a common descriptor. The frontend never branches on
the type; whatever legitimately differs is declared **once** as a
capability.

## The descriptor: `SessionTarget` (`lib/target.ts`)

```ts
SessionTarget = {
  id, kind, displayName, subtitle, connectUrl,
  protocols: [{name, port, default, params, userParams}],
  defaultProtocol,
  capabilities: { pause, wake, splitView, connectionSettings,
                  editEndpoint, hasPhase, reload },
}
```

Two adapters — `targetFromWorkspace` / `targetFromRemote` — are the
ONLY places that know the shape of the two API models. On the API
side, both kinds expose the same `protocols[]` shape (remotes are
multi-endpoint since the `add_remote_protocols` migration; legacy rows
synthesize their single endpoint on read).

## The unique components and their contexts

| Component | Contexts | Differences carried by |
|---|---|---|
| `SessionCard` + `FolderedGrid` | in-cluster **and** remote cards | capabilities (badge if `hasPhase`, WoL if `wake`, clickable drift badge → reload if `reload` and phase Running) + action slots |
| `useProtocolSwitch` | card chips + overlay, both kinds | preference `workspaceSettings[id].protocol`, confirmed in-session |
| `ProtocolTabs` + `ProtocolParamsForm` | creation, connection settings, remote dialog, admin template editor | `allowList` (userParams vs admin/owner), `placeholders` (locked values), `renderParamExtra` (admin checkbox) |
| `SessionOverlay` | in-cluster **and** remote session | capabilities (split view), param storage (prefs vs server endpoint) |
| `Dialog`, `useEscape` | all dialogs/menus | — |
| `lib/lifecycle` | badge/buttons/polling | Pausing…/Resuming… drift from the spec/status gap |

**Rule for future evolutions**: a card, overlay or protocol form
feature is written ONCE against `SessionTarget`/`ProtocolParamsForm`. A
need specific to one type = a new flag in `TargetCapabilities` +
conditional rendering in the unique component — never a parallel
component. What deliberately stays separate: WoL, machine credentials,
endpoint editing (remote); pause/resume, connection settings, split
view, pending-configuration reload (in-cluster — a remote machine has
no template to drift from).

Controls stay **server-side**: the webhook/policy filters protocols
and params, `Connect` validates the chosen protocol against what the
target declares — the frontend unification has not moved any
enforcement to the client.

## State refresh

- **Source of truth**: the CR's `status` (projected by the api-server,
  which forces `Terminating` during teardown). The UI doesn't invent
  state: it labels the intent/reality gap (`Pausing…`, `Resuming…`) and
  converges.
- **SSE** `GET /api/v1/events`: a shared Kubernetes watch on the
  api-server side relays every Workspace change; remote mutations
  (DB, single writer) notify directly. Messages only carry *kinds* —
  the client re-queries the authorized API, nothing leaks.
  Auth: same access token in the query string (`EventSource` can't set
  headers), same middleware check. 25s heartbeat,
  `X-Accel-Buffering: no` for nginx (traefik streams natively).
- **Polling kept as fallback**: 3s during convergence, 15s
  otherwise (workspaces), 30s (remote).

## Protocol validation matrix (deliverable 6)

| Surface | In-cluster | Remote |
|---|---|---|
| Creation | tabs + params/protocol + "connect with" radio (locked if not overridable) | tabs + port/endpoint + default + "add protocol" |
| Card | switch chips (if >1 protocol served) + clickable drift badge (confirmed reload, if Running) | switch chips (if >1 endpoint); no drift badge |
| Connection settings | two levels: "Connection" tab (protocol tabs + params, userParams allow-list, admin bypass) + "Workspace" tab (env / placement / metadata / schedule / resources, template ∩ policy gating via `lib/overrides`) | via the edit dialog (same protocol tabs); no Workspace tab (nothing instantiated) |
| Session (overlay) | confirmed switch + reconnect params (prefs) | confirmed switch + reconnect params (server endpoint) |
| Admin template | tabs + params + user-overridable checkbox | n/a (no template) |

Automated coverage: `lib/target.test.ts` (adapters, legacy synthesis,
capabilities), `lib/lifecycle.test.ts` (derived states),
`lib/overrides.test.ts` (template ∩ policy gating),
`components/SessionCard.test.tsx` (drift badge: confirmed reload,
capability/phase gating), `dialogs/ConnectionSettingsDialog.test.tsx`
(Workspace tab: per-group gating, PATCH of only changed fields),
`remote_workspace_service_test.go` (multi-protocol: round-trip,
connect on a non-default endpoint, port/param resolution, rejection of
undeclared ones, legacy entry compat), `event_hub_test.go` (fan-out by
owner/admin, watch relay). Manual pass: run through the matrix
above on k3d (`make dev-reload`), plus pause/resume/deletion and
a cron transition to verify convergence without reloading the page.
