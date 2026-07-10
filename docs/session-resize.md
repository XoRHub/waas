# Resize dynamique des sessions VNC/RDP — mécanisme WaaS, pas guacd

Le redimensionnement en cours de session d'un bureau in-cluster **ne
passe pas** par le resize natif de Guacamole. Ne cherchez pas de
`sendSize()` dans le tunnel guac ni d'effet du paramètre RDP
`resize-method` : les deux sont des impasses dans cette architecture.

## Pourquoi le chemin natif est mort

- **VNC** : le client VNC de guacd n'émet jamais de resize en cours de
  session (pas de `size` client→serveur sur ce protocole).
- **RDP** : `resize-method=display-update` parlerait au serveur RDP —
  mais notre serveur RDP est le pont xrdp-libvnc, qui ne peut pas
  répercuter un resize sur le Xvnc sous-jacent
  (`waas-images/.../waas-resize`, commentaire d'en-tête).
- **TigerVNC**, lui, supporte RandR `SetDesktopSize` : la résolution EST
  changeable en live, mais uniquement *depuis l'intérieur du pod* —
  c'est ce que fait le script `waas-resize WIDTHxHEIGHT` (xrandr).

## Le mécanisme réel

```
navigateur (ResizeObserver, débouncé ~500 ms)
  → POST /api/v1/workspaces/{id}/resize {width, height}   (api-server)
    → exec `waas-resize WxH` dans le pod (client-go SPDY, argv fixe)
      → xrandr / RandR SetDesktopSize sur Xvnc
        → guacd voit le framebuffer changer et suit naturellement
```

- Frontend : `frontend/src/lib/sessionResize.ts` (débounce + gating) —
  seuls les sessions **in-cluster vnc/rdp** appellent l'endpoint.
  kasmvnc se redimensionne nativement dans son propre client
  (`resize=remote`), ssh n'a pas de bureau, les remote workspaces n'ont
  pas de pod (400 explicite côté serveur).
- api-server : `internal/service/workspace_resize.go`. Autorisation =
  `fetchByID` (propriétaire ou admin), workspace `Running` exigé (409
  sinon), bornes 100–7680 validées avant toute résolution, pod résolu
  par le label `waas.xorhub.io/workspace` dans le namespace de
  placement. Commande **fixe** (`waas-resize WxH`), jamais de shell ;
  `waas-resize` re-valide son argument dans le pod.
- RBAC : `pods/exec` (verbe `create`) est une entrée dédiée du
  ClusterRole api-server (`helm/waas/templates/api-server.yaml`) —
  volontairement séparée du `get/list` lecture seule pour rester
  visible en review.
- Audit : chaque resize effectif écrit `workspace.resized` (nom + mode).

## Pourquoi le PR #469 de guacamole-server n'est pas pertinent ici

Le PR #469 (guacd 1.6) ajoute la négociation native guacd↔serveur VNC
d'un resize serveur-initié. Notre guacd est déjà en 1.6
(`helm/waas/values.yaml`), mais ce chemin n'est **ni utilisé ni
nécessaire** : le resize WaaS passe par pod-exec (schéma ci-dessus),
qui fonctionne identiquement pour VNC et RDP puisque les deux tournent
sur le même Xvnc dans les images WaaS (RDP = pont xrdp vers ce Xvnc).
Il n'y a donc rien à « activer » côté guacd pour amener VNC au niveau
de RDP — c'est déjà symétrique, indépendamment de la version de guacd.
Implémenter le natif #469 *en plus* (latence moindre qu'un exec, ou
scénarios sans exec possible) serait un chantier séparé, non entamé.

## Sort de `resize-method` (arbitrage 2026-07-10 : conservé)

`resize-method` (registre RDP, tier ui) reste inerte pour les bureaux
in-cluster : le mécanisme pod-exec le contourne complètement. Il est
**conservé** parce que pour les *remote workspaces* RDP, guacd parle à
un vrai serveur RDP externe et ce paramètre pilote alors la
négociation native de guacd. Sa description dans le registre
(`operator/pkg/params/params.go`) dit désormais explicitement cette
frontière.

## Versions guacd / guacamole-common-js

guacd est en 1.6.0 ; le frontend reste sur `guacamole-common-js@^1.5.0`
**volontairement** : Apache ne publie pas cette lib sur npm — le paquet
`guacamole-common-js` comme `@glokon/guacamole-common-js` (seul miroir
en 1.6.x) sont des miroirs tiers, et on refuse d'introduire une source
non-Apache dans la chaîne d'approvisionnement du frontend. L'API
consommée (`DesktopPane.tsx` : Tunnel/Client/Mouse/Keyboard/Streams)
est stable entre 1.5 et 1.6 et guacd 1.6 reste compatible avec les
clients 1.5. Si l'alignement devient nécessaire, la voie acceptable est
de vendorer le build officiel Apache (Maven Central
`org.apache.guacamole:guacamole-common-js`).
