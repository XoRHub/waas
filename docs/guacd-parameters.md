# guacd connection parameters — CR ↔ guacd mapping

<!-- GENERATED FILE — do not edit. Source: operator/pkg/params.
     Regenerate with: make docs-params -->

One vocabulary end to end: the key used in a template's
`spec.protocols[].params` (and in connect-time overrides) **is** the
guacd wire name. This table is generated from `operator/pkg/params`,
the registry that the admission webhook, the api-server and the frontend
forms all consume.

## Exposure tiers

| Tier | Meaning |
|---|---|
| `ui` | Exposed in portal forms (template editor, workspace creation, connection settings, in-session overlay). |
| `advanced` | Settable in the CR/template only (kubectl/GitOps or the template editor's advanced section). |
| `platform` | Owned by the platform: injected automatically (hostname, port, credentials) or banned as a security/topology hazard. Rejected by the webhook in any CR, for every caller. |

"Live" parameters can be toggled mid-session (enforced client-side or by
the wwt proxy); everything else requires a reconnect — guacd fixes its
parameters at connect time.

## vnc

| Parameter | Tier | Type | Constraints | Default | Live | Description |
|---|---|---|---|---|---|---|
| `color-depth` | ui | enum | 8, 16, 24, 32 | 24 |  | Display color depth in bits per pixel; lower saves bandwidth. |
| `cursor` | ui | enum | local, remote | local |  | Render the mouse cursor locally (responsive) or remotely (accurate for cursor-morphing apps). |
| `disable-copy` | ui | bool |  | false | yes | Block copying FROM the remote desktop to the local clipboard. Enforced by the wwt proxy, live-toggleable. |
| `disable-paste` | ui | bool |  | false | yes | Block pasting FROM the local clipboard to the remote desktop. Enforced by the wwt proxy, live-toggleable. |
| `force-lossless` | ui | bool |  | false |  | Force lossless compression (sharp text, higher bandwidth). |
| `read-only` | ui | bool |  | false |  | View-only session: display without mouse/keyboard input. |
| `swap-red-blue` | ui | bool |  | false |  | Fix red/blue channel inversion produced by some VNC servers. |
| `autoretry` | advanced | int | 0 – 10 |  |  | Connection retries before giving up (covers desktops still booting). |
| `clipboard-encoding` | advanced | enum | ISO8859-1, UTF-8, UTF-16, CP1252 | ISO8859-1 |  | Character encoding the VNC server uses for clipboard data. |
| `encodings` | advanced | string |  |  |  | Space-separated VNC encodings offered to the server (expert tuning). |
| `dest-host` | platform | string |  |  |  | VNC repeater redirection — banned: would let a CR reroute guacd to an arbitrary host. |
| `dest-port` | platform | int |  |  |  | VNC repeater redirection — banned (see dest-host). |
| `enable-sftp` | platform | bool |  |  |  | File transfer — platform-owned until the file-transfer feature ships with its own policy gate. |
| `hostname` | platform | string |  |  |  | Always the workspace service address, resolved by the operator. |
| `password` | platform | string |  |  |  | Desktop credential — comes from the protocol's credentials Secret, never from a CR param. |
| `port` | platform | int |  |  |  | Always the workspace protocol port, resolved by the operator. |
| `username` | platform | string |  |  |  | Desktop credential — comes from the protocol's credentials Secret, never from a CR param. |
| `wol-send-packet` | platform | bool |  |  |  | Wake-on-LAN — meaningless in-cluster, banned. |

## rdp

| Parameter | Tier | Type | Constraints | Default | Live | Description |
|---|---|---|---|---|---|---|
| `color-depth` | ui | enum | 8, 16, 24, 32 | 24 |  | Display color depth in bits per pixel; lower saves bandwidth. |
| `disable-audio` | ui | bool |  | false |  | Disable audio redirection from the remote desktop. |
| `disable-copy` | ui | bool |  | false | yes | Block copying FROM the remote desktop to the local clipboard. Enforced by the wwt proxy, live-toggleable. |
| `disable-paste` | ui | bool |  | false | yes | Block pasting FROM the local clipboard to the remote desktop. Enforced by the wwt proxy, live-toggleable. |
| `read-only` | ui | bool |  | false |  | View-only session: display without mouse/keyboard input. |
| `console` | advanced | bool |  | false |  | Attach to the console (admin) session instead of a new one. |
| `enable-audio-input` | advanced | bool |  | false |  | Redirect the local microphone into the remote session. |
| `enable-font-smoothing` | advanced | bool |  | false |  | Enable ClearType font smoothing. |
| `enable-wallpaper` | advanced | bool |  | false |  | Render the desktop wallpaper (bandwidth for cosmetics). |
| `ignore-cert` | advanced | bool |  | true |  | Accept the RDP server certificate unverified. Acceptable in-cluster (self-signed VM certs); the connection never leaves the cluster network. |
| `resize-method` | advanced | enum | display-update, reconnect | display-update |  | How guacd propagates browser resizes to the RDP server. |
| `security` | advanced | enum | any, nla, tls, rdp | any |  | RDP security negotiation mode; in-cluster Windows VMs may need a specific one. |
| `server-layout` | advanced | string |  |  |  | Keyboard layout the RDP server expects (e.g. fr-fr-azerty). |
| `enable-drive` | platform | bool |  |  |  | Drive redirection — platform-owned until the file-transfer feature ships with its own policy gate. |
| `enable-sftp` | platform | bool |  |  |  | File transfer — platform-owned until the file-transfer feature ships with its own policy gate. |
| `gateway-hostname` | platform | string |  |  |  | RDP gateway — banned: workspace traffic never leaves the cluster network. |
| `gateway-password` | platform | string |  |  |  | RDP gateway credential — banned (see gateway-hostname). |
| `gateway-port` | platform | int |  |  |  | RDP gateway — banned (see gateway-hostname). |
| `gateway-username` | platform | string |  |  |  | RDP gateway credential — banned (see gateway-hostname). |
| `hostname` | platform | string |  |  |  | Always the workspace service address, resolved by the operator. |
| `password` | platform | string |  |  |  | Desktop credential — comes from the protocol's credentials Secret, never from a CR param. |
| `port` | platform | int |  |  |  | Always the workspace protocol port, resolved by the operator. |
| `username` | platform | string |  |  |  | Desktop credential — comes from the protocol's credentials Secret, never from a CR param. |
| `wol-send-packet` | platform | bool |  |  |  | Wake-on-LAN — meaningless in-cluster, banned. |

## ssh

| Parameter | Tier | Type | Constraints | Default | Live | Description |
|---|---|---|---|---|---|---|
| `color-scheme` | ui | enum | black-white, gray-black, green-black, white-black | gray-black |  | Terminal color scheme. |
| `disable-copy` | ui | bool |  | false | yes | Block copying FROM the remote desktop to the local clipboard. Enforced by the wwt proxy, live-toggleable. |
| `disable-paste` | ui | bool |  | false | yes | Block pasting FROM the local clipboard to the remote desktop. Enforced by the wwt proxy, live-toggleable. |
| `font-size` | ui | int | 6 – 48 | 12 |  | Terminal font size in points. |
| `read-only` | ui | bool |  | false |  | View-only session: display without mouse/keyboard input. |
| `command` | advanced | string |  |  |  | Command to run instead of an interactive shell (kiosk-style templates). |
| `font-name` | advanced | string |  |  |  | Terminal font family (must exist server-side in guacd). |
| `scrollback` | advanced | int | 0 – 100000 | 1000 |  | Scrollback buffer size in rows. |
| `server-alive-interval` | advanced | int | 0 – 3600 |  |  | SSH keep-alive interval in seconds. |
| `terminal-type` | advanced | string |  | linux |  | TERM value announced to the SSH server. |
| `enable-sftp` | platform | bool |  |  |  | File transfer — platform-owned until the file-transfer feature ships with its own policy gate. |
| `hostname` | platform | string |  |  |  | Always the workspace service address, resolved by the operator. |
| `passphrase` | platform | string |  |  |  | SSH key passphrase — comes from the protocol's credentials Secret, never from a CR param. |
| `password` | platform | string |  |  |  | Desktop credential — comes from the protocol's credentials Secret, never from a CR param. |
| `port` | platform | int |  |  |  | Always the workspace protocol port, resolved by the operator. |
| `private-key` | platform | string |  |  |  | SSH private key — comes from the protocol's credentials Secret, never from a CR param. |
| `username` | platform | string |  |  |  | Desktop credential — comes from the protocol's credentials Secret, never from a CR param. |
| `wol-send-packet` | platform | bool |  |  |  | Wake-on-LAN — meaningless in-cluster, banned. |

## Adding a parameter

1. Add one entry to the registry in `operator/pkg/params/params.go`:
   name (the guacd wire name), protocols, kind (+ enum/bounds), tier,
   live flag, description. Pick the tier deliberately:
   security/topology-sensitive parameters are `platform`.
2. Run `make docs-params` (this file) and `make test`
   (the registry has coherence tests).
3. Nothing else. The webhook validates it, the api-server serves it on
   `GET /api/v1/meta/protocols`, and the forms render it from
   that payload.
