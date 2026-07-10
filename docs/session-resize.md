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

## Ce que ça ne règle pas

`resize-method` (registre RDP, tier ui) reste inerte au sens
guacd/RDP : le vrai mécanisme le contourne complètement. Le garder, le
retirer ou le reformuler est un arbitrage produit — voir la note en fin
d'implémentation (étude protocol-feature-matrix, écart #2).
