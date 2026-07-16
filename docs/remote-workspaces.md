# Remote Workspaces — off-cluster machines via guacd

A feature distinct from the "New workspace" flow: an authorized user
registers machines **external to the cluster** (host, port, protocol
ssh/vnc/rdp, credentials) and connects to them through the same
frontend → wwt → guacd chain as provisioned workspaces.

Only guacd protocols (ssh/vnc/rdp) are accepted: **kasmvnc is
explicitly refused** (400 both at registration and at connect). wwt's
kasm reverse proxy targets a KasmVNC server co-located in the
cluster; the "external machine" semantics has no kasm equivalent
and has never been verified in a real session.

## Data model (deliberately separate)

| Aspect | Provisioned workspace | Remote workspace |
|---|---|---|
| Entity | `Workspace` CR (+ operator) | SQL row `remote_workspaces` |
| Lifecycle | provisioning, pause, TTL, PVC | none — the machine is managed elsewhere |
| Network target | in-cluster Service (`status.address`) | user-supplied `hostname:port` |
| Credentials | template Secret (`credentialsSecretRef`) | **one Kubernetes Secret per entry** (`waas-remote-<id>`) |
| Deletion | operator teardown | row deletion + Secret, nothing else |

Credentials (`username`, `password`, `private-key`, `passphrase`) are
**write-only**: sent on creation/edit, stored only
in the Secret, never returned by the API (the model only exposes
`credentialKeys`, the list of present keys). The api-server resolves
them at connect time (internal endpoint `/internal/v1/sessions/{id}/connection`,
unreachable from outside the cluster) — same flow as templates.

## Access control

Opt-in via policy, fail-closed:

```yaml
# WorkspacePolicy
spec:
  remoteWorkspaces: true   # absent/false = feature invisible and refused
```

- Resolution identical to the rest of governance (priority, IdP
  groups); platform admins always pass.
- The flag is projected to the portal via `GET /api/v1/me/quota`
  (`features.remoteWorkspaces`): the tab doesn't exist for others.
- Each entry belongs strictly to its creator — even an admin cannot
  see another user's remotes (nor their credentials).

## guacd parameters

The form reuses the platform's declarative registry
(`operator/pkg/params`, served by `GET /api/v1/meta/protocols`): simple
mode (tier `ui`) by default, an "advanced parameters" checkbox for tier
`advanced`. Platform-owned parameters (hostname, port, credentials,
gateways, registration…) are refused by the same validation as
templates — both at registration AND at connect.

## API

```
GET    /api/v1/remote-workspaces            # own entries
POST   /api/v1/remote-workspaces            # {name, hostname, port, protocol, params?, credentials?}
GET    /api/v1/remote-workspaces/{id}
PUT    /api/v1/remote-workspaces/{id}       # credentials: field absent = kept, "" = removed
DELETE /api/v1/remote-workspaces/{id}       # also deletes the Secret
POST   /api/v1/remote-workspaces/{id}/connect  # → {sessionId, connectionToken, …}
```

Sessions carry `kind = "remote"` (column `sessions.kind`,
migration `20260707100001`); audit: `remote_workspace.created/updated/
deleted` + `session.started` (target included, never credentials).

## Wake-on-LAN (external relay)

A remote machine can be woken via Wake-on-LAN if a **MAC
address** is provided (`macAddress`, validated and normalized to
`aa:bb:cc:dd:ee:ff` by the api-server).

**Where the magic packet comes from.** A cluster pod cannot broadcast
on the physical L2 of the target machines. Emission is therefore
**delegated to an external relay** on the target's LAN (a manageable
device, WoL router, or small agent), which the api-server triggers over
HTTP:

```
POST $WAAS_WOL_RELAY_URL   {"mac": "aa:bb:cc:dd:ee:ff"}
Authorization: Bearer $WAAS_WOL_RELAY_TOKEN   # if set
```

api-server config: `WAAS_WOL_RELAY_URL` (enables the feature),
`WAAS_WOL_RELAY_TOKEN` (optional). Without a URL, waking returns
`503 Unavailable` and the Wake button has no effect.

**Network limitation (to document for the operator).** The magic packet
only reaches its target if the relay is on the **same L2 domain** as
the machine. In a multi-site setup, plan for **one relay per site/VLAN**;
the machine → relay mapping is currently global (a single relay) — for
multi-site, route on the relay side (per subnet) or extend the model
with a site selector on the RemoteWorkspace.

**Flow.** Manual "Wake" button on the card (as soon as a MAC is
set). On opening (open-desktop) a remote with a MAC: if the guacd
connection fails (machine off), the UI automatically attempts a
WoL once, gives the machine ~20s to boot, then retries the
connection — a magic packet to an already-on machine has no effect,
so the operation is idempotent. Audit: `remote_workspace.woke`.

## Network & RBAC (to plan for on the platform side)

- guacd must be able to **egress** to target machines: adapt
  NetworkPolicies (the `waas-images/examples/networkpolicy-workspaces.yaml`
  example only covers in-cluster traffic).
- The api-server's Role gained `create/update/delete` on Secrets
  in the workspaces namespace (still without `list`/`watch`) — see
  `helm/waas/templates/api-server/roles.yaml`.
- Clipboard policies (`WorkspacePolicy.spec.clipboard`) also apply
  to remote sessions (same token, same wwt filter).
