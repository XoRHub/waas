# Diagnostic — VNC/RDP « connection closed » : netpol des namespaces placés

**Symptôme.** Toute session vers un workspace in-cluster **placé** (namespace
dédié) tombe immédiatement en « connection closed ». guacd logge
`Unable to connect to VNC server` (VNC) ou `RDP server closed/refused
connection: Server refused connection (wrong security type?)` (RDP —
message générique de freerdp sur un simple ECONNREFUSED, ne pas se laisser
distraire par « security type »). Les workspaces **non placés**
(namespace des CRs) fonctionnent, ce qui mimait un bug par-protocole.

**Chaîne de diagnostic** (reproductible telle quelle) :

1. `Status.Address` du workspace vs Service réel : cohérents (même
   helpers `computeName`/`computeNamespace` des deux côtés) ;
2. endpoints du Service : sains, et le kubelet (probes) se connecte ;
3. depuis le pod guacd : `nc -z <svc>.<ns>.svc.cluster.local 5901` →
   **connection refused** sur tous les ports ;
4. depuis un pod du namespace des CRs : **succès** — le discriminant.
   Sous k3s, le contrôleur NetworkPolicy (kube-router) rejette en REJECT,
   d'où « refused » et non un timeout ;
5. la netpol `waas-default-ingress` du namespace placé n'admettait que le
   namespace des CRs (`waas-workspaces`) — pas `waas`, où tournent
   guacd/wwt.

**Cause racine.** L'operator tournait avec `PlatformNamespace` vide : le
Deployment live prédatait le commit 0fa8a9d (qui ajoutait l'env
`WAAS_PLATFORM_NAMESPACE` au chart), car la release Helm n'avait jamais
été upgradée — `make dev-reload` rechargeait les images mais ne re-rendait
pas le chart. Le bootstrap étant create-only, chaque namespace créé
pendant cette fenêtre gardait sa netpol fausse pour toujours.

**Correctifs** (l'un sans les autres ne suffit pas) :

- l'operator **retombe sur son propre namespace** (montage serviceaccount)
  quand l'env manque — plus de mode silencieusement cassé ;
- la netpol `waas-default-ingress` est **désired-state** : re-synchronisée
  à chaque reconcile tant qu'elle porte le label managed-by (une netpol
  reprise par un admin — label retiré — n'est jamais réécrite). Les
  namespaces cassés existants guérissent seuls (event
  `IngressPolicyHealed`) ;
- `make dev-reload` inclut désormais `dev-deploy` (helm upgrade) ;
- gate de livraison : `make smoke` établit une vraie session par
  protocole (`docs/smoke-connections.md`) — cette classe de régression ne
  peut plus passer une itération.
