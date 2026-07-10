# Matrice protocole × fonctionnalité (2026-07-10)

État des lieux sourcé des quatre chemins de connexion (VNC, RDP, SSH —
brokered guacd — et KasmVNC — reverse-proxy HTTP brut par
`wwt/internal/kasm`, sans guacd), croisés avec les fonctionnalités
transverses. Chaque case est vérifiée dans le code (fichier:ligne) ou
dans une doc existante ; rien n'est estimé. Ce document ne redit pas ce
que [templates-and-protocols.md](../templates-and-protocols.md)
documente déjà (modèle template/overrides, tiers de formulaires,
credentials) — il le référence.

**Sources de vérité et fraîcheur** :

- Le registre `operator/pkg/params/params.go` (chaque `Param` porte
  `Protocols`, `Tier`, `Live`, `Default`) est la source unique ;
  `docs/guacd-parameters.md` en est généré. Régénéré le 2026-07-10
  (`make docs-params`) : **aucun diff**, le fichier était à jour.
- `kasmvnc` est dans `Protocols()` (`params.go:447`) mais **aucune
  entrée du registre ne le liste dans `Protocols`** : tout paramètre
  guacd posé sur un protocole kasmvnc est rejeté fail-closed
  (`Lookup` retourne nil → violation, `params.go:450-462, 505-519`).
  Le seul canal de configuration kasmvnc est `spec.kasmvncConfig`
  (YAML opaque admin-only, jamais parsé —
  [templates-and-protocols.md](../templates-and-protocols.md) §KasmVNC).
- Comportements runtime non testables sans cluster : marqués ❓ avec la
  raison, jamais devinés.

**Légende** : ✅ `ui` = exposé dans les formulaires portail · ⚙️
`advanced` = CR/YAML ou section avancée seulement · 🚫 `platform` =
bloqué plateforme (raison dans la `Description` du registre) · ❌ =
inexistant sur ce chemin · N/A = sans objet (ex. terminal SSH) · ❓ =
non vérifiable sans session live. Un astérisque renvoie aux notes sous
le tableau.

## Tableau principal

| Fonctionnalité | VNC | RDP | SSH | KasmVNC |
|---|---|---|---|---|
| 1. Audio — lecture | ✅ ui `enable-audio` *[1]* | ✅ ui `disable-audio` (défaut guacd : actif) *[1]* | N/A | ❌ *[2]* |
| 2. Audio — micro | ❌ (aucun paramètre) | ⚙️ advanced `enable-audio-input`, **inerte côté client web** *[3]* | N/A | ❌ *[2]* |
| 3. Clipboard gouverné (policy + live) | ✅ ui, `Live` *[4]* | ✅ ui, `Live` — canal absent des images internes *[4]* | ✅ ui, `Live` *[4]* | ❌ **non gouverné** *[5]* |
| 4. Home persistant | ✅ (indépendant du protocole) *[6]* | ✅ *[6]* | ✅ *[6]* | ✅ (`/home/kasm-user`) *[6]* |
| 4b. Volume partagé concurrent | ❌ *[6]* | ❌ *[6]* | ❌ *[6]* | ❌ *[6]* |
| 5. Transfert de fichiers | 🚫 platform *[7]* | 🚫 platform *[7]* | 🚫 platform *[7]* | ❌ *[7]* |
| 6. Enregistrement de session | 🚫 platform *[8]* | 🚫 platform *[8]* | 🚫 platform *[8]* | ❌ *[8]* |
| 7. Disposition clavier | N/A (keysyms directs) *[9]* | ✅ ui `server-layout` + auto-détection *[9]* | N/A (keysyms directs) *[9]* | N/A *[9]* |
| 8. Resize dynamique | ❌ *[10]* | ✅ ui `resize-method`, **inerte aujourd'hui** *[10]* | ❌ (taille fixée au handshake) *[10]* | ✅ natif, exploité *[10]* |
| 9. Multi-écran | ❌ | ❌ | N/A | ❓ *[11]* |
| 10. Qualité d'image / couleur | ✅ ui ×4 *[12]* | ✅ ui + ⚙️ advanced ×9 *[12]* | ✅ ui (aspect terminal) *[12]* | ⚙️ via `kasmvncConfig` seulement *[12]* |
| 11. Paramètres à chaud (`Live`) | `disable-copy`/`disable-paste` uniquement *[13]* | idem *[13]* | idem *[13]* | ❌ aucun *[13]* |
| 12. Overrides workspace post-création | ✅ implémenté (Feature 1) *[14]* | ✅ *[14]* | ✅ *[14]* | ✅ *[14]* |

Notes (sources) :

1. `enable-audio` : `params.go:116` (ui, VNC) ; `audio-servername` :
   `params.go:121` (advanced). `disable-audio` : `params.go:160` (ui,
   RDP) ; `console-audio` : `params.go:250` (advanced). **Mais** aucune
   image interne ne fournit d'audio : `waas-images/HARDENING.md:78-82`
   (« Audio is not shipped », pas de PulseAudio, pas de chansrv xrdp).
   Voir §Audio.
2. Capacité « plateforme Kasm uniquement », hors périmètre standalone :
   `docs/studies/kasm-images-feasibility.md:42-43` — toujours exact :
   `wwt/internal/kasm/kasm.go` est un reverse-proxy pur, sans aucun
   flux audio ajouté.
3. `enable-audio-input` : `params.go:165` (advanced, RDP). Aucune
   capture micro côté client : zéro occurrence de
   `AudioRecorder`/`createAudioStream` dans `frontend/src` (vérifié par
   grep le 2026-07-10). Voir §Audio.
4. `disable-copy`/`disable-paste` : `params.go:84,89` (ui, `Live:
   true`, VNC+RDP+SSH). Enforcement wwt : `wwt/internal/guac/clipboard.go`
   (drop des streams + toggles live clampés au grant). Limite RDP image
   interne : `waas-images/HARDENING.md:78-79`. Voir §Clipboard.
5. Aucun équivalent du `ClipboardFilter` sur le chemin kasm :
   `wwt/internal/kasm/kasm.go` proxifie octets et WebSocket sans
   inspection (`proxyTo`, `kasm.go:167-202`). Décision v1 assumée
   (`kasm-images-feasibility.md:86`). Voir §Clipboard et §Écarts.
6. `docs/volumes.md` (PVC = source de vérité, rétention par labels) ;
   RWO + stratégie `Recreate` interdisent le double-montage
   (`templates-and-protocols.md` §Workload). `homeVolumeName` = adoption
   d'un volume **retained** à la création (`volumes.md:31-35`), pas un
   partage concurrent. Voir §Volumes.
7. `enable-sftp` : `params.go:365` ; `enable-drive` : `params.go:369` ;
   `sftp-hostname` banni : `params.go:409` — tous `TierPlatform`,
   raison : « until the file-transfer feature ships with its own policy
   gate ». KasmVNC : upload/download = plateforme Kasm uniquement
   (`kasm-images-feasibility.md:42-43`). Voir §Transfert & enregistrement.
8. `recording-path`/`recording-name`/`create-recording-path` :
   `params.go:393-403` ; `typescript-path` (SSH) : `params.go:405` —
   tous `TierPlatform`, même raison d'attente de policy gate. Aucun
   mécanisme d'enregistrement sur le chemin kasm (rien dans
   `wwt/internal/kasm`).
9. `server-layout` : `params.go:172-183` (ui, enum 24 valeurs) ;
   auto-détection locale navigateur : `DesktopPane.tsx:175` (`?layout=`)
   + `wwt/internal/guac/handshake.go:120-127`. VNC/SSH sans équivalent :
   `templates-and-protocols.md` §Keyboard layout (« VNC forwards keysyms
   directly ») — confirmé : aucun paramètre layout VNC/SSH au registre.
10. `resize-method` : `params.go:145` (ui, RDP). Inerte en pratique :
    voir §Resize et §Écarts. KasmVNC : `resize=remote` passé au client
    embarqué (`DesktopPane.tsx:158`), resize dynamique natif confirmé
    par le PoC (`kasm-images-feasibility.md:98`).
11. Capacité standalone KasmVNC d'après l'étude
    (`kasm-images-feasibility.md:42-43`) ; aucune intégration WaaS
    explicite (rien dans `DesktopPane.tsx` ni `wwt/internal/kasm`). Le
    client KasmVNC complet est embarqué en iframe, son UI propre peut
    l'exposer — **non vérifié sans session live** (fenêtres secondaires
    + cookie scopé `/kasm/{sid}` : comportement inconnu).
12. VNC : `color-depth` (`params.go:96`), `swap-red-blue` (`:101`),
    `cursor` (`:106`), `force-lossless` (`:111`) — tous ui. RDP :
    `color-depth` ui + 9 réglages cosmétique/perf advanced
    (`params.go:190-233`). SSH : `font-size` (`:262`), `color-scheme`
    (`:267`) ui. KasmVNC : rien au registre ; `VNC_RESOLUTION` →
    `MaxVideoResolution` (`kasm-images-feasibility.md:98`) et options
    upstream via `kasmvncConfig` (admin, opaque, non validé).
13. `grep 'Live: true' params.go` → lignes 85 et 90 uniquement
    (vérifié le 2026-07-10) : aucun autre paramètre live n'a été ajouté.
    Chemin kasm : pas de tunnel guac, donc pas de messages
    `waas-clipboard` — aucun paramètre à chaud.
14. Feature 1 livrée : commits `c671cbd40f55` (PATCH
    `/workspaces/{id}/overrides` + endpoint reload), `320d746ecaaf`
    (reload one-shot opérateur + ADR), `4a9f41a910c4` (onglet Workspace).
    Voir §Overrides post-création.

## 1-2. Audio — le registre promet, les images ne suivent pas

Trois couches doivent s'aligner ; aujourd'hui **aucune combinaison
première-partie ne produit de son** :

- **Registre/formulaires** : `enable-audio` (VNC) et `disable-audio`
  (RDP) sont `Tier: ui` — le portail les propose dans le mode simple
  (`params.go:116,160` ; rendu générique documenté dans
  `templates-and-protocols.md` §Parameter forms).
- **Client web** : la lecture fonctionnerait sans code spécifique —
  `guacamole-common-js` instancie un `AudioPlayer` par défaut quand
  `client.onaudio` n'est pas posé
  (`frontend/node_modules/guacamole-common-js/dist/esm/guacamole-common.js:2887-2892`).
  En revanche le **micro** exige un câblage explicite
  (`Guacamole.AudioRecorder` + `createAudioStream`) qui n'existe nulle
  part dans `frontend/src` : `enable-audio-input` (`params.go:165`) ne
  peut rien capturer, même si guacd et le serveur RDP l'acceptaient.
- **Images** : `waas-images/HARDENING.md:80-82` — l'audio n'est pas
  embarqué (`pulseaudio-module-xrdp` non packagé Ubuntu ; pas de
  PulseAudio joignable pour le chemin VNC). La description du registre
  est honnête (« requires the image to run one », `params.go:118`) mais
  aucune image du catalogue interne ne satisfait la condition.

**KasmVNC** : l'état décrit dans l'étude de faisabilité est inchangé —
audio (et micro) sont des fonctions de la *plateforme* Kasm, pas du
serveur standalone (`kasm-images-feasibility.md:42-43,54` : ne jamais
installer l'agent Kasm, c'est la frontière de licence). `wwt/internal/kasm`
n'ajoute rien. **SSH** : N/A, guacd ne rend qu'un terminal.

Une session avec audio réel supposerait donc : image tierce avec
PulseAudio joignable (VNC) — **non vérifiable sans cluster ni image
tierce**, marqué tel quel.

## 3. Clipboard — gouverné partout sauf kasm

[clipboard.md](../clipboard.md) documente la chaîne complète
(policy → grant JWT → `ClipboardFilter` wwt → client) et sa matrice
navigateur ; tout ce qui suit la confirme, avec deux réconciliations :

- **La matrice de clipboard.md reste exacte** pour VNC/SSH (les deux
  sens, enforcement wwt indépendant du protocole,
  `wwt/internal/guac/clipboard.go:39-92`, testé
  `clipboard_test.go`). Les toggles overlay sont live et clampés au
  grant (`DesktopPane.tsx:92-94` → messages `waas-clipboard`).
- **RDP, à durcir dans clipboard.md** : la doc dit « xrdp sans sesman ne
  monte pas *toujours* le canal cliprdr » (`clipboard.md:66-67`) ;
  `waas-images/HARDENING.md:78-79` est plus catégorique — *pas de
  chansrv du tout* → jamais de clipboard RDP avec l'image interne. La
  nuance a un sens pour les **remote workspaces** : un vrai serveur RDP
  (Windows) monte cliprdr, et le filtre wwt s'applique alors normalement
  (même tunnel, même token — `docs/remote-workspaces.md:108-109`).
- **KasmVNC : rien n'est gouverné.** Vérifié dans
  `wwt/internal/kasm/kasm.go` : le handler valide le JWT, résout la
  session, puis proxifie tout (`httputil.ReverseProxy`,
  `kasm.go:167-202`) — aucune inspection du flux, aucun équivalent du
  `ClipboardFilter`. Le clipboard fonctionne via l'UI propre de KasmVNC
  dans l'iframe, **hors de portée de la policy**. C'est la décision v1
  assumée (`kasm-images-feasibility.md:86`), mitigation prévue en
  phase 4 (DLP KasmVNC dérivé de la policy). Mais l'« affichage
  honnête » qui accompagnait cette décision n'est pas au rendez-vous —
  voir §Écarts.
- Paramètres connexes : `clipboard-encoding` (VNC, advanced,
  `params.go:136`) et `normalize-clipboard` (RDP, advanced,
  `params.go:235`) existent mais ne changent pas la gouvernance.

## 4. Volumes — home persistant oui, partage non

Le modèle complet est dans [volumes.md](../volumes.md) (PVC = source de
vérité, rétention par labels, quotas). Pour la matrice :

- Le **home PVC est monté sur tous les chemins in-cluster**, protocole
  compris kasmvnc (`homeMountPath` `/home/kasm-user` pour les images
  Kasm, persistance validée au PoC — `kasm-images-feasibility.md:101`).
  La ligne 4 est identique sur les 4 colonnes car le volume est une
  propriété du workspace, pas du protocole.
- Il n'existe **pas de volume partagé entre workspaces** : le PVC est
  RWO avec stratégie `Recreate` précisément pour que deux pods ne se
  chevauchent jamais (`templates-and-protocols.md` §Workload), et
  `homeVolumeName` n'autorise que l'**adoption d'un volume `retained`**
  (workspace précédent supprimé) par son propriétaire, dans le même
  namespace (`volumes.md:31-35`). Adoption séquentielle ≠ partage
  concurrent.
- **Remote workspaces** : N/A — aucune ressource de stockage côté
  plateforme (`model.go:82-86` : « no template, no operator lifecycle,
  no compute »).

## 5-6. Transfert de fichiers et enregistrement — bloqués volontairement

À dire sans ambiguïté : **non supportés aujourd'hui**, sur tous les
chemins, pour tout le monde (admins compris — le tier `platform` est
rejeté « whoever asks », `params.go:34-37`).

- Transfert : `enable-sftp` (VNC/RDP/SSH) et `enable-drive` (RDP) sont
  `TierPlatform` avec la raison explicite « until the file-transfer
  feature ships with its own policy gate » (`params.go:365-371`) ;
  toute la famille `sftp-*` est de plus non enregistrée à dessein,
  `sftp-hostname` étant banni comme side-channel (`params.go:409-411`).
  Ce ne sont **pas** des paramètres « disponibles en advanced » : le
  webhook (`workspacetemplate_webhook.go:63-67`), le connect
  (`workspace_service.go:548`) et le chemin remote
  (`remote_workspace_service.go:186,398`) les rejettent tous via le
  même registre.
- Enregistrement : `recording-path`, `recording-name`,
  `create-recording-path` (VNC/RDP/SSH) et `typescript-path` (SSH) —
  même statut, même raison (`params.go:393-407`).
- KasmVNC : pas d'équivalent (upload/download = plateforme Kasm,
  `kasm-images-feasibility.md:42-43`) — cohérent avec le blocage côté
  guacd : aucune fuite de fichiers par aucun chemin.

## 7. Clavier — RDP seulement, et c'est structurel

Déjà documenté en détail dans `templates-and-protocols.md` §Keyboard
layout (auto) ; pour la matrice : seul RDP négocie un layout
(`server-layout`, ui, enum de 24 layouts + `failsafe`,
`params.go:172-183`), avec défaut auto-détecté depuis la locale
navigateur (`DesktopPane.tsx:175` → `handshake.go:120-127`). VNC et le
terminal SSH transmettent des keysyms — le layout est celui du serveur
X / du shell, il n'y a **rien à configurer côté guacd** (vérifié : aucun
paramètre layout VNC/SSH au registre). KasmVNC : même logique keysym
côté client embarqué, aucun paramètre plateforme.

## 8. Resize — la seule vraie réussite est kasm

État vérifié des trois mécanismes :

- **Chemin guacd (VNC/RDP/SSH)** : la taille d'affichage est fixée une
  fois au handshake — le client envoie `width`/`height`/`dpi` en query
  du WebSocket (`DesktopPane.tsx:167-171`), wwt les traduit en
  instruction `size` (`handshake.go:54-65`, défauts 1920×1080@96).
  Ensuite, le `ResizeObserver` du pane ne fait que **rescaler le canvas
  côté client** (`DesktopPane.tsx:117-125,292-293`) ; aucun
  `client.sendSize()` nulle part (`grep sendSize frontend/src wwt` :
  zéro occurrence, 2026-07-10). Conséquences :
  - VNC : pas de resize dynamique, et c'est cohérent — aucun paramètre
    registre, la géométrie est celle du serveur Xvnc ;
  - RDP : `resize-method` (ui, `params.go:145`) choisit comment guacd
    *propagerait* un resize… qui n'est jamais émis → paramètre inerte
    aujourd'hui (voir §Écarts) ;
  - SSH : le terminal reste à la taille du handshake, mis à l'échelle
    en CSS.
- **KasmVNC** : le resize dynamique natif (`AcceptSetDesktopSize`) est
  bien **exploité** : l'iframe est montée avec `resize=remote`
  (`DesktopPane.tsx:156-163`), le client KasmVNC pilote la géométrie
  réelle (PoC : `kasm-images-feasibility.md:98`). C'est aujourd'hui le
  seul chemin où la fenêtre navigateur redimensionne réellement le
  bureau.

## 9. Multi-écran — resté théorique

Aucune trace côté WaaS : ni `DesktopPane.tsx` (un pane = un canvas ou
une iframe), ni `wwt/internal/kasm`, ni le split view (qui juxtapose
des *sessions*, pas des écrans d'une même session —
`templates-and-protocols.md` §Portal UX). La capacité citée par l'étude
de faisabilité (`kasm-images-feasibility.md:42-43`) est celle du client
KasmVNC upstream, embarqué entier dans l'iframe : son UI interne peut
proposer l'ouverture d'écrans secondaires, mais cela passe par des
fenêtres popup dont l'interaction avec le cookie scopé
`Path=/kasm/{sid}` (`kasm.go:136-143`) n'a jamais été exercée —
**❓ non vérifié, à tester en session live avant toute promesse**.

## 11. Paramètres à chaud — deux, et seulement deux

`grep 'Live: true'` sur le registre (2026-07-10) : `params.go:85` et
`params.go:90` — `disable-copy` et `disable-paste`, rien d'autre depuis
leur introduction. Le mécanisme (messages tunnel `waas-clipboard`
interceptés par wwt, clampés au grant) ne concerne que le tunnel guac ;
le chemin kasm n'a ni tunnel ni paramètre live. Tout le reste exige une
reconnexion (guacd fige les paramètres au connect, `params.go:66-68`) —
l'overlay distingue d'ailleurs les réglages « live » des réglages
« reconnect » (`SessionOverlay.tsx:113-117,249-264`).

## 12. Overrides workspace post-création — Feature 1 livrée

L'étude s'écrit **après** l'implémentation de la Feature 1
(`docs/studies/prompt-feature1-workspace-runtime-config.md`) ; le
statut réel :

- `PATCH /api/v1/workspaces/{id}/overrides` accepte `env`,
  `nodeSelector`, `tolerations`, `resources` (sémantique
  « présence = remplacement », `workspace_service.go:384-397`) ; les
  droits restent jugés par le webhook seul (intersection
  template ∩ policy, re-déniée en 403 —
  `workspace_service.go:399-406`), audit
  `workspace.overrides_updated` (noms d'env seulement, jamais les
  valeurs).
- L'application au pod suit l'ADR 0001
  (`docs/adr/0001-template-boundary-convergence.md`) : convergence aux
  frontières de scale-up (pause/reprise), jamais en pleine session ;
  entre-temps condition `TemplateDrifted` + badge cliquable → **reload
  manuel confirmé** (annotation one-shot opérateur, commit
  `320d746ecaaf` ; UI commit `4a9f41a910c4`, onglet « Workspace » de
  Connection settings, `frontend/src/dialogs/WorkspaceRuntimeForm.tsx`).
- Indépendant du protocole (c'est le workload qui change), donc ✅ sur
  les 4 colonnes. À ne pas confondre avec les **params de connexion**
  post-création, qui ont toujours été possibles au connect
  (`templates-and-protocols.md` §SSH, point `/connect`).

## Workspaces distants (`RemoteWorkspace`) — mêmes protocoles, gouvernance allégée

Modèle complet dans [remote-workspaces.md](../remote-workspaces.md) ;
différences pertinentes pour la matrice (sessions `kind: "remote"`,
`model.go:61`) :

| Aspect | In-cluster | Remote |
|---|---|---|
| Accès à la feature | toujours | opt-in policy `remoteWorkspaces`, fail-closed (`remote_workspace_service.go:142-159`, `model.go:390-392`) |
| Tiers de paramètres | non-admins bornés au `userParams` du template (`workspace_service.go:548`) | **ui + advanced libres** pour le propriétaire — pas de template, seule la barrière `platform` s'applique (`remote_workspace_service.go:186,396-399`) |
| Clipboard | policy + filtre wwt | **identique** — même grant résolu (`remote_workspace_service.go:417`), même tunnel, même filtre ; et sur un vrai serveur RDP le canal cliprdr existe, contrairement aux images internes |
| Transfert / enregistrement | 🚫 platform | 🚫 platform (même registre) |
| Wake-on-LAN | `wol-send-packet` banni (« meaningless in-cluster », `params.go:373`) | ✅ supporté par un autre mécanisme : relais HTTP externe + `macAddress` (`remote_workspace_service.go:479-499`, `docs/remote-workspaces.md` §WoL) |
| Home / volumes / overrides runtime | ✅ | N/A (rien d'instancié — `docs/frontend-capabilities.md` : pas d'onglet Workspace, pas de badge de dérive) |
| KasmVNC | ✅ (chemin nominal kasmweb) | **accepté par le code, non documenté** : la validation prend tout `params.Protocols()` donc kasmvnc (`remote_workspace_service.go:174`), la résolution applique `kasmDefaults` (`workspace_service.go:810`) et le front route `kind=remote` + `kasmvnc` vers l'iframe (`DesktopPane.tsx:134-135,149`). Mais `remote-workspaces.md:4-5` et le commentaire modèle (`model.go:82-84` « reachable through guacd ») disent ssh/vnc/rdp. **❓ jamais exercé** (le smoke ne couvre pas ce croisement) — à trancher : soit le documenter comme supporté, soit le refuser à l'enregistrement |

## Écarts vs. ce que l'UI laisse penser

Par gravité décroissante :

1. **Overlay clipboard sur session kasmvnc : affiché, inopérant, et non
   enforced.** La section clipboard de l'overlay se rend pour toute
   session, sans distinction de protocole
   (`SessionOverlay.tsx:244-264`) : toggles « live » clampés aux
   capabilities policy, échange manuel. Sur le chemin kasm, (a) les
   toggles appellent `setClipboard` → `tunnelRef.current` est nul →
   no-op silencieux (`DesktopPane.tsx:92-94`), (b) l'échange manuel
   passe par `sendClipboardRef`, resté au no-op par défaut
   (`DesktopPane.tsx:59` — jamais réassigné sur la branche kasm), et
   (c) le vrai clipboard vit dans l'iframe KasmVNC, **hors policy**
   (§Clipboard). L'UI suggère donc une gouvernance qui n'existe pas —
   l'inverse exact de l'« affichage honnête » acté dans
   `kasm-images-feasibility.md:86`.
2. **`resize-method` (RDP, tier ui) est un choix sans effet.** Le
   formulaire propose display-update vs reconnect (`params.go:145-147`),
   mais le client n'émet jamais de resize en cours de session (aucun
   `sendSize`, §Resize) : les deux valeurs sont indistinguables
   aujourd'hui. Soit câbler `sendSize` sur le `ResizeObserver`
   (`DesktopPane.tsx:292`), soit rétrograder le paramètre.
3. **Les toggles audio (tier ui) ne produisent aucun son avec le
   catalogue interne.** `enable-audio` (VNC) et `disable-audio` (RDP)
   apparaissent en mode simple alors qu'aucune image interne n'a de
   chaîne audio (`HARDENING.md:78-82`) ; `enable-audio-input` est de
   plus inerte côté navigateur (§Audio). Le formulaire étant généré du
   registre, il n'y a pas de mensonge *du code* — mais l'utilisateur
   coche une case qui ne peut rien faire.
4. **Clipboard RDP : deux docs qui ne disent pas la même chose.**
   `clipboard.md:66-67` (« ne monte pas toujours cliprdr ») vs
   `HARDENING.md:78-79` (pas de chansrv → jamais). À réconcilier dans
   clipboard.md, en gardant la nuance remote-RDP où le filtre sert
   réellement (§Clipboard).
5. **Remote + kasmvnc : capacité réelle non documentée** (tableau
   §Workspaces distants) — le cas inverse des points précédents : le
   code fait plus que ce que la doc annonce, sans test qui le protège.
6. Mineur — **formulaires kasmvnc vides par construction** :
   `ForProtocol("kasmvnc")` ne retourne rien (`params.go:416-434`),
   donc les tabs protocole du portail n'affichent aucun paramètre pour
   kasmvnc ; la configuration réelle (qualité, DLP, SSL) passe par le
   `kasmvncConfig` opaque admin-only. Cohérent, mais à savoir : le
   « mode avancé » du portail ne montrera jamais rien pour ce
   protocole tant que la phase 4 (« registre params kasmvnc »,
   `kasm-images-feasibility.md:115`) n'existe pas.
7. **Trou de gouvernance kasmvnc — résolu (Feature 11, 2026-07-10).**
   La recherche externe avait révélé que KasmVNC standalone expose une
   API `/api/…` de neuf endpoints (tous « owner credentials ») que le
   proxy `wwt/internal/kasm/kasm.go` relayait sans filtrage. L'audit
   live a confirmé les faits (voir `kasm-images-feasibility.md`
   §« Mise à jour 2026-07-10 ») ; l'arbitrage et la livraison :
   - **`/api/downloads` bloqué** au niveau du proxy wwt (403), parité
     avec `enable-sftp`/`enable-drive` gelés `TierPlatform` sur guacd
     « until the file-transfer feature ships with its own policy gate »
     (`params.go:365-371`). Vérifié live : listing et sous-chemins → 403.
   - **Les huit autres endpoints restent proxifiés** (statu quo
     documenté) : session mono-utilisateur qui s'administre elle-même,
     `kasm_user` est `owner` (flags `wo` dans `.kasmpasswd`), aucun tiers
     à protéger contre l'utilisateur lui-même — il a déjà clavier + écran.
   - **Clipboard réellement gouverné** : l'opérateur dérive les
     directives DLP (`data_loss_prevention.clipboard.{server_to_client,
     client_to_server}.enabled` + `allow_client_to_override_kasm_server_settings:
     false`) depuis `WorkspacePolicy.Clipboard` et les fusionne dans
     `kasmvnc.yaml` (`operator/internal/controller/kasm_config.go`).
     Refuser le clipboard désactive vraiment le copier/coller dans le
     conteneur ; l'overlay affiche l'état appliqué (lu depuis
     `capabilities`), plus de texte statique. Vérifié live (deny →
     `enabled: false`).
   Prompt d'origine : `08-prompt-feature11-kasmvnc-governance-gap.md`.
