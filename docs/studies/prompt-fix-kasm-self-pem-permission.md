# Fix — Boot loop kasmvnc : `self.pem` Permission denied dans `~/.vnc`

*2026-07-10 — diagnostic confirmé par reproduction live sur le cluster
dev (k3d, local-path), corrigé dans l'opérateur, vérifié e2e (home neuf
ET volume existant déjà cassé).*

## Symptôme

Au démarrage d'un pod kasmvnc, `vnc_startup.sh` échoue avec :

```
req: Can't open "/home/kasm-user/.vnc/self.pem" for writing, Permission denied
kill: usage: kill [-s sigspec | -n signum | -sigspec] pid | jobspec ...
```

puis le pod boucle en CrashLoopBackOff. Les symlinks Downloads/Uploads
avaient pourtant été créés sans problème par le même utilisateur
`kasm-user` juste avant : les droits manquent **uniquement** sous `.vnc`.

## Diagnostic — cause réelle confirmée

Deux hypothèses étaient sur la table : absence de `fsGroup` par défaut
(`buildPodTemplate` ne force rien), ou auto-création de `.vnc` par le
mount subPath de `kasmvnc.yaml`. **La seconde est la cause réelle**,
reproduite pas à pas sur le cluster dev :

1. `ensureKasmConfig` matérialise la config effective dans une ConfigMap,
   et `buildPodTemplate` la montait en subPath à
   `<home>/.vnc/kasmvnc.yaml` — en supposant (commentaire explicite dans
   `kasm_config.go`) que `.vnc` « reste writable » pour les artefacts
   runtime.
2. Sur un home neuf, le répertoire parent du subPath n'existe pas : **le
   kubelet le crée lui-même, en root:root 0755**, pendant la préparation
   des mounts du conteneur. Constaté sur le PV :

   ```
   drwxrwxrwx 9    0    0  .          ← racine du volume (0777, local-path)
   drwxrwxrwx 2 1000 1000 Desktop     ← créé par kasm-user, OK
   drwxr-xr-x 2    0    0  .vnc       ← créé par le kubelet, root, 0755
   ```

3. Le conteneur tourne en `kasm-user` (uid 1000, `runAsNonRoot: true`,
   pas de capability chown) : il écrit partout dans le home (racine
   0777) **sauf** dans `.vnc` → `self.pem` échoue → boot loop. Le chown
   « premier boot » de l'image kasmweb supposé par `placement.go` ne
   peut rien faire : le process n'est pas root.

### Pourquoi pas le fsGroup par défaut

- **Il ne corrigerait pas le cluster dev** : les PV de
  local-path-provisioner sont de type hostPath/local, auxquels le
  kubelet n'applique **pas** la gestion fsGroup.
- Même sur un CSI qui l'applique, `SetVolumeOwnership` opère au montage
  du volume, **avant** la création des parents de subPath : le `.vnc`
  créé par le kubelet resterait root:root 0755 au premier démarrage
  (celui qui casse).
- Imposer un gid par défaut à toutes les images (le champ est un
  passthrough template/overrides) serait un changement de contrat bien
  plus large que le bug.

### Pourquoi pas déplacer la config vers `/etc/kasmvnc/kasmvnc.yaml`

KasmVNC merge `defaults < /etc/kasmvnc < ~/.vnc` : au niveau `/etc`, un
utilisateur pourrait écrire son propre `~/.vnc/kasmvnc.yaml` persistant
dans son home et **écraser la couche DLP clipboard** (Feature 11). La
config doit rester au niveau utilisateur, montée read-only par-dessus.

## Correctif retenu

`workload.go` : quand le template est kasmvnc, `.vnc` devient un
**emptyDir dédié** (`kasmvnc-dir`) monté à `<home>/.vnc`, et le fichier
`kasmvnc.yaml` reste un subPath read-only monté **par-dessus** (mount
imbriqué, le répertoire déclaré avant le fichier). Propriétés :

- le kubelet n'a plus jamais à créer `.vnc` sur le PVC : le parent du
  subPath est le point de montage emptyDir ;
- les emptyDir sont world-writable (0777) par conception → **aucune
  hypothèse d'uid** : n'importe quelle image non-root écrit ses
  artefacts (`self.pem`, `passwd`, logs, `xstartup`) ;
- tout le contenu de `.vnc` est régénéré au boot par les scripts kasm —
  rien n'y nécessite la persistance du home (cert self-signed, pid,
  logs) ;
- **propriété curative** : un volume déjà cassé (`.vnc` root-owned
  persisté sur le PVC) est masqué par le mount emptyDir — vérifié live,
  le workspace en CrashLoop est reparti sain après un cycle
  pause/resume ;
- gouvernance intacte : le `kasmvnc.yaml` monté reste immuable pour
  l'utilisateur (bind read-only, `rm`/écriture refusés — vérifié), et
  `.vnc` éphémère supprime même la possibilité d'un shadow persistant.

`hack/dev/templates-dev.yaml` reste inchangé : aucun `fsGroup` requis.

## Vérification

- Repro avant fix : workspace neuf sur `kasm-terminal`
  (`homeMountPath: /home/kasm-user`, pas de fsGroup) → CrashLoopBackOff
  avec le log exact du rapport ; `.vnc` root:root 0755 constaté sur le
  PV.
- Après fix : même scénario → pod `1/1 Running`, `self.pem` écrit par
  uid 1000, ConfigMap montée (couche DLP incluse) ; idem sur le volume
  déjà cassé (guérison) ; test de mutation du fichier refusé.
- Test unitaire : `TestKasmVncDirNeverAutoCreatedOnHome`
  (`kasm_config_test.go`) fige le volume emptyDir, son mount writable à
  `<home>/.vnc`, et l'ordre répertoire-avant-fichier ;
  `TestKasmConfigAbsentForNonKasmWorkspace` vérifie qu'un template guacd
  n'embarque aucun des deux volumes.

## Observé en marge (hors périmètre, préexistant)

Sur un home **totalement vierge**, le tout premier boot de l'image
kasmweb échoue une fois : `cp -rp /home/kasm-default-profile/. →
'preserving times for /home/kasm-user/.': Operation not permitted` (la
racine du PV local-path appartient à root, uid 1000 ne peut pas en
modifier les timestamps ; `set -e` tue le script). Auto-réparé au
restart suivant : `.bashrc` existe alors et la copie de profil est
sautée. Indépendant du bug `.vnc` (c'est même lui qui masquait ce
premier échec dans les logs rapportés) ; à traiter séparément si le
restart cosmétique du premier démarrage gêne.
