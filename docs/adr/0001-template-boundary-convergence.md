# ADR 0001 — Convergence des templates aux frontières de scale-up

**Statut** : accepté (2026-07-08). **Décideur** : propriétaire de la
plateforme, sur arbitrage post-audit (T7).

## Contexte

Historiquement, `ensureDeployment`/`ensureStatefulSet` étaient
**create-only** sur le podTemplate : une édition de `WorkspaceTemplate`
(image, env, resources, mounts, config kasmvnc) n'atteignait jamais les
workspaces existants — dérive invisible, patches d'images non propagés.
L'alternative « converger à chaque édition » (GitOps pur) a un défaut
rédhibitoire pour des bureaux : éditer un template **tuerait la session
de chaque utilisateur** qui tourne dessus.

## Décision

Convergence **aux frontières de scale-up uniquement** :

- Un fingerprint du podTemplate désiré (`waas.xorhub.io/pod-template-hash`,
  sha256 du template construit) est comparé au workload vivant à chaque
  reconcile.
- **Workload à 0 réplique (pause, arrêt planifié) ou en train d'y
  passer** : le podTemplate converge librement — aucune session ne peut
  être tuée. La reprise démarre donc toujours sur la forme à jour.
- **Workload en cours d'exécution** : la dérive est **signalée, jamais
  appliquée** — condition `TemplateDrifted=True` (raison
  `TemplateChanged`), event `TemplateDrifted` émis à la transition,
  colonne `DRIFTED` dans `kubectl get workspace`, badge « mise à jour en
  attente » (tooltip explicatif) à côté du statut sur la card du
  portail.
- Workload de kind `Pod` : convergence par recréation (pause/reprise),
  dérive signalée pareillement.
- Chemin Windows/KubeVirt : détection de dérive hors périmètre
  (spec non structurée, chemin non testé e2e).

**Changement de comportement assumé** : la config kasmvnc
(`spec.kasmvncConfig`) convergeait jusqu'ici en pleine session (rollout
immédiat). Elle rentre dans la doctrine générale : le fichier ConfigMap
converge immédiatement, le POD la prend à sa prochaine frontière — la
cohérence de doctrine prime sur la promesse d'origine de la phase 2
kasm.

## Conséquences

- Les éditions de template se propagent d'elles-mêmes au fil des
  pauses/reprises et des fenêtres d'arrêt planifiées (les workspaces à
  schedule convergent au plus tard à la prochaine fenêtre).
- Un workspace jamais suspendu peut dériver longtemps : c'est visible
  (condition/badge) et l'admin peut forcer via pause/reprise.
- Les tests de la sémantique vivent dans
  `operator/internal/controller/kasm_config_test.go`
  (`TestKasmConfigBoundaryConvergence`).
