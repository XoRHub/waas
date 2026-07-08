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
  serveur. **Plateforme uniquement** : audio, upload/download, micro —
  parité avec l'existant WaaS (rien de perdu).
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
