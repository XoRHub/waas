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
| `advanced` | Same validation policy as `ui`, but rendered behind the advanced disclosure of its section in every form. |
| `platform` | Owned by the platform: injected automatically (hostname, port, credentials) or banned as a security/topology hazard. Rejected by the webhook in any CR, for every caller. |

Each parameter also carries a **category** (display, audio, input,
clipboard, session, security, connection): the thematic section it
renders under in the forms, and the unit a template can delegate
wholesale — a `userParams` entry `cat:audio` allow-lists every
non-platform parameter of the category for that protocol, including
parameters added to it later (resolved against this registry at
validation time, never hardcoded in the template). Values themselves
are never validated per category.

"Live" parameters can be toggled mid-session (enforced client-side or by
the wwt proxy); everything else requires a reconnect — guacd fixes its
parameters at connect time.

## vnc

| Parameter | Category | Tier | Type | Constraints | Default | Live | Description |
|---|---|---|---|---|---|---|---|
| `color-depth` | display | ui | enum | 8, 16, 24, 32 | 24 |  | Display color depth in bits per pixel; lower saves bandwidth. |
| `cursor` | display | ui | enum | local, remote | local |  | Render the mouse cursor locally (responsive) or remotely (accurate for cursor-morphing apps). |
| `force-lossless` | display | ui | bool |  | false |  | Force lossless compression (sharp text, higher bandwidth). |
| `swap-red-blue` | display | ui | bool |  | false |  | Fix red/blue channel inversion produced by some VNC servers. |
| `encodings` | display | advanced | string |  |  |  | Space-separated VNC encodings offered to the server (expert tuning). |
| `enable-audio` | audio | ui | bool |  | false |  | Stream audio from the workspace's PulseAudio server (requires the image to run one). |
| `audio-servername` | audio | advanced | string |  |  |  | PulseAudio server name when it differs from the VNC hostname. |
| `disable-copy` | clipboard | ui | bool |  | false | yes | Block copying FROM the remote desktop to the local clipboard. Enforced by the wwt proxy, live-toggleable. |
| `disable-paste` | clipboard | ui | bool |  | false | yes | Block pasting FROM the local clipboard to the remote desktop. Enforced by the wwt proxy, live-toggleable. |
| `clipboard-encoding` | clipboard | advanced | enum | ISO8859-1, UTF-8, UTF-16, CP1252 | ISO8859-1 |  | Character encoding the VNC server uses for clipboard data. |
| `create-recording-path` | session | platform | bool |  |  |  | Session recording — platform-owned (see recording-path). |
| `enable-sftp` | session | platform | bool |  |  |  | File transfer — platform-owned until the file-transfer feature ships with its own policy gate. |
| `recording-name` | session | platform | string |  |  |  | Session recording — platform-owned (see recording-path). |
| `recording-path` | session | platform | string |  |  |  | Session recording — platform-owned until the recording feature ships with its own policy gate. |
| `read-only` | security | ui | bool |  | false |  | View-only session: display without mouse/keyboard input. |
| `autoretry` | connection | advanced | int | 0 – 10 |  |  | Connection retries before giving up (covers desktops still booting). |
| `dest-host` | connection | platform | string |  |  |  | VNC repeater redirection — banned: would let a CR reroute guacd to an arbitrary host. |
| `dest-port` | connection | platform | int |  |  |  | VNC repeater redirection — banned (see dest-host). |
| `hostname` | connection | platform | string |  |  |  | Always the workspace service address, resolved by the operator. |
| `password` | connection | platform | string |  |  |  | Desktop credential — comes from the protocol's credentials Secret, never from a CR param. |
| `port` | connection | platform | int |  |  |  | Always the workspace protocol port, resolved by the operator. |
| `sftp-hostname` | connection | platform | string |  |  |  | SFTP side-channel to an arbitrary host — banned (the whole sftp-* family is unregistered on purpose). |
| `username` | connection | platform | string |  |  |  | Desktop credential — comes from the protocol's credentials Secret, never from a CR param. |
| `wol-send-packet` | connection | platform | bool |  |  |  | Wake-on-LAN — meaningless in-cluster, banned. |

## rdp

| Parameter | Category | Tier | Type | Constraints | Default | Live | Description |
|---|---|---|---|---|---|---|---|
| `color-depth` | display | ui | enum | 8, 16, 24, 32 | 24 |  | Display color depth in bits per pixel; lower saves bandwidth. |
| `resize-method` | display | ui | enum | display-update, reconnect | display-update |  | How guacd propagates resizes to a remote RDP server (display-update = live resize). No effect on in-cluster desktops, which WaaS resizes via pod exec. |
| `disable-bitmap-caching` | display | advanced | bool |  | false |  | Disable the RDP bitmap cache (workaround for buggy servers). |
| `disable-glyph-caching` | display | advanced | bool |  | false |  | Disable glyph caching (workaround for text rendering glitches). |
| `disable-offscreen-caching` | display | advanced | bool |  | false |  | Disable caching of off-screen regions (workaround for buggy servers). |
| `enable-desktop-composition` | display | advanced | bool |  | false |  | Enable Windows Aero desktop composition effects. |
| `enable-font-smoothing` | display | advanced | bool |  | false |  | Enable ClearType font smoothing. |
| `enable-full-window-drag` | display | advanced | bool |  | false |  | Render window contents while dragging (bandwidth for comfort). |
| `enable-menu-animations` | display | advanced | bool |  | false |  | Enable menu open/close animations. |
| `enable-theming` | display | advanced | bool |  | false |  | Enable desktop/window theming. |
| `enable-wallpaper` | display | advanced | bool |  | false |  | Render the desktop wallpaper (bandwidth for cosmetics). |
| `disable-audio` | audio | ui | bool |  | false |  | Disable audio redirection from the remote desktop. |
| `console-audio` | audio | advanced | bool |  | false |  | Play audio on the server console instead of streaming it to the client. |
| `enable-audio-input` | audio | advanced | bool |  | false |  | Redirect the local microphone into the remote session. |
| `server-layout` | input | ui | enum | en-us-qwerty, en-gb-qwerty, cs-cz-qwertz, da-dk-qwerty, de-ch-qwertz, de-de-qwertz, es-es-qwerty, es-latam-qwerty, fr-be-azerty, fr-ca-qwerty, fr-ch-qwertz, fr-fr-azerty, hu-hu-qwertz, it-it-qwerty, ja-jp-qwerty, nl-nl-qwerty, no-no-qwerty, pl-pl-qwertz, pt-br-qwerty, pt-pt-qwerty, ro-ro-qwerty, sv-se-qwerty, tr-tr-qwerty, failsafe | en-us-qwerty |  | Keyboard layout the RDP server expects. Left unset, the platform auto-detects it from the browser locale (failsafe sends Unicode events). |
| `disable-copy` | clipboard | ui | bool |  | false | yes | Block copying FROM the remote desktop to the local clipboard. Enforced by the wwt proxy, live-toggleable. |
| `disable-paste` | clipboard | ui | bool |  | false | yes | Block pasting FROM the local clipboard to the remote desktop. Enforced by the wwt proxy, live-toggleable. |
| `normalize-clipboard` | clipboard | advanced | enum | preserve, text | preserve |  | Line-ending normalization applied to clipboard text. |
| `client-name` | session | advanced | string |  |  |  | Client hostname announced to the RDP server (some session brokers key on it). |
| `console` | session | advanced | bool |  | false |  | Attach to the console (admin) session instead of a new one. |
| `initial-program` | session | advanced | string |  |  |  | Program launched instead of the full desktop (kiosk-style templates). |
| `timezone` | session | advanced | string |  |  |  | IANA timezone forwarded to the session (e.g. Europe/Paris). |
| `create-recording-path` | session | platform | bool |  |  |  | Session recording — platform-owned (see recording-path). |
| `enable-drive` | session | platform | bool |  |  |  | Drive redirection — platform-owned until the file-transfer feature ships with its own policy gate. |
| `enable-sftp` | session | platform | bool |  |  |  | File transfer — platform-owned until the file-transfer feature ships with its own policy gate. |
| `recording-name` | session | platform | string |  |  |  | Session recording — platform-owned (see recording-path). |
| `recording-path` | session | platform | string |  |  |  | Session recording — platform-owned until the recording feature ships with its own policy gate. |
| `static-channels` | session | platform | string |  |  |  | Raw static virtual channel pass-through — banned: uncontrolled side channel. |
| `read-only` | security | ui | bool |  | false |  | View-only session: display without mouse/keyboard input. |
| `ignore-cert` | security | advanced | bool |  | true |  | Accept the RDP server certificate unverified. Acceptable in-cluster (self-signed VM certs); the connection never leaves the cluster network. |
| `security` | security | advanced | enum | any, nla, tls, rdp | any |  | RDP security negotiation mode; in-cluster Windows VMs may need a specific one. |
| `disable-auth` | security | platform | bool |  |  |  | Disables RDP authentication entirely — banned: authentication is platform policy (see RDP_AUTH_ENABLED image contract). |
| `domain` | connection | platform | string |  |  |  | RDP credential — comes from the protocol's credentials Secret, never from a CR param. |
| `gateway-hostname` | connection | platform | string |  |  |  | RDP gateway — banned: workspace traffic never leaves the cluster network. |
| `gateway-password` | connection | platform | string |  |  |  | RDP gateway credential — banned (see gateway-hostname). |
| `gateway-port` | connection | platform | int |  |  |  | RDP gateway — banned (see gateway-hostname). |
| `gateway-username` | connection | platform | string |  |  |  | RDP gateway credential — banned (see gateway-hostname). |
| `hostname` | connection | platform | string |  |  |  | Always the workspace service address, resolved by the operator. |
| `load-balance-info` | connection | platform | string |  |  |  | RDP broker routing token — platform topology concern, banned in CRs. |
| `password` | connection | platform | string |  |  |  | Desktop credential — comes from the protocol's credentials Secret, never from a CR param. |
| `port` | connection | platform | int |  |  |  | Always the workspace protocol port, resolved by the operator. |
| `sftp-hostname` | connection | platform | string |  |  |  | SFTP side-channel to an arbitrary host — banned (the whole sftp-* family is unregistered on purpose). |
| `username` | connection | platform | string |  |  |  | Desktop credential — comes from the protocol's credentials Secret, never from a CR param. |
| `wol-send-packet` | connection | platform | bool |  |  |  | Wake-on-LAN — meaningless in-cluster, banned. |

## ssh

| Parameter | Category | Tier | Type | Constraints | Default | Live | Description |
|---|---|---|---|---|---|---|---|
| `color-scheme` | display | ui | enum | black-white, gray-black, green-black, white-black | gray-black |  | Terminal color scheme. |
| `font-size` | display | ui | int | 6 – 48 | 12 |  | Terminal font size in points. |
| `font-name` | display | advanced | string |  |  |  | Terminal font family (must exist server-side in guacd). |
| `scrollback` | display | advanced | int | 0 – 100000 | 1000 |  | Scrollback buffer size in rows. |
| `backspace` | input | advanced | int | 1 – 255 | 127 |  | Code sent by the backspace key (127 = ASCII DEL, 8 = BS for legacy hosts). |
| `disable-copy` | clipboard | ui | bool |  | false | yes | Block copying FROM the remote desktop to the local clipboard. Enforced by the wwt proxy, live-toggleable. |
| `disable-paste` | clipboard | ui | bool |  | false | yes | Block pasting FROM the local clipboard to the remote desktop. Enforced by the wwt proxy, live-toggleable. |
| `command` | session | advanced | string |  |  |  | Command to run instead of an interactive shell (kiosk-style templates). |
| `locale` | session | advanced | string |  |  |  | LANG value forwarded to the SSH session (server must accept env forwarding). |
| `terminal-type` | session | advanced | string |  | linux |  | TERM value announced to the SSH server. |
| `timezone` | session | advanced | string |  |  |  | IANA timezone forwarded to the session (e.g. Europe/Paris). |
| `create-recording-path` | session | platform | bool |  |  |  | Session recording — platform-owned (see recording-path). |
| `enable-sftp` | session | platform | bool |  |  |  | File transfer — platform-owned until the file-transfer feature ships with its own policy gate. |
| `recording-name` | session | platform | string |  |  |  | Session recording — platform-owned (see recording-path). |
| `recording-path` | session | platform | string |  |  |  | Session recording — platform-owned until the recording feature ships with its own policy gate. |
| `typescript-path` | session | platform | string |  |  |  | Terminal typescript recording — platform-owned (see recording-path). |
| `read-only` | security | ui | bool |  | false |  | View-only session: display without mouse/keyboard input. |
| `host-key` | security | advanced | string |  |  |  | Expected server host key (Base64); connection is refused on mismatch. |
| `server-alive-interval` | connection | advanced | int | 0 – 3600 |  |  | SSH keep-alive interval in seconds. |
| `hostname` | connection | platform | string |  |  |  | Always the workspace service address, resolved by the operator. |
| `passphrase` | connection | platform | string |  |  |  | SSH key passphrase — comes from the protocol's credentials Secret, never from a CR param. |
| `password` | connection | platform | string |  |  |  | Desktop credential — comes from the protocol's credentials Secret, never from a CR param. |
| `port` | connection | platform | int |  |  |  | Always the workspace protocol port, resolved by the operator. |
| `private-key` | connection | platform | string |  |  |  | SSH private key — comes from the protocol's credentials Secret, never from a CR param. |
| `username` | connection | platform | string |  |  |  | Desktop credential — comes from the protocol's credentials Secret, never from a CR param. |
| `wol-send-packet` | connection | platform | bool |  |  |  | Wake-on-LAN — meaningless in-cluster, banned. |

## kasmvnc

| Parameter | Category | Tier | Type | Constraints | Default | Live | Description |
|---|---|---|---|---|---|---|---|

## Adding a parameter

1. Add one entry to the registry in `operator/pkg/params/params.go`:
   name (the guacd wire name), protocols, kind (+ enum/bounds), tier,
   category (the form section it renders under), live flag, description.
   Pick the tier deliberately: security/topology-sensitive parameters
   are `platform`.
2. Run `make docs-params` (this file) and `make test`
   (the registry has coherence tests).
3. Nothing else. The webhook validates it, the api-server serves it on
   `GET /api/v1/meta/protocols`, and the forms render it from
   that payload.
