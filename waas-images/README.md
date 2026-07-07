# waas-images

OCI images for WaaS Linux workspaces — Kasm-style, 100 % OSS, built for
the platform's Workspace CR (operator → pod → guacd → wwt → browser).

```
base/ubuntu     TigerVNC Xvnc + openbox fallback, optional xrdp   ┐
desktop/xfce    XFCE on top of ubuntu-base-rdp                    ├─ layers
apps/firefox    XFCE + policy-managed Firefox                     ┘
images.yaml     global build config (OS matrix, archs, scan gate)
ci/             pipeline generator, build/smoke scripts
examples/       WorkspaceTemplate + NetworkPolicy
HARDENING.md    verifiable hardening checklist + threat model
```

## Design in one paragraph

TigerVNC's **Xvnc is the display server** (no Xvfb double stack): it
serves RFB 3.8 natively, which is exactly what guacd's VNC client
speaks, and supports RandR resize. **RDP is a bridge**: xrdp without
sesman/PAM, its `libvnc` backend pointed at the local Xvnc
(`password=ask` forwards the RDP password as the VNC password) — fully
non-root, both protocols always show the same session. Services run
under **tini + supervisord**, entirely unprivileged; the entrypoint
renders all mutable config into tmpfs so the rootfs can be read-only.
The web client is guacd/wwt from the platform — no noVNC in the images.

## Contract with the Workspace CR

| Aspect | Value |
|---|---|
| VNC port | `5901` (RFB 3.8, VncAuth) — guacd protocol `vnc`, the default for `os: linux` |
| RDP port | `3389` (TLS negotiated) — only images built with `INSTALL_RDP=1` and `WAAS_RDP_ENABLED=1` |
| Readiness/liveness | TCP open on the template port ⇔ protocol server accepting connections (matches the operator's TCP probes) |
| User | UID/GID `1000:1000` (build-args `WAAS_UID/WAAS_GID`), home **`/home/user`** = operator's PVC mount; fresh PVCs are seeded from `/etc/skel` |
| Writable paths | `/home/user` (PVC), `/tmp`, `/run` (emptyDirs) — everything else read-only-safe |
| Required env | `VNC_PW` (or `VNC_PASSWORD` / `RDP_PASSWORD` — one shared session password). **Refuses to start without it.** |
| Optional env | `VNC_RESOLUTION` (`1920x1080`), `VNC_COL_DEPTH` (`24`), `WAAS_VNC_ENABLED` (`1`), `WAAS_RDP_ENABLED`, `RDP_AUTH_ENABLED` (`true`), `WAAS_STARTUP` (session command), `WAAS_TLS_CERT`/`WAAS_TLS_KEY` (mounted RDP cert) |
| Recommended pod securityContext | `runAsNonRoot`, `runAsUser/fsGroup: 1000`, `readOnlyRootFilesystem: true`, `capabilities.drop: [ALL]`, `allowPrivilegeEscalation: false`, `seccompProfile: RuntimeDefault` → PodSecurity **restricted** compliant |

See `examples/workspacetemplate-xfce.yaml` for a complete template.

**RDP authentication (`RDP_AUTH_ENABLED`, default `true`)**: images are
secure by default — every build ships with RDP client authentication ON
(`ENV RDP_AUTH_ENABLED=true` in the base image), meaning the RDP client
must present the session password, which the xrdp bridge forwards to
Xvnc (`password=ask`). There is no build argument to turn it off: an
image can never leave the pipeline with an open RDP. The only opt-out is
the **runtime** env `RDP_AUTH_ENABLED=false` (lab/dev setups behind
their own gate): the bridge then authenticates to Xvnc itself and any
client reaching `:3389` gets the session — the entrypoint logs a loud
warning when this mode is active. The value must be exactly `true` or
`false`; anything else aborts startup. VNC authentication (VncAuth) is
unaffected in both modes, and a session password is required in every
configuration.

**Secrets**: nothing is baked into images; the password arrives via env
at runtime. Today the api-server reads the guacd-side password from the
template's literal `env` values — until it resolves `secretKeyRef`s,
inject the value into the WorkspaceTemplate manifest itself via
ESO/Vault in your GitOps repo (and RBAC-restrict template reads). The
images are already `valueFrom`-ready; only the api-server needs the
follow-up.

**Resize / clipboard / audio (honest status)**: clipboard works over VNC
(vncconfig bridge). Dynamic resolution is server-side only
(`waas-resize 2560x1440` inside the session) because Guacamole's VNC
channel doesn't push browser resizes; wiring wwt → `waas-resize` is the
clean future fix. Audio and RDP-clipboard are not shipped (no chansrv on
the sesman-less RDP path; guac-VNC has no audio channel) — VNC is the
recommended protocol for Linux, RDP is a compatibility option.

## Build matrix & tagging

Global knobs live in `images.yaml` (OS matrix, default archs, trivy
gate); each image directory has a `manifest.yaml` declaring its
version, parent (`from:`), archs and smoke expectations.
`ci/generate_pipeline.py` discovers manifests, topo-sorts on `from` and
emits one child-pipeline job per variant: **base → desktop → apps in a
single pipeline**, derived images consuming the parent's same-arch tag
pushed minutes earlier (`BASE_IMAGE=<registry>/<parent>:<ver>-g<sha>-<arch>`).

Tags are **immutable**:

- `<version>-g<shortsha>` — pushed from every branch (unique, throwaway)
- `<version>` — pushed from `main` once; CI refuses to move it (bump
  `version` in the manifest instead)
- ArgoCD/templates should pin `<version>` or, better, the digest

Multi-arch: `linux/amd64` + `linux/arm64` built NATIVELY on the amd/arm
runner fleets — no QEMU in the nominal path (per-image override, e.g.
Firefox is amd64-only until packages.mozilla.org's arm64 debs are
validated). Pipeline: hadolint/shellcheck → generate → per image and per
arch *build → smoke → trivy gate → push `-g<sha>-<arch>`*, then a merge
job assembles the manifest list and cosign-signs it (sign only when
`COSIGN_PRIVATE_KEY` is set). Both smoke and scan run on BOTH arches.
Fallback: the CI variable `WAAS_IMAGES_BUILD_STRATEGY=qemu` routes every
build job to the amd fleet under emulation (arm fleet down) — same jobs,
same gates, just slower.

The smoke test is also the hardening gate: every image must boot with
`--read-only --cap-drop ALL --security-opt no-new-privileges` and answer
a real RFB banner / X.224 handshake. See `HARDENING.md`.

## Adding an app image

1. `mkdir apps/<name>` with a `Dockerfile` starting:
   ```dockerfile
   ARG BASE_IMAGE
   FROM ${BASE_IMAGE}
   USER 0
   # apt-get install ... && strip suid bits (copy the firefox layer)
   USER 1000:1000
   ```
2. Add `apps/<name>/manifest.yaml`:
   ```yaml
   name: ubuntu-<name>
   layer: apps
   version: "1.0.0"
   from: ubuntu-xfce        # or ubuntu-base-vnc for single-app style
   variants:
     - name: ubuntu-<name>
       smoke: { vnc: true }
   ```
3. To autostart the app in XFCE, ship an
   `/etc/xdg/autostart/waas-app.desktop`; for single-app on the bare
   base, set `ENV WAAS_STARTUP="<command>"` instead.
4. Push — the generator picks the directory up automatically; nothing
   else to edit. New OS/distro = new directory under `base/` + an entry
   in `images.yaml`'s `os:` map.

Local loop: `make build|run|smoke IMAGE=ubuntu-xfce` (same scripts as CI;
`make run` serves VNC on `localhost:15901`, password `devpassword`).
