# Workspace templates: workloads, protocols and user overrides

This document covers the template-driven deployment model introduced after
the governance layer: how a `WorkspaceTemplate` shapes the workload, how
multiple connection protocols are declared and tuned, and which parts a
workspace creator may override.

## Workload

Linux desktops are no longer bare pods. The template picks the workload
kind and passes through the pod spec:

```yaml
spec:
  workload:
    kind: Deployment          # Deployment (default) | StatefulSet | Pod
    securityContext: {...}     # container-level
    podSecurityContext: {...}  # pod-level
    volumes: [...]
    volumeMounts: [...]
    nodeSelector: {...}
    tolerations: [...]
    serviceAccountName: ...
```

- **Deployment** (default): replicas=1, `Recreate` strategy — the home PVC
  is RWO, two desktop pods must never overlap.
- **StatefulSet**: stable identity; `serviceName` is the workspace service.
- **Pod**: the legacy bare-pod behavior.

The home PVC mount, protocol ports and probes stay platform-managed and
cannot be overridden. Windows/KubeVirt is unchanged.

Changing the workload kind of a template does not touch running
workspaces (grandfathering counts any existing kind); the new kind applies
on the next provisioning (resume after pause, or recreate).

## Logo

`spec.logo` (optional) sets the icon shown for the template in the
portal's picker. It takes the same three forms as a catalog entry's
`icon` — an absolute https URL, a `file:<path>` path internal to the
frontend, or a dashboard-icons slug — documented in
[image-catalog.md](image-catalog.md). Precedence: `spec.logo` when set,
else the icon of the catalog entry whose image matches the template's
image (the pre-existing deduction, unchanged), else the OS icon.

## Protocols

A template may declare several protocols in guacd terms:

```yaml
spec:
  protocols:
    - name: vnc          # vnc | rdp | ssh
      port: 5901
      default: true      # first entry wins if none is marked
      params:            # locked guacd connection parameters
        color-depth: "24"
      userParams: [color-depth, cursor]   # user-tunable at connect time
      credentialsSecretRef: my-creds      # username/password/private-key/passphrase
```

`vnc`, `rdp` and `ssh` are freely combinable on one template. `kasmvnc`
is exclusive: it bypasses guacd entirely, so a template declaring it may
declare no other protocol — the admission webhook rejects any
combination with `vnc`/`rdp`/`ssh`.

When `protocols` is empty, one protocol is synthesized from `os`/`port`
(linux → vnc:5901, windows → rdp:3389) so older templates keep working.

Every `params` key is a guacd wire name, validated by the template
admission webhook against the platform registry (`operator/pkg/params`):
unknown names, malformed values and platform-owned parameters
(credentials, gateways, repeaters, `enable-sftp`, …) are rejected for
every caller, kubectl included. The full mapping with exposure tiers
lives in [guacd-parameters.md](guacd-parameters.md) (generated —
`make docs-params`).

### Credentials

Desktop credentials never live in a CR. Three levels, in precedence
order:

1. **`credentialsSecretRef`** — explicit, always wins. Each protocol
   entry may name a Secret (workspace namespace) with the keys
   `username`, `password`, `private-key`, `passphrase` (all optional).
   The api-server resolves it server-side when a session starts and
   hands the values to guacd via the proxy — the browser never sees
   them. Ship the Secret with External Secrets/Vault. The same Secret
   typically also feeds the pod via env `valueFrom` (e.g. `VNC_PW`,
   `WAAS_SSH_AUTHORIZED_KEYS`) so both sides of the connection agree;
   see `waas-images/examples/workspacetemplate-ssh.yaml` for the
   complete pattern.
2. **Generated per-workspace password** — the default for `vnc`, `rdp`
   and `kasmvnc` when nothing explicit is provided: the operator
   generates a random password per workspace (never shared between
   tenants), stores it in a Secret (`waas-desktop-<name>` for vnc/rdp,
   `waas-kasm-<name>` for kasmvnc), injects it into the pod as `VNC_PW`
   via `secretKeyRef`, and the api-server resolves the same Secret at
   connect time. Zero template configuration. vnc and rdp on one
   workspace share one password (the container has a single session
   secret); at most one Secret is generated per workspace.
3. **Literal `VNC_PW` with `docker run`** — the standalone path for
   running a waas-images build outside the platform, unrelated to the
   CRs. Literal env passwords in a `WorkspaceTemplate` are **not** read
   by the platform.

Usernames are defaulted per protocol family when no credentials Secret
sets one: `waas_user` for `vnc`/`rdp` (the fixed system account of
waas-images builds, also presented to guacd by their `xrdp.ini`) and
`kasm_user` for `kasmvnc` (the fixed HTTP Basic identity of kasmweb/*
images).

### SSH

`ssh` is a first-class protocol: guacd renders the terminal, so the
portal needs nothing special. SSH is a capability of any OS-only
waas-images desktop built with `INSTALL_SSH=1` (`ubuntu-desktop-noble`,
`debian-desktop-13`, `fedora-desktop-43` — never `apps/*` images, which
are VNC-only), opted into a given workspace with `WAAS_SSH_ENABLED=1`.
It ships a fully non-root sshd on port 2222, public-key only (an
unprivileged sshd cannot read /etc/shadow, so password auth is
impossible by construction); authorized keys and the guacd private key
come from the same credentials Secret. Terminal look (`font-size`,
`color-scheme`) is user-tunable via `userParams`.

- The workspace **Service exposes every declared port**; status carries
  the full list (`status.protocols`) plus the effective default.
- The catalog gate checks **every** declared protocol against the
  `WorkspaceImage.protocols` list (`ssh` is now a valid image protocol).
- `POST /api/v1/workspaces/{id}/connect` accepts an optional body
  `{"protocol": "rdp", "params": {"color-depth": "16"}}`. The api-server
  rejects protocols the template does not declare and, for non-admins,
  any param name outside `userParams`. Accepted params are stored on the
  session and merged over the template params in the internal
  ConnectionInfo; wwt forwards them during the guacd handshake.
  `hostname`/`port` remain platform-managed whatever the params say.
- The portal stores each user's per-workspace protocol/params choice in
  their profile preferences (`workspaceSettings`) — the server still
  re-validates at connect time, so the preference is a convenience, not a
  grant.

### Audio port (`protocols[].exposeAudioPort`)

Server-side audio needs two things that are configured separately: the
guacd parameter `enable-audio` (per session) and a reachable PulseAudio
port. `exposeAudioPort: true` on a `vnc` protocol entry (VNC only,
webhook-enforced) opens port **4713** — fixed by the waas-images build
(`module-native-protocol-tcp`, gated by `WAAS_AUDIO_ENABLED` in the
image) — on both the desktop container and the workspace Service, which
mirrors container ports 1:1. Without it, `enable-audio` dials a port
nothing routes and the session degrades silently (video OK, no sound) —
which is why the `vnc-audio` smoke subtest exists
(`docs/smoke-connections.md`). Like the session ports it stays
cluster-internal (ClusterIP), never on the Ingress. In the portal the
field only surfaces once `enable-audio` is turned on in the protocol
form. Service ports converge on every reconcile, so enabling it reaches
already-provisioned workspaces without recreating them
(`docs/adr/0001-template-boundary-convergence.md`, exception note).

### KasmVNC user config (`spec.kasmvncConfig`)

kasmvnc templates may embed the raw content of the user-level KasmVNC
YAML. The string is **opaque by design** (every upstream option works;
the operator only checks it parses and rejects the policy-managed
clipboard keys, see below); it materializes as a per-workspace
ConfigMap mounted read-only at `<homeMountPath>/.vnc/kasmvnc.yaml`
(single-file subPath; the `.vnc` directory is an operator-managed
emptyDir so it stays writable for KasmVNC's runtime artifacts). KasmVNC
deep-merges this file over the image's own defaults, so a partial
config inherits every unspecified key — WaaS adds no default layer of
its own. The clipboard DLP keys derived from `WorkspacePolicy.Clipboard`
are stamped last over the admin's content (and rejected if set by hand);
the mount is therefore unconditional for a kasmvnc template, even with
an empty field. A content change rolls the workload (hash annotation on
the pod template). The webhook rejects the field on templates without a
kasmvnc protocol. Trust boundary: templates are admin-managed CRs — the
config can loosen security-relevant KasmVNC settings (`require_ssl`,
auth file paths), which is the admin's call. Full detail:
[kasmvnc.md](kasmvnc.md).

### Private registries (`WorkspaceImage.spec.imagePullSecretRef`)

Pull credentials belong to the CATALOG entry (the admin approving a
private image/registry provides its secret), never to templates. The
operator copies the referenced dockerconfigjson Secret into each
workspace's target namespace (`waas-pull-<ref>`, shared per namespace,
rotations propagate) and wires `imagePullSecrets` into the PodSpec.
Missing source = fail-closed: `PhaseFailed` / `PullSecretMissing`
condition, retried on the slow loop.

## Creator overrides

The template decides what workspace creators may deviate:

```yaml
spec:
  overrides:
    allowedFields: [env, resources, protocol, protocolParams,
                    securityContext, podSecurityContext, volumes,
                    nodeSelector, tolerations]
    owner: alice        # this platform user may override anything
```

`Workspace.spec.overrides` mirrors the workload passthrough (env,
security contexts, volumes/mounts, nodeSelector, tolerations, protocol).
Merge semantics: env/volumes/mounts merge by name (workspace wins),
nodeSelector merges key-wise, tolerations append, security contexts
replace.

On top of the template list, the **policy** may restrict overrides for
the users it governs (`WorkspacePolicy.spec.overrides.allowedFields`).
The effective allow-list is the **intersection** template ∩ policy:

- policy block absent → the template list applies alone;
- block present with an empty list → the policy forbids every override;
- platform admins bypass both lists; the template `overrides.owner`
  bypasses the template list but **stays subject to the policy list**.

Enforcement is the usual two-line defense:

- the **admission webhook** denies `[OverrideNotAllowed]` when a set field
  is not in the effective allow-list — unless the creator is a platform
  admin (`waas.xorhub.io/role: admin` annotation, trusted-writer only,
  frozen like the other identity annotations);
- the **reconciler** re-checks before creating compute (deferred-template
  case).

Every applied override is journaled: the api-server records a
`workspace.overrides_applied` audit entry listing the overridden fields
and env var **names** (never values — they may carry credentials).

Note: allow-listing `volumes` lets users mount arbitrary volume sources —
including hostPath. Only enable it on templates aimed at trusted groups.

### Portal "Advanced" panel

The workspace-creation dialog shows a collapsible **Advanced (template
overrides)** panel — env var editor today, protocol choice in the
connection section — only to users whose effective allow-list (or admin
role) permits it; everyone else never sees it. The panel mirrors the
webhook's decision, it never replaces it.

## Parameter forms: simple vs advanced

Every guacd parameter form (creation dialog, per-workspace connection
settings, remote workspaces) is generated from the platform registry
(`operator/pkg/params`, served by `GET /api/v1/meta/protocols`):

- **simple mode** (default) shows the registry tier `ui` — the everyday
  parameters: resize method, keyboard layout, color depth, audio, font
  size, read-only…;
- the **"Show advanced parameters"** toggle adds the whole `advanced`
  tier;
- the `platform` tier never reaches a form (hostname/port/credentials/
  gateways/recording are platform-owned and rejected server-side).

The ui/advanced distinction only exists in **rendering** — validation
treats both identically; only `platform` is rejected for every caller.

Every parameter also carries a **`Category`** (7 values: `display`,
`audio`, `input`, `clipboard`, `session`, `security`, `connection` —
canonical order in `AllCategories()`): the forms render one disclosure
section per category, and a registry exhaustiveness test keeps a new
parameter from shipping without one. Widget rendering is driven by
`Kind` (`ParamField.tsx`): booleans are a **tri-state segmented
control** (Default / On / Off — empty deliberately means "let guacd
apply its own default", which a binary toggle cannot express);
`int`/`string` parameters with a registry default render as a hybrid
select listing the default and common values plus a "Custom…" free
entry. Rendering only — the wire contract stays `""`/`"true"`/`"false"`.

Adding a guacd parameter = one entry in the registry table; the forms,
the validation (webhook + connect) and `docs/guacd-parameters.md`
(`make docs-params`) all follow without UI code changes.

### Delegating parameters: `userParams` and the connect-time gate

`protocols[].userParams` delegates parameter **names** (not values) the
user may override at connect time. Each entry is either an exact name
or a **category selector** `cat:X` (e.g. `cat:audio`), which delegates
every parameter of that category for that protocol — resolved
dynamically against the registry (`params.ResolveUserParamNames`), so a
parameter later added to a delegated category needs no template change.
Entries are purely additive and deduplicated (`cat:audio` +
`audio-servername` ≡ `cat:audio` alone); `platform`-tier names stay
banned whatever the syntax. The webhook rejects unknown names and
unknown categories. The template editor works on the **raw** list
(`cat:` intact, so it can render "whole category delegated" vs
per-parameter checkboxes); the connect-time model serves the list
**already resolved** — the frontend never parses `cat:` itself.

`userParams` only takes effect **under** the `protocolParams` override
right: at `/connect`, a non-admin needs `protocolParams` in the
template's `overrides.allowedFields` AND in the policy's (same
intersection as creation-time overrides; the template `owner` bypasses
the template side only, admins bypass both). Without that right, a
populated `userParams` grants nothing.

**Keyboard layout (auto).** The RDP `server-layout` is a first-class UI
parameter, but its *default* is auto-detected: when neither the template
nor the user sets it, the browser sends its locale as a client display
characteristic (`?layout=` on the WebSocket, like DPI/resolution) and wwt
uses it as the `server-layout` default — so a French browser gets an
AZERTY layout with no configuration. An explicit `server-layout` in the
template or overlay always wins. VNC/SSH have no equivalent guacd layout
parameter (VNC forwards keysyms directly). Non-admin users
additionally stay inside the template's `userParams` allow-list whatever
the tier; the browser-managed resolution (width/height/dpi) is sent at
handshake time and is not a form parameter.

## Protocol × feature matrix

Cross-cutting capabilities of the four connection paths (VNC/RDP/SSH
brokered by guacd; KasmVNC reverse-proxied by `wwt/internal/kasm`, see
`docs/kasmvnc.md`). Legend: ✅ supported · ⚙️ CR/YAML or advanced tier
only · 🚫 platform-blocked (reason in the registry) · ❌ absent ·
N/A not applicable.

| Feature | VNC | RDP | SSH | KasmVNC |
|---|---|---|---|---|
| Audio playback | ✅ `enable-audio` + `exposeAudioPort` (PulseAudio in waas-images) | ⚙️ `disable-audio` param exists; internal images ship no RDP audio chain | N/A | ❌ Kasm-platform-only upstream |
| Microphone | ❌ | ⚙️ `enable-audio-input` exists but the web client has no capture wiring — inert | N/A | ❌ |
| Governed clipboard | ✅ live | ✅ live, text only (xrdp-libvnc bridge) | ✅ live | ✅ container-side DLP (owner policy, reconcile-time) |
| Persistent home | ✅ | ✅ | ✅ | ✅ (`/home/kasm-user`) |
| Concurrent shared volume | ❌ (RWO; `homeVolumeName` = sequential adoption, not sharing) | ❌ | ❌ | ❌ |
| File transfer | 🚫 `enable-sftp`/`enable-drive` platform-blocked | 🚫 | 🚫 | 🚫 `/api/downloads` blocked at the proxy |
| Session recording | 🚫 `recording-*` platform-blocked | 🚫 | 🚫 (`typescript-path` too) | ❌ |
| Keyboard layout | N/A (direct keysyms) | ✅ `server-layout` + browser auto-detect | N/A | N/A |
| Dynamic resize | ✅ pod-exec mechanism | ✅ same (`resize-method` stays inert in-cluster) | ❌ (handshake size, CSS-scaled) | ✅ native client-side (`resize=remote`) |
| Multi-monitor | ❌ | ❌ | N/A | not integrated (upstream client capability, unverified through the iframe) |
| Live (no-reconnect) params | `disable-copy`/`disable-paste` only | same | same | none (no guac tunnel) |
| Post-creation overrides | ✅ protocol-independent (`PATCH /overrides` + reload) | ✅ | ✅ | ✅ |

Detail lives in the dedicated docs: `docs/clipboard.md` (precedence and
browser matrix), `docs/session-resize.md` (why resize is pod-exec, not
guacd), `docs/volumes.md`, `docs/kasmvnc.md`, and
`docs/adr/0001-template-boundary-convergence.md` (overrides/drift).
File transfer and recording are deliberately blocked **for everyone,
admins included**, "until the file-transfer feature ships with its own
policy gate" (`operator/pkg/params/params.go`) — the kasmvnc downloads
block mirrors the same doctrine. Remote workspaces run the same guacd
protocols with the same clipboard filter (and a real RDP server does
mount cliprdr, unlike the internal images); kasmvnc is refused there
(`docs/remote-workspaces.md`).

## Portal UX shipped alongside

- **Split view** (`/view`): 1–3 desktops side by side, Termix-style
  dynamic splits (split right/down per pane, draggable dividers, per-pane
  keyboard focus and rescaling).
- **Folders**: users group their workspaces into named boxes (stored in
  profile preferences, purely presentational).
- **Theme**: light/dark/system, persisted in the profile, quick toggle in
  the avatar menu.
