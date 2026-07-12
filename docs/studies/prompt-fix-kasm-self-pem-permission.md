# Fix — kasmvnc boot loop: `self.pem` Permission denied in `~/.vnc`

*2026-07-10 — diagnosis confirmed by live reproduction on the dev
cluster (k3d, local-path), fixed in the operator, verified e2e (fresh
home AND already-broken existing volume).*

## Symptom

On startup of a kasmvnc pod, `vnc_startup.sh` fails with:

```
req: Can't open "/home/kasm-user/.vnc/self.pem" for writing, Permission denied
kill: usage: kill [-s sigspec | -n signum | -sigspec] pid | jobspec ...
```

then the pod loops in CrashLoopBackOff. Yet the Downloads/Uploads
symlinks had just been created without any issue by the same
`kasm-user` user right before: permissions are missing **only** under
`.vnc`.

## Diagnosis — confirmed root cause

Two hypotheses were on the table: absence of a default `fsGroup`
(`buildPodTemplate` doesn't force anything), or auto-creation of `.vnc`
by the `kasmvnc.yaml` subPath mount. **The second is the real cause**,
reproduced step by step on the dev cluster:

1. `ensureKasmConfig` materializes the effective config into a
   ConfigMap, and `buildPodTemplate` mounted it as a subPath at
   `<home>/.vnc/kasmvnc.yaml` — assuming (explicit comment in
   `kasm_config.go`) that `.vnc` "stays writable" for runtime
   artifacts.
2. On a fresh home, the subPath's parent directory doesn't exist: **the
   kubelet creates it itself, as root:root 0755**, while preparing the
   container's mounts. Observed on the PV:

   ```
   drwxrwxrwx 9    0    0  .          ← volume root (0777, local-path)
   drwxrwxrwx 2 1000 1000 Desktop     ← created by kasm-user, OK
   drwxr-xr-x 2    0    0  .vnc       ← created by the kubelet, root, 0755
   ```

3. The container runs as `kasm-user` (uid 1000, `runAsNonRoot: true`,
   no chown capability): it writes everywhere in the home (root 0777)
   **except** in `.vnc` → `self.pem` fails → boot loop. The "first
   boot" chown of the kasmweb image assumed by `placement.go` can't do
   anything: the process isn't root.

### Why not the default fsGroup

- **It wouldn't fix the dev cluster**: local-path-provisioner PVs are
  of type hostPath/local, to which the kubelet does **not** apply
  fsGroup management.
- Even on a CSI that does apply it, `SetVolumeOwnership` operates at
  volume mount time, **before** the subPath parents are created: the
  `.vnc` created by the kubelet would remain root:root 0755 on the
  first startup (the one that breaks).
- Imposing a default gid on all images (the field is a
  template/overrides passthrough) would be a much broader contract
  change than the bug warrants.

### Why not move the config to `/etc/kasmvnc/kasmvnc.yaml`

KasmVNC merges `defaults < /etc/kasmvnc < ~/.vnc`: at the `/etc` level,
a user could write their own `~/.vnc/kasmvnc.yaml` persisted in their
home and **override the clipboard DLP layer** (Feature 11). The config
must stay at the user level, mounted read-only on top.

## Fix adopted

`workload.go`: when the template is kasmvnc, `.vnc` becomes a
**dedicated emptyDir** (`kasmvnc-dir`) mounted at `<home>/.vnc`, and the
`kasmvnc.yaml` file remains a read-only subPath mounted **on top**
(nested mount, the directory declared before the file). Properties:

- the kubelet never again has to create `.vnc` on the PVC: the
  subPath's parent is the emptyDir mount point;
- emptyDirs are world-writable (0777) by design → **no uid assumption**:
  any non-root image writes its artifacts (`self.pem`, `passwd`, logs,
  `xstartup`);
- the entire content of `.vnc` is regenerated at boot by the kasm
  scripts — nothing there needs home persistence (self-signed cert,
  pid, logs);
- **curative property**: an already-broken volume (`.vnc` root-owned
  persisted on the PVC) is masked by the emptyDir mount — verified
  live, the workspace stuck in CrashLoop came back healthy after a
  pause/resume cycle;
- governance intact: the mounted `kasmvnc.yaml` remains immutable to
  the user (read-only bind, `rm`/write refused — verified), and the
  ephemeral `.vnc` even removes the possibility of a persistent shadow
  copy.

`hack/dev/templates-dev.yaml` remains unchanged: no `fsGroup` required.

## Verification

- Repro before the fix: fresh workspace on `kasm-terminal`
  (`homeMountPath: /home/kasm-user`, no fsGroup) → CrashLoopBackOff
  with the exact log from the report; `.vnc` root:root 0755 observed on
  the PV.
- After the fix: same scenario → pod `1/1 Running`, `self.pem` written
  by uid 1000, ConfigMap mounted (DLP layer included); same for the
  already-broken volume (healed); file mutation test refused.
- Unit test: `TestKasmVncDirNeverAutoCreatedOnHome`
  (`kasm_config_test.go`) pins down the emptyDir volume, its writable
  mount at `<home>/.vnc`, and the directory-before-file ordering;
  `TestKasmConfigAbsentForNonKasmWorkspace` verifies that a guacd
  template carries neither of the two volumes.

## Observed on the side (out of scope, pre-existing)

On a **totally blank** home, the very first boot of the kasmweb image
fails once: `cp -rp /home/kasm-default-profile/. →
'preserving times for /home/kasm-user/.': Operation not permitted` (the
root of the local-path PV is owned by root, uid 1000 cannot change its
timestamps; `set -e` kills the script). Self-healed on the next
restart: `.bashrc` then exists and the profile copy is skipped.
Independent of the `.vnc` bug (it's actually what was masking this
first failure in the reported logs); to be handled separately if the
cosmetic restart on first boot becomes a problem.
