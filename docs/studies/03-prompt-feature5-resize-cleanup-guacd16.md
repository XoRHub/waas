# Prompt Fable 5 — Feature 5 : nettoyage resize-method (RDP) + alignement guacd 1.6 / guacamole-common-js

Colle ce document tel quel comme prompt d'implémentation. Il part du principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Contexte du repo — et une prémisse à corriger avant de coder

La demande initiale de cette feature était "active le resize natif guacd 1.6 pour VNC comme pour RDP (Apache guacamole-server PR #469)". **Cette prémisse est fausse dans ce repo — vérifié avant d'écrire ce prompt.** Le mécanisme de resize de session, livré par le commit `906017f3272e feat(resize): real end-to-end VNC/RDP session resize via pod exec`, **ne passe pas du tout par le resize natif de guacd**. Il fonctionne déjà de façon strictement identique pour VNC et RDP, et ne dépend donc en rien de la version de guacd ni du PR #469.

## Ce qui existe déjà (à connaître avant de coder)

**Le mécanisme réel** : `api-server/internal/service/workspace_resize.go:52-88` (`Resize`, handler `api-server/internal/handler/workspace_handler.go:209`) exécute `waas-resize WxH` **dans le pod du workspace via `client-go`/SPDY exec** (`api-server/internal/k8s/exec.go`) — pas un message guacd. Côté frontend, `frontend/src/lib/sessionResize.ts:16-52` observe le conteneur via `ResizeObserver` (débounce, dédup) et appelle cet endpoint ; `DesktopPane.tsx:297-311` le branche sur le cycle de vie du composant.

**VNC est déjà au même niveau que RDP** : `sessionResize.ts:27` — `const active = kind === 'workspace' && (protocol === 'vnc' || protocol === 'rdp');`. Il n'y a **aucune restriction VNC-spécifique** dans le frontend, aucun toggle qui désactive le resize pour VNC — c'est déjà générique. Seuls `kasmvnc` et les workspaces distants (`kind === 'remote'`) en sont exclus (`DesktopPane.tsx:151-167` pour kasmvnc ; 400 explicite dans `workspace_resize.go:60-67` pour remote).

**Pourquoi ça marche déjà pour VNC sans le natif guacd** : `waas-images/base/ubuntu/rootfs/usr/local/bin/waas-resize:4-9` documente la vraie raison — "Guacamole's VNC client does not push browser-window resizes to the server... TigerVNC does support RandR SetDesktopSize... from inside the session... or by anything that can exec into the pod." VNC et RDP tournent tous les deux sur le même Xvnc sous-jacent dans les images WaaS (RDP = xrdp qui pont vers ce même Xvnc), donc le `xrandr`/RandR exécuté via pod-exec fonctionne identiquement des deux côtés — **indépendamment de guacd et de sa version**. Le PR #469 de guacamole-server (négociation native guacd↔serveur VNC compatible pour un resize serveur-initié) est un chemin totalement différent, non implémenté ici, et non nécessaire au fonctionnement actuel.

**Le seul reliquat concerné par guacd** : `resize-method` (`operator/pkg/params/params.go:145-148`, `Enum: ["display-update","reconnect"]`, RDP uniquement, `TierUI`). Le message du commit 906017f3272e le signale déjà comme "effectively dead" — VNC n'a d'ailleurs aucun paramètre `resize-method` dans le registre (aucune entrée n'existe pour ce protocole).

**Versions guacd/guacamole-common-js** : `helm/waas/values.yaml:145` → `guacamole/guacd:1.6.0` (déjà 1.6, déjà au-dessus du PR #469). `frontend/package.json:18` → `"guacamole-common-js": "^1.5.0"` — **en retard** par rapport à guacd. C'est l'écart réel identifié par l'audit (`docs/studies/audit-2026-07.md` §6 dette technique).

**Tests existants** : `api-server/internal/service/workspace_resize_test.go` (auth, bounds, phase, remote-rejection, exec failure), `frontend/src/lib/sessionResize.test.ts` (debounce, gating incl. whitelist protocole, dedup, cancel).

## Ce qu'il faut livrer

Cette feature est un **nettoyage de dette + une mise à jour de dépendance**, pas une nouvelle fonctionnalité de resize :

### A. Trancher le sort de `resize-method`

Deux options défendables, choisis-en une et documente :
1. **Le supprimer** du registre (`operator/pkg/params/params.go:145-148`) puisqu'il n'a plus aucun effet réel sur le mécanisme de resize actuel (pod-exec le rend inutile), avec migration : vérifier qu'aucun template/CR en prod/dev ne le référence dans `Params`/`UserParams` avant suppression (grep `resize-method` dans `hack/dev/templates-dev.yaml`, `gitops/`).
2. **Le garder** mais reformuler sa `Description` pour dire explicitement qu'il ne pilote plus le resize (WaaS gère le resize par exec, ce paramètre ne fait qu'influencer le comportement natif de guacd si jamais un client s'y appuyait hors WaaS) — seulement si tu identifies une raison de le garder (ex. compatibilité avec un client guacd tiers).

### B. Aligner `guacamole-common-js` sur guacd 1.6

Monte `guacamole-common-js` de `^1.5.0` à la dernière version compatible 1.6.x dans `frontend/package.json:18`. Vérifie le changelog de la lib pour toute breaking change d'API affectant `DesktopPane.tsx`/le rendu du canvas, et fais tourner la suite de tests frontend + un test manuel de connexion (via `make smoke` sur l'env k3d dev) après la montée de version.

### C. Documenter l'architecture de resize (nouveau ou existant)

Si `docs/session-resize.md` existe déjà (le doc-comment de `Resize()` y renvoie, `workspace_resize.go:42-47`), vérifie qu'il explique clairement : (1) le mécanisme est pod-exec, pas guacd natif ; (2) c'est pourquoi VNC et RDP sont déjà symétriques ; (3) pourquoi le PR #469 de guacamole-server n'est pas pertinent pour ce mécanisme. Si ce doc n'existe pas ou n'aborde pas ces points, complète-le — c'est la confusion à l'origine de cette feature, éviter qu'elle se reproduise.

## Contraintes à respecter

- Zéro changement de comportement utilisateur attendu : le resize VNC/RDP fonctionne déjà, cette feature ne doit rien casser sur ce chemin (fais tourner `workspace_resize_test.go` et `sessionResize.test.ts` sans régression).
- Si tu supprimes `resize-method` (option A.1), mets à jour `docs/guacd-parameters.md` (généré par `make docs-params`, ne l'édite jamais à la main) en relançant la génération.
- `go test ./...` sur `operator`/`api-server`, `tsc -b` + tests vitest sur le frontend.

## Points ouverts (ton arbitrage)

- Suppression vs reformulation de `resize-method` (§A) — vérifie d'abord s'il existe un usage réel avant de trancher pour la suppression.
- Si, après cette investigation, tu identifies un vrai bénéfice produit à implémenter le resize natif guacd↔VNC (PR #469) **en plus** du mécanisme pod-exec actuel (par exemple : réduire la latence par rapport à un exec, ou supporter un scénario où l'exec n'est pas possible) — ce serait un chantier **séparé et nettement plus lourd** (négociation côté guacd + support côté serveur VNC/TigerVNC), hors du périmètre "nettoyage" de cette feature. Ne l'entreprends pas ici ; note-le comme piste future si tu le juges pertinent.
