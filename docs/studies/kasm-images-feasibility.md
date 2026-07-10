# Étude de faisabilité — Support des images de workspace Kasm (`kasmweb/*`)

*2026-07-08 — étude validée, PoC phase 0 passé (GO). Décisions actées :
Docker Hub direct (pas de miroir privé), images amd64-only acceptées,
architecture hybride retenue, clipboard non gouverné acceptable en v1
avec affichage honnête.*

## Objectif et problématique

Étendre le catalogue d'images avec les images officielles Kasm
(desktops/apps conteneurisés), **sans utiliser la plateforme Kasm
Workspaces** (broker/UI propriétaire) — WaaS reste le broker. Le nœud :
ces images embarquent **KasmVNC**, qui a rompu avec RFB et ne s'accède
que par navigateur (websocket HTTPS, port 6901, cert self-signed,
Basic auth). **guacd ne peut pas s'y connecter.**

## Audit de l'existant (points de couplage)

| Couche | État | Couplage guacd |
|---|---|---|
| CRDs | `WorkspaceProtocol{name,port,params,userParams}`, enum `vnc;rdp;ssh` (template **et** image) | Enum + registre de params 100 % guacd |
| Data plane | navigateur (guacamole-common-js) → ingress `/ws` → wwt (JWT/JWKS, session→`ConnectionInfo`) → dial `WWT_GUACD_ADDR` | wwt ne parle QUE le tunnel guacamole |
| Auth | go-oidc générique (`WAAS_OIDC_*`) — **déjà IDP-agnostique** | aucun |
| Opérateur | Service expose les ports des protocols ; home monté sur `/home/user` (constante) ; `Architectures` → nodeAffinity déjà en place ; passthrough volumes/securityContext | faible |
| Gouvernance | Politique clipboard appliquée **par le ClipboardFilter de wwt** | contournée si on contourne le tunnel |
| Sessions | fermées par callback wwt + SessionSweeper | fin de session détectée par wwt |

Quatre points à traiter : enum protocole, home path, clipboard policy,
détection de fin de session. Le reste est réutilisable tel quel.

## Vérifications KasmVNC (état courant, vérifié)

- **1.4.0** (oct. 2025). Navigateur uniquement (« does not support legacy
  VNC viewer applications »). Config YAML `/etc/kasmvnc/kasmvnc.yaml` +
  `~/.vnc/kasmvnc.yaml` ; `network.websocket_port` (6901 dans les
  images), `network.ssl.require_ssl: true`, Basic auth via
  `kasm_password_file` (`kasmvncpasswd`).
- Images standalone (documenté) : `docker run --shm-size=512m -p
  6901:6901 -e VNC_PW=... kasmweb/...` → `https://<ip>:6901`, user
  `kasm_user`. Aucune dépendance plateforme.
- **Standalone** : clipboard, resize dynamique, multi-monitor, DLP
  serveur. **Plateforme uniquement** : audio, micro. **Corrigé
  2026-07-10** (voir §Audit API kasmvnc plus bas) : le téléchargement
  de fichiers (session → navigateur) N'EST PAS plateforme-only — c'est
  une capacité standalone (`/api/downloads`) que WaaS expose déjà sans
  le savoir. Seul l'upload (navigateur → session) est réellement absent
  de KasmVNC, à toutes les échelles (ni standalone ni plateforme ne
  l'implémentent selon le mainteneur amont).
- **ARM64 (Docker Hub, tags 1.18/1.19)** — multi-arch : `core-ubuntu-*`,
  `ubuntu-noble-desktop`, `firefox`, `vs-code`, `terminal` ; amd64-only :
  `desktop`, `chrome` (acceptées, placement nodeAffinity existant).

## Licences (textes vérifiés)

| Artefact | Licence | Droits dans notre contexte | Obligations / risques |
|---|---|---|---|
| KasmVNC (serveur) | **GPL-2.0** | Exécution illimitée (y compris commerciale) : la GPL ne restreint que la distribution | Redistribution publique d'images contenant KasmVNC ⇒ GPLv2 §3 (source). Modification ⇒ sources modifiées. **On ne fait ni l'un ni l'autre ⇒ zéro obligation** |
| Repos `workspaces-images` / `workspaces-core-images` | Texte **MIT** + disclaimer : couvre uniquement les Dockerfiles/scripts, PAS les dépendances embarquées | Copier/adapter les Dockerfiles | L'image construite est un agrégat (MIT + GPL + Ubuntu + EULAs d'apps — Chrome). Pull Docker Hub : OK. Republication publique : à éviter |
| Plateforme Kasm Workspaces | Propriétaire ; Community = 5 sessions concurrentes, non-commercial | **Aucun composant utilisé** — le standalone est documenté sans dépendance ni phone-home | Aucune. Ne jamais installer d'agent Kasm (audio/upload) : c'est ce qui ferait entrer dans leur licence |

## Architecture retenue (hybride)

- **guacd intact** pour les images maison (VNC/RDP/SSH).
- Nouveau protocole **`kasmvnc`** implémenté par **wwt en mode
  reverse-proxy brut** : auth JWT plateforme (inchangée, IDP-agnostique),
  dial `pod:6901` en TLS skip-verify (intra-cluster, netpol-gardé),
  injection côté serveur de `Authorization: Basic` (secret `VNC_PW` par
  workspace, jamais exposé au navigateur).
- Frontend : le client web de KasmVNC rendu en **iframe** (same-origin
  via le proxy wwt).

Écartés : (B) réinjecter TigerVNC dans les images kasmweb (chirurgie
lourde — KasmVNC *est* le serveur X —, maintenance récurrente par
release upstream, obligations GPL si images modifiées redistribuées) ;
oauth2-proxy/ingress par workspace (churn d'objets, RAM par session,
timeouts websocket, couplage à l'implémentation d'ingress) ; creds dans
l'URL (bloqué par les navigateurs).

## Impacts prévus par couche

- **CRDs** : `kasmvnc` ajouté aux enums protocole (template + image) ;
  `homeMountPath` template (défaut `/home/user`, `/home/kasm-user` pour
  Kasm) ; webhook rejette les params guacd sur un protocole kasmvnc ;
  `/dev/shm` via passthrough `workload.volumes` (emptyDir Memory).
- **Opérateur** : Secret `VNC_PW` aléatoire par workspace, créé dans le
  ns du pod (mécanisme credentialsSecretRef).
- **Gouvernance** : entrées `WorkspaceImage` `docker.io/kasmweb/*`
  pinnées par digest (tags rolling upstream → Renovate), `Architectures`
  d'après la réalité Docker Hub. Clipboard non gouverné sur ce chemin en
  v1 (affiché honnêtement) ; mitigation phase 4 = config DLP KasmVNC
  dérivée de la policy (`-http-header`/VNCOPTIONS injectables).
- **Sessions** : fermeture à la coupure du websocket proxifié (wwt reste
  dans le chemin) + SessionSweeper en filet.

## PoC phase 0 — résultats (kasmweb/firefox:1.19.0, local)

| Point | Résultat |
|---|---|
| Web 6901 / TLS / Basic | ✅ `-sslOnly`, realm `Websockify`, auth exigée avant résolution de chemin |
| Iframe-abilité | ✅ aucun `X-Frame-Options` / `frame-ancestors` (COEP/COOP sans effet iframe ; same-origin via wwt de toute façon) |
| Proxy à préfixe | ✅ assets relatifs. ⚠️ le client construit `wss://host/websockify` À LA RACINE → l'iframe passera `?path=kasm/{id}/websockify` (mécanisme noVNC) |
| Handshake WS | ✅ 101 + bannière `RFB 003.008`. **Piège (lu dans websocket.c upstream)** : `parse_handshake` exige `Origin:` + `Sec-WebSocket-Protocol` — sans `Origin`, fallback handler fichiers = 404 trompeur |
| `VNC_PW` / `VNC_RESOLUTION` | ✅ (résolution → `MaxVideoResolution` ; géométrie réelle pilotée par le resize dynamique client) |
| PSA / caps | ✅ uid 1000 non-root, tourne avec `--cap-drop ALL`, sandbox Firefox vivant |
| `/dev/shm` | ✅ 512Mi suffisent (Firefox) |
| Persistance home | ✅ `HOME=/home/kasm-user`, volume survit au recreate, uid 1000 |
| Restart (≈ scale-to-0) | ✅ `KASMVNC_AUTO_RECOVER` — Xvnc + app reviennent seuls |

**Conclusion : GO.** Le proxy wwt doit poser trois choses vers
l'upstream : `Authorization: Basic`, `Origin`, sous-protocole `binary`.

## Plan par phases

| Phase | Contenu | Effort |
|---|---|---|
| 0 — PoC | *fait, voir ci-dessus* | 1 j |
| 1 — Data plane | mode reverse-proxy wwt, branche `ConnectionInfo`, iframe frontend, fermeture session sur coupure WS | 3–5 j |
| 2 — CRDs/opérateur | enum `kasmvnc`, `homeMountPath`, Secret `VNC_PW`, webhooks, smoke `test/smoke` | 2–4 j |
| 3 — Catalogue/gouvernance | entrées gitops kasmweb pinnées digest, `Architectures`, affichage clipboard, docs | 1–2 j |
| 4 — Durcissement (opt.) | DLP kasmvnc.yaml dérivé de la policy, cert-manager à la place du self-signed, registre params kasmvnc | 2–3 j |

## Mise à jour 2026-07-10 — audit de l'API kasmvnc exposée par le proxy

Investigation déclenchée par une relecture croisée avec
`docs/studies/protocol-feature-matrix-2026-07-10.md` : les affirmations
« plateforme uniquement » de ce document ci-dessus (ligne 41, avant
correction) ont été vérifiées contre la doc officielle KasmVNC et deux
issues GitHub tranchées par un mainteneur du projet — deux corrections
en ressortent, plus une découverte non prévue au plan de phases.

**Audio (lecture + micro) : confirmé absent de KasmVNC standalone, à
toute version.** Issue [kasmtech/KasmVNC#31](https://github.com/kasmtech/KasmVNC/issues/31),
réponse d'un mainteneur (`mmcclaskey`, collaborateur) : *"No, KasmVNC
does not support audio at this time. Kasm Server does support audio in
and out"* — Kasm Server désigne le produit commercial (Kasm
Workspaces), pas le serveur standalone qu'utilise WaaS. Aucune mention
d'audio dans les changelogs de release de KasmVNC de 2020 à 1.4.0
(oct. 2025, dernière version au moment de l'audit) : situation
inchangée depuis l'ouverture de l'issue. Rien à corriger dans ce
document — l'hypothèse initiale était juste.

**Upload (navigateur → session) : confirmé absent, à toute version, et
pas prévu.** Issue [kasmtech/KasmVNC#209](https://github.com/kasmtech/KasmVNC/issues/209)
(2024), même mainteneur : *"File uploads/downloads are not part of VNC
and we don't currently plan on modifying KasmVNC to support it
directly."* Aucune API d'upload dans la doc développeur actuelle
(`kasmweb.com/kasmvnc/docs/latest/developer_api.html`).

**Download (session → navigateur) : FAUX dans la version initiale de
ce document.** KasmVNC standalone expose une vraie *Downloads API*
depuis la v0.9.3-beta (2022, « support for downloads over 4GB »),
affinée jusqu'à la 1.4.0 (« fixed bug with the downloads API not
escaping certain characters in returned json »). Le client web
embarque un bouton natif qui liste et télécharge les fichiers du
dossier downloads de la session. `wwt/internal/kasm/kasm.go` proxifie
l'intégralité de l'app KasmVNC sans filtrage de chemin (`proxyTo`,
`kasm.go:167-202`) : ce bouton était **déjà accessible aux utilisateurs
WaaS sans policy** — aucun équivalent du tier `platform` qui bloque
`enable-sftp`/`enable-drive` sur les protocoles guacd
(`operator/pkg/params/params.go:365-371`).

✅ **Vérifié en session live (2026-07-10, Feature 11).** Sur k3d dev,
`GET /kasm/{sid}/api/downloads` avec le cookie de session standard
retournait `200` et le vrai listing JSON des fichiers du dossier
downloads (métadonnées : nom, taille, dates, perms) ; sans cookie →
`401` (wwt exige le token de session, l'auth Basic est injectée côté
serveur). **Corrigé** : `wwt/internal/kasm/kasm.go` bloque désormais
`/api/downloads` et ses sous-chemins (`403`), parité avec le gel
`TierPlatform` de guacd. Vérifié live après correctif : `403`.

**Découverte non prévue : l'API kasmvnc exposée est beaucoup plus
large qu'un simple téléchargement.** La doc développeur
(`developer_api.html`) documente neuf endpoints, tous sous
`/api/…`, tous « require owner credentials » :

| Endpoint | Effet |
|---|---|
| `/api/downloads` | Liste + télécharge les fichiers du dossier downloads de la session |
| `/api/clear_clipboard` | Vide le clipboard KasmVNC ET celui de la session X |
| `/api/get_screenshot` | Capture d'écran de la session courante |
| `/api/create_user`, `/api/update_user`, `/api/remove_user` | Gestion des comptes de la session (permissions read/write/owner) |
| `/api/send_full_frame` | Force le renvoi d'une frame complète à tous les utilisateurs en lecture |
| `/api/get_bottleneck_stats`, `/api/get_frame_stats` | Télémétrie de session |

Le proxy wwt injecte les identifiants Basic (`VNC_PW` / `kasm_user`)
côté serveur pour **toute** requête vers le pod (`kasm.go:187`) — y
compris ces neuf endpoints, puisque rien ne les distingue des requêtes
d'assets ou de la WebSocket `/websockify`.

✅ **Audit live (2026-07-10, Feature 11).** `kasm_user` porte bien le
rôle `owner` : `.kasmpasswd` du conteneur contient
`kasm_user:<hash>:wo` (flags **w**rite + **o**wner). Les neuf endpoints
répondent à travers le proxy avec le cookie de session standard :
`downloads` → `200` + listing réel, `get_screenshot` → `200` + JPEG du
bureau (1024×768), `clear_clipboard`/`send_full_frame`/
`get_bottleneck_stats` → `200`, `get_frame_stats` et
`create_user`/`update_user`/`remove_user` → `400` (atteints, auth
`owner` acceptée, il ne manque que les paramètres — donc pleinement
fonctionnels avec le bon corps). Le navigateur d'un utilisateur WaaS a
donc bien un accès complet à cette API.

**Arbitrage (Feature 11)** : seul `/api/downloads` est bloqué — c'est
le seul à recouper une capacité que WaaS gouverne explicitement ailleurs
(transfert de fichiers, `TierPlatform` sur guacd). Les huit autres
restent proxifiés **par choix documenté** : session mono-utilisateur qui
s'administre elle-même, sans tiers à protéger contre l'utilisateur
lui-même (il a déjà le clavier et l'écran ; `get_screenshot` ne fuite
que son propre bureau vers son propre navigateur ;
`create_user`/`remove_user` ne créent des comptes que sur un pod dont
wwt reste le seul point d'entrée, injectant `kasm_user` quoi qu'il
arrive). Bloquer par réflexe ces huit-là serait de la défense contre
les propres capacités de l'utilisateur sur sa propre session.

**Multi-écran : confirmé standalone**, pas une exclusivité plateforme —
la doc client (`clientside.html`) documente un bouton natif « Display
Manager » pour ajouter/positionner des écrans additionnels, sans
mention de dépendance à Kasm Workspaces. Cohérent avec ce que disait
déjà ce document (ligne 41) ; simplement jamais vérifié en session
live WaaS (même statut ❓ que dans la matrice protocole ×
fonctionnalité, note 11).

**Conséquence — livrée (Feature 11, 2026-07-10).** La reprise élargie
(clipboard *et* périmètre API) est faite :

- **`/api/downloads` bloqué** au proxy wwt (403), voir plus haut.
- **Clipboard réellement gouverné.** L'opérateur dérive les directives
  DLP KasmVNC depuis `WorkspacePolicy.Clipboard` et les fusionne dans
  `kasmvnc.yaml` (`operator/internal/controller/kasm_config.go`) :
  `data_loss_prevention.clipboard.server_to_client.enabled` (copy),
  `…client_to_server.enabled` (paste), et
  `runtime_configuration.allow_client_to_override_kasm_server_settings:
  false` (sans quoi le client peut réactiver le clipboard — la doc
  officielle est explicite là-dessus). La config admin opaque
  (`kasmvncConfig`) est préservée pour tout le reste ; seul le bloc
  clipboard est autoritaire. Refuser le clipboard désactive vraiment le
  copier/coller dans le conteneur (vérifié live : policy deny →
  `enabled: false` des deux côtés). L'enforcement suit le **propriétaire**
  du workspace (la DLP conteneur est une-par-workload ; sur les
  namespaces kasmvnc personnels `waas-{user}`, owner == utilisateur).
- **Garde-fou côté template.** Comme l'opérateur écrase ces trois clés
  depuis la policy, un admin qui les écrirait à la main dans
  `kasmvncConfig` serait silencieusement ignoré. Le webhook de validation
  (`workspacetemplate_webhook.go`) refuse donc un template dont le
  `kasmvncConfig` fixe l'une des clés managées, en pointant vers
  `WorkspacePolicy.Clipboard` (même principe « honest refusal beats a
  silently ignored field » que le reste du webhook). Les sous-clés non
  managées (`size`, `allow_mimetypes`, `delay_between_operations`,
  `primary_clipboard_enabled`) restent celles de l'admin. Vérifié live.
- **UI fidèle.** `SessionOverlay.tsx` lit `capabilities.clipboardCopy/
  Paste` (état appliqué, read-only) au lieu d'un texte statique, avec la
  mention « presse-papiers KasmVNC natif, appliqué dans le conteneur ».

Prompt d'origine : `docs/studies/08-prompt-feature11-kasmvnc-governance-gap.md`
(remplace la Feature 4 périmée).

## Mise à jour 2026-07-10 (suite) — défaut KasmVNC et éditeur admin

Deux écarts laissés par la Feature 11, corrigés ensuite.

**Hiérarchie de config KasmVNC — vérifiée en session live.** KasmVNC
fusionne lui-même trois fichiers, du moins prioritaire au plus
prioritaire, **clé par clé** (deep-merge) :

1. `/usr/share/kasmvnc/kasmvnc_defaults.yaml` — les défauts complets
   livrés par l'image kasmweb/\* (résolution 1024×768, encodage, SSL,
   headers CORS, brute-force, bloc `data_loss_prevention.clipboard`
   par défaut…). Doc officielle des directives :
   <https://kasmweb.com/kasmvnc/docs/latest/configuration.html>.
2. `/etc/kasmvnc/kasmvnc.yaml` — override système (petit ; chemins des
   pem SSL, `runtime_configuration`).
3. `~/.vnc/kasmvnc.yaml` — override utilisateur, **c'est là que WaaS
   monte sa ConfigMap** (read-only, subPath).

Preuve live : le fichier user monté ne contenait que
`data_loss_prevention` + `runtime_configuration`, mais le processus
`Xvnc` réel portait `-geometry 1024x768`, `-MaxVideoResolution
1920x1080`, `-BlacklistThreshold 5`, `-http-header
Cross-Origin-Embedder-Policy=require-corp` — toutes issues de
`kasmvnc_defaults.yaml`, absentes des deux autres fichiers. **Donc les
défauts de l'image s'appliquent déjà** même quand le fichier WaaS est
partiel : WaaS n'a pas à re-fournir une couche de défaut.

**Conséquence** : on N'introduit PAS de constante de défaut figée côté
WaaS. Copier `kasmvnc_defaults.yaml` dans le code gèlerait ces défauts
contre toute future version d'image (sur toutes les images à la fois) —
exactement l'anti-pattern qu'on évite pour les défauts CRD. La fusion
WaaS reste donc à **deux couches** (config admin → clés clipboard
policy), et le « socle » effectif est le fichier de l'image, pas le
nôtre. Le commentaire du champ CRD (`workspacetemplate_types.go`) et le
commentaire de package (`kasm_config.go`) ont été corrigés en ce sens
(l'ancien « Empty = no mount, the image default applies » était faux sur
le *mount*, désormais inconditionnel pour un template kasmvnc).

**Éditeur admin.** `kasmvncConfig` a maintenant un `<textarea>` dans
`TemplatesPage.tsx` (`admin/`), affiché seulement quand un protocole
`kasmvnc` est présent (même garde que le webhook), avec un texte d'aide
qui explique la propagation (fusion sur les défauts image, montage
read-only, clés clipboard refusées ici) et un lien vers la doc officielle
ci-dessus. Le plumbing backend (`TemplateInput` → `WorkspaceTemplate` →
`types.gen.ts`) était déjà complet ; seuls le round-trip `toInput` et les
types facade frontend manquaient. Pas d'aperçu de la fusion finale dans
l'UI (le textarea édite la couche de surcharge brute) — non demandé,
éviterait de dupliquer la fusion côté client.
