# Hardening checklist

Applies to **every** image built from this repo. Each item says where it
is enforced, so the list is verifiable, not aspirational.

## Enforced at build time (Dockerfile)

- [x] **Non-root user** with fixed, build-arg-configurable UID/GID
      (default `1000:1000`, user `user`, home `/home/user`).
      Ubuntu 24.04's default `ubuntu` user is removed.
      → `base/ubuntu/Dockerfile`, `USER` directive; verify:
      `docker run --rm <img> id` → `uid=1000`.
- [x] **No setuid/setgid binaries**: all `+s` bits stripped after every
      apt layer (base and derived images re-assert after installs).
      → verify: `docker run --rm <img> find / -xdev -perm /6000 -type f`
      → empty.
- [x] **No secrets in the image**: password arrives at runtime via env,
      hashed to a 0600 file in tmpfs, then scrubbed from the environment.
      CI runs `trivy --scanners vuln,secret` as a gate.
      → `waas-entrypoint`; verify: `docker history`, trivy job.
- [x] **Minimal packages**: `--no-install-recommends` everywhere,
      `apt-get clean`, apt lists / caches / logs removed, xterm purged
      from the XFCE layer.
- [x] **Pinned supply chain**: base image pinned by Renovate digest pin;
      Mozilla APT repo verified against its published key fingerprint and
      priority-pinned; CI tool images version-pinned.

## Enforced at runtime (image design + smoke test)

- [x] **Read-only rootfs compatible**: only `/home/user`, `/tmp`, `/run`
      are written. CI smoke-runs every image with `--read-only
      --cap-drop ALL --security-opt no-new-privileges` — a regression
      fails the pipeline.
      → `ci/smoke_test.sh`.
- [x] **Zero capabilities required**: all ports > 1024, no PAM, no
      chown at startup (fsGroup handles the PVC). `--cap-drop ALL` in CI.
- [x] **X display protected** by a MIT-MAGIC-COOKIE (no `Xvnc -ac`).
- [x] **VNC auth always on** (`-SecurityTypes VncAuth`, `-rfbauth`);
      empty password refuses to start. xrdp: `crypt_level=high`,
      `security_layer=negotiate` with TLS cert (provided or ephemeral).

## To apply on the platform side (documented contract)

- [ ] Pod `securityContext` (recommended for the operator's pod spec):
      `runAsNonRoot`, `runAsUser/fsGroup: 1000`,
      `seccompProfile: RuntimeDefault`, `allowPrivilegeEscalation: false`,
      `capabilities.drop: [ALL]`, `readOnlyRootFilesystem: true` +
      emptyDir on `/tmp` and `/run`. Meets PodSecurity **restricted**.
      AppArmor: `runtime/default` is sufficient; no custom profile needed.
- [ ] `examples/networkpolicy-workspaces.yaml`: only guacd reaches
      5901/3389; no east-west between workspaces.
- [ ] Template credentials from Vault/ESO (see README § Secrets).

## Threat model for desktop traffic

The browser session is TLS-terminated at the ingress; wwt→guacd→workspace
runs on the pod network. VNC/RDP between guacd and the workspace is
**cleartext by default**, accepted because: (1) both endpoints live in
the same cluster namespace, (2) the NetworkPolicy above restricts the
path to guacd exactly, (3) guacd's VNC client support for X509/VeNCrypt
is unreliable, so forcing TLS there would break the primary protocol.
If the pod network itself is in scope (multi-tenant nodes, no CNI
encryption), enable WireGuard/IPsec at the CNI layer (Cilium/Calico)
rather than per-protocol TLS — it also covers guacd→wwt. For RDP, TLS
*is* enabled when guacd negotiates it (`security_layer=negotiate`); mount
a real cert via `WAAS_TLS_CERT`/`WAAS_TLS_KEY` to replace the ephemeral
self-signed one.

## Known, accepted gaps (documented, not hidden)

- `/etc/machine-id` is identical across containers of one image (baked
  for read-only dbus); not used as identity by anything shipped.
- RDP path has no chansrv → no clipboard/audio over RDP (VNC path has
  clipboard via vncconfig). VNC is the recommended Linux protocol.
- Audio is not shipped: `pulseaudio-module-xrdp` is not packaged in
  Ubuntu and the guacd VNC path has no audio channel. Revisit if/when a
  KasmVNC-compatible gateway lands.
- Firefox's *internal* process sandbox degrades in the container
  (`CanCreateUserNamespace: EPERM`): unprivileged user namespaces are
  blocked by the seccomp/caps profile. Deliberate: the pod (non-root,
  no caps, read-only, seccomp, NetworkPolicy) is the sandbox boundary
  here; granting CAP_SYS_ADMIN to restore Firefox's inner sandbox would
  weaken the outer one.
