# Rapport : HTTPS sur l'env de dev + vérification clipboard en navigateur réel

Livrable du prompt `13-prompt-fix-clipboard-host-workspace-guacd.md`
(le HTTPS sert aussi à vérifier le fix kasmvnc `afc081da` du prompt 14).
Vérifié le 2026-07-10 sur le cluster k3d `waas-dev`.

## Ce qui a été livré

- `hack/dev/k3d-config.yaml` : mapping `8443:443@loadbalancer` (+ note :
  `k3d cluster edit waas-dev --port-add "8443:443@loadbalancer"` pour un
  cluster existant — le fichier n'est lu qu'à la création).
- `hack/dev/values-dev.yaml` : `ingress.tls.enabled: true` avec
  `issuerRef: {kind: Issuer, name: waas-selfsigned}` — réutilise l'Issuer
  self-signed que le chart crée déjà pour le webhook de l'opérateur
  (`operator.webhook.enabled` est actif par défaut et nécessaire en dev).
  Aucun changement côté chart : `helm/waas/templates/ingress.yaml`
  supportait déjà tout (annotation cert-manager + section `tls`).
- `Makefile` `dev-url` : affiche les deux URLs (https = clipboard
  seamless, http = smoke tests).
- Commentaires corrigés (`DesktopPane.tsx`, `lib/clipboard.ts`) et
  `docs/clipboard.md` mis à jour : l'event DOM `paste` n'est PAS un filet
  universel (voir diagnostic ci-dessous).

Chemin prod inchangé : `values.yaml` et les templates ne bougent pas.

## Infra vérifiée

- `Certificate waas-public-tls` → `Ready: True`, secret `kubernetes.io/tls`
  créé par l'ingress-shim, SAN `DNS:waas.127.0.0.1.nip.io`, 90 jours.
- Traefik (bundled k3s) termine le TLS sur `websecure` sans rien installer.
- `curl` : 200 sur `http://…:8080/` ET `https://…:8443/` — **aucune
  redirection HTTP→HTTPS** (la section `tls` d'un Ingress Traefik
  n'éteint pas le routeur HTTP) ; login API OK sur les deux.
- `make smoke` (HTTP, sessions réelles par protocole) : vert après coup.

## Protocole de vérification navigateur (Chromium réel, Playwright)

Non simulable en jsdom : modèle de permission navigateur + vraie session
guacd. Outillage : Chromium piloté par Playwright
(`ignoreHTTPSErrors: true` = l'équivalent programmatique de
l'interstitiel de certificat accepté ; permissions `clipboard-read`/
`clipboard-write` accordées à l'origine = l'état « prompt accepté »).
Côté workspace, le presse-papiers X (sélection CLIPBOARD) est lu/écrit
par `kubectl exec` : image `ubuntu-xfce` via deux scripts python-xlib
copiés dans le pod (l'image n'a ni xclip ni xsel), image kasm via son
`xclip` embarqué. TigerVNC (`vncconfig -nowin`) synchronise nativement
sélections X ↔ cut-text VNC.

Workspaces : `ubuntu-xfce` (vnc/guacd) et `kasm-terminal` (kasmvnc),
créés par l'API puis supprimés après la vérification.

## Résultats sur `https://waas.127.0.0.1.nip.io:8443`

| Check | Résultat |
|---|---|
| `window.isSecureContext`, `navigator.clipboard`, `readText` | ✅ présents |
| guacd host→remote : `writeText` + event `focus` → CLIPBOARD X du pod | ✅ texte identique |
| guacd remote→host : CLIPBOARD X posé dans le pod → `readText` navigateur | ✅ texte identique |
| kasmvnc host→remote (iframe, params `clipboard_*` du fix `afc081da`) | ✅ |
| kasmvnc remote→host | ✅ |
| Contrôle `http://…:8080` : `isSecureContext` | ❌ `false`, pas de `navigator.clipboard` (attendu) |

Le `wss://` du tunnel passe dès que l'exception d'origine est acceptée.

## Diagnostic § 3 : l'event `paste`, tranché par l'observation

Instrumentation en live (listener `paste` en capture sur le pane +
listener `keydown` en phase bubble) puis Ctrl+V réel dans le pane :

```
{"pasteFired": false, "keydownPrevented": true}
```

Confirmé : `Guacamole.Keyboard` fait `preventDefault()` sur le keydown
relayé (le retour `undefined` de `onkeydown` vaut « bloquer le défaut »),
donc la commande de collage native ne s'exécute jamais et l'event `paste`
ne se produit pas. L'event reste câblé comme filet théorique mais les
commentaires qui le présentaient comme chemin universel (« fonctionne
partout, HTTP compris ») étaient faux — corrigés.

**Arbitrage : correction des commentaires, pas de pass-through Ctrl+V.**
Laisser passer le défaut navigateur sur Ctrl+V ferait courir l'event
`paste` (qui envoie le stream clipboard) APRÈS le keydown déjà relayé au
bureau : l'appli distante collerait le contenu périmé au premier Ctrl+V.
Le host→remote seamless fonctionne via le focus-sync (vérifié ci-dessus),
qui amorce le presse-papiers distant AVANT la frappe ; Firefox et
permission refusée gardent l'échange manuel de l'overlay. Le `readText`
au focus n'a pas non plus eu besoin du geste `mousedown` : permission
accordée = lecture OK hors activation transitoire (l'état après le
premier prompt Chromium accepté).

## Piège rencontré (à retenir)

La première passe kasmvnc échouait : l'image frontend déployée dans k3d
était antérieure à `afc081da` — l'URL de l'iframe n'avait pas les
paramètres `clipboard_up/down/seamless`. Un `docker build` + `k3d image
import` + `rollout restart` du frontend a suffi. Symptôme générique :
un fix frontend « committé mais pas rechargé » est invisible en dev
(même drift que le lockout netpol documenté dans le Makefile).

## Reproduire

```sh
make dev-url                 # les deux URLs
# cluster existant : k3d cluster edit waas-dev --port-add "8443:443@loadbalancer"
# puis make dev-deploy ; cluster neuf : make dev-reset && make dev-bootstrap
kubectl -n waas get certificate waas-public-tls   # Ready: True
curl -sk -o /dev/null -w '%{http_code}\n' https://waas.127.0.0.1.nip.io:8443/
```

Vérification manuelle : ouvrir la variante https en Chromium, accepter
l'avertissement de certificat une fois, se connecter à un workspace vnc ;
copier sur le host → revenir sur l'onglet → Ctrl+V dans une appli du
bureau distant ; copier dans le bureau → coller sur le host.
