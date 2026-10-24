# ADR 0003 — kasm-agent: a WaaS sidecar for policy-gated file transfer on KasmVNC

**Status**: accepted (2026-10-23). **Decider**: platform owner, on the
Lot 3b restitution of the KasmVNC support plan.

## Context

WaaS serves the `kasmweb/*` images without the Kasm Workspaces platform
(`docs/kasmvnc.md`). Standalone KasmVNC has no upload at all
(upstream: not planned) and its only download surfaces — `/api/downloads`
and the `www/Downloads` symlink — are blocked at wwt because they bypass
every policy. The images do ship the platform's upload service
(`kasm_upload_server`, `KASM_SVC_UPLOADS`), but it is a closed-source
binary of the proprietary platform: running it is exactly what the
"never install a Kasm agent" rule exists for, and it would not read a
WaaS policy anyway. Lot 3a put the grant in place —
`WorkspacePolicy.fileTransfer{upload,download}`, default deny, carried
in the connection token and the session capabilities — with nothing
reading it yet.

The threat model (Lot 3a) is the other constraint: inside the session
container everything is soft against a determined occupant, who owns
Xvnc, the startup scripts and the screen. A file-transfer path that ran
inside that container would be no more governable than the printer
toggle.

## Decision

A **new component**, `kasm-agent/` (its own Go module, stdlib only,
imaged and released like `wwt`): a small HTTP server the operator
injects as a **sidecar** of every kasmvnc workload. It serves three
routes over the session home — list, download, upload — and nothing
else. The design points:

- **Gate in wwt, not in the agent.** wwt validates the connection JWT
  (as for every kasm request) and gates each route on the token's
  `fileTransfer` claims — list needs either direction, download its
  own, upload its own, one HTTP method per route — before relaying to
  `http://<workspace service>:4800`. The agent never sees the token and
  cannot be reached from the browser except through wwt.
- **Auth reuse.** The agent accepts the Basic pair wwt already injects
  for KasmVNC (`kasm_user:$VNC_PW`), read from the same per-workspace
  Secret through the same env entry as the desktop container. No new
  secret, nothing to rotate twice. The occupant knows `VNC_PW`; that
  grants them their own home, which they already have.
- **Confinement.** `os.Root` on the home: `..`, absolute paths and
  escaping symlinks are refused by the kernel-side resolution, not by
  string matching. Uploads never overwrite (` (1)` suffix) and are
  capped per request (`operator.kasmAgent.maxUploadSize`, 1 GiB default,
  413 beyond).
- **Same home, same identity.** The sidecar mounts the home volume at
  the desktop's path and runs as the desktop's uid/gid (container over
  pod `securityContext`, else the kasmweb 1000/1000), so uploads land
  owned by the session user. Hardened like the platform pods (non-root,
  read-only rootfs, drop ALL, no privilege escalation, RuntimeDefault
  seccomp).
- **Soft vs governed.** The sidecar is a separate container the occupant
  cannot modify, so the grant it enforces holds against the session
  user for *this* path — the governed path for normal use. It does not
  make file egress a hard guarantee: the in-container channels
  documented in `docs/kasmvnc.md` (printing over RFB, the occupant's own
  Xvnc, the screen) are unchanged. The old HTTP surfaces stay blocked
  outright, grant or not: one auditable route, no symlinked listings.
- **Contract in `shared/kasmagent`** (port, env and route names): the
  operator, wwt and the agent are three modules, so the operator gains
  the `shared` dependency the other two already had (its image now
  builds from the repo root like theirs).
- **Off switch.** `operator.kasmAgent.enabled: false` renders an empty
  image: no sidecar, no Service port, and wwt answers 503 on the file
  routes. A workspace whose desktop has no `VNC_PW` env (explicit
  credentials Secret feeding the image another way) gets no sidecar
  either — one that cannot authenticate would only keep the pod from
  turning Ready.

## Consequences

- The portal's session menu shows a **Files** section on kasmvnc
  sessions when the policy grants at least one direction (hidden
  otherwise); downloads are plain links on the gated route, uploads land
  in `~/Uploads`.
- Enabling the sidecar on an installed platform is a pod-template
  change: the Service port converges at once, the container at the next
  scale-up boundary (ADR 0001) — until then wwt answers 503 and the
  portal says so. The same holds afterwards for **every platform
  release** (the sidecar image tag follows the components') and every
  `maxUploadSize` change: kasmvnc workspaces show `TemplateDrifted`
  until their next boundary, where guacd workspaces did not drift on a
  release before.
- The sidecar mounts the home volume only: admin `volumeMounts` under
  the home are not mirrored into it (`docs/kasmvnc.md` § Files).
- The connection token's TTL stopped bounding a kasmvnc session: the
  portal refreshes the session cookie from a fresh token for as long as
  the desktop is up (`docs/kasmvnc.md` § Auth) — needed by the Files
  routes and, as it turned out, by KasmVNC's own auto-reconnect.
- Lot 4 (audio) extends this **same** sidecar rather than adding
  another; the agent's base image changes then.
- `rdp`/`vnc` (guacd) file transfer is out of scope: `enable-sftp` /
  `enable-drive` stay platform-blocked.
