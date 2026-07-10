# Prompt Fable 5 — Feature 1 : reconfiguration runtime d'un workspace + indicateur de changement en attente

Colle ce document tel quel comme prompt d'implémentation. Il part du principe que tu (Fable 5) n'as aucun contexte de conversation préalable — tout ce qui est nécessaire est ci-dessous ou dans les fichiers référencés.

## Contexte du repo

WaaS est une plateforme K8s-native de "workspace as a service" (bureaux distants VNC/RDP/SSH/KasmVNC provisionnés via un opérateur Go controller-runtime). Composants : `operator/` (CRDs `Workspace`/`WorkspaceTemplate`/`WorkspacePolicy`/`WorkspaceImage` + reconcilers + webhooks + la bibliothèque de gouvernance partagée `operator/pkg/policy`), `api-server/` (backend REST chi, consomme `operator/api/v1alpha1` + `operator/pkg/*`), `frontend/` (React 19 + TS strict + react-query), `wwt/` (proxy websocket).

Lis d'abord `docs/adr/0001-template-boundary-convergence.md` — c'est la doctrine centrale sur laquelle cette feature s'appuie : un `WorkspaceTemplate` édité (ou les overrides d'un `Workspace`) ne convergent vers le workload vivant (Deployment/StatefulSet/Pod) qu'aux frontières de scale-up (pause→reprise, arrêt planifié→reprise). En session, la dérive est **signalée, jamais appliquée**. Ce mécanisme existe déjà et fonctionne pour les deux sources de dérive (template ET overrides du workspace), car `buildPodTemplate` (`operator/internal/controller/workload.go:36+`) consomme les deux pour construire le fingerprint de dérive (`podTemplateFingerprint`, comparé dans `ensureDeployment`/`ensureStatefulSet`/`ensurePod`).

## Ce qui existe déjà (à connaître avant de coder)

**Gouvernance des overrides (déjà complète, ne pas dupliquer) :**
- `operator/api/v1alpha1/workspace_types.go` : `WorkspaceOverrides` porte déjà `Env`, `SecurityContext`, `PodSecurityContext`, `Volumes`, `VolumeMounts`, `NodeSelector`, `Tolerations`, `Protocol`, `Schedule`, `Labels`, `Annotations`. `WorkspaceSpec.Resources *corev1.ResourceRequirements` est un champ séparé (non-nil = override, présence testée jamais valeurs — voir commentaire `specClaims` dans `operator/pkg/policy/overrides.go`).
- `operator/pkg/policy/overrides.go` est LE registre unique reliant chaque champ JSON à un `OverridableField` (`FieldEnv`, `FieldNodeSelector`, `FieldTolerations`, `FieldResources`, etc.) et `CheckOverrides` calcule l'usage par réflexion — **zéro code de gouvernance à écrire côté toi**, le webhook d'admission (`operator/internal/webhook`) revalide automatiquement toute mise à jour du CR `Workspace`, y compris une mise à jour faite après création.
- Le front calcule déjà côté `CreateWorkspaceDialog.tsx:174-177` le pattern de gating :
  ```ts
  const policyAllows = (field) => !q?.allowedOverrides || q.allowedOverrides.includes(field);
  const canOverride = (field) => isAdmin || ((template?.allowedOverrides?.includes(field) ?? false) && policyAllows(field));
  ```
  `q` vient de `useQuota()` (policy-level `allowedOverrides`), `template` de `useTemplates()` (template-level `allowedOverrides`). **Réutilise ce pattern tel quel** pour griser les champs non autorisés — c'est exactement le mécanisme demandé ("uniquement si le WorkspaceTemplate l'y autorise, auquel cas les options devront être grisées").
- `Workspace.templateRef` existe déjà dans `frontend/src/types.gen.ts:190` — un `ConnectionSettingsDialog` peut donc résoudre le template via `useTemplates().data?.data.find(t => t.name === workspace.templateRef)` exactement comme le fait la création.

**UI existante à réutiliser (ne pas réinventer) :**
- Éditeur de resources (sliders CPU/mémoire bornés) : `CreateWorkspaceDialog.tsx:329-443`.
- Éditeur d'env (liste de lignes name/value, add/remove) : `CreateWorkspaceDialog.tsx:444-500`.
- Il n'existe **aucune UI aujourd'hui** pour `nodeSelector`/`tolerations` (nulle part dans le repo — seulement éditable en YAML brut dans l'éditeur de template admin, `TemplatesPage.tsx`). C'est le seul morceau réellement nouveau visuellement : un éditeur clé/valeur pour `nodeSelector` (même UX que les lignes env) et un petit éditeur de liste pour `tolerations` (`{key, operator, value, effect, tolerationSeconds}` par ligne — cf. `corev1.Toleration`).
- `ConnectionSettingsDialog.tsx` actuel : un seul niveau d'onglets, un par protocole (`ProtocolTabs`/`ProtocolParamsForm`, `frontend/src/components/ProtocolTabs.tsx`).

**Le badge de dérive existe déjà, partiellement :**
- Backend : `model.Workspace.TemplateDrifted bool` (`api-server/internal/model/model.go:199-203`), alimenté par le `drifted` retourné par `ensureWorkload`/`ensureDeployment` etc.
- Frontend : badge ambre cliquable-non-actionnable dans `SessionCard.tsx:90-104`, avec tooltip statique (`portal.drift.badge`/`portal.drift.full` dans `frontend/src/i18n/locales/{en,fr}.json:106-109`). Le texte actuel ("Le modèle de ce workspace a été mis à jour...") ne couvre QUE le cas template — à généraliser puisque la Feature ci-dessous permettra aussi de faire dériver un workspace via ses propres overrides.
- Pas de déclencheur manuel aujourd'hui : la seule façon de forcer la convergence est de suspendre puis reprendre le workspace via `useWorkspaceAction()` (`POST /api/v1/workspaces/{id}/pause` puis `.../resume`, `frontend/src/hooks/useApi.ts:170-177`), qui stamp `AnnotationManualStateAt` — pertinent pour la règle B du scheduler (`docs/workspace-lifecycle.md`), mais un enchaînement pause+resume manuel depuis le front n'est pas un "reload" propre : il modifie l'intention de pause persistante et l'annotation qui pilote la résolution de conflit avec le planning cron, alors que l'utilisateur veut juste forcer une convergence ponctuelle sans toucher à son planning.

## Ce qu'il faut livrer

### A. Onglet "Workspace" dans Connection Settings — reconfiguration runtime

Le menu "connection settings" d'un workspace (`ConnectionSettingsDialog.tsx`) doit gagner un niveau d'onglets au-dessus des onglets protocole actuels : **"Connexion"** (ce qui existe déjà — les onglets VNC/RDP/SSH) et **"Workspace"** (nouveau).

L'onglet "Workspace" permet de modifier, sur un workspace déjà instancié :
- les variables d'environnement (`overrides.env`),
- le placement (`overrides.nodeSelector` + `overrides.tolerations` — c'est le "nodePlacement" demandé),
- les ressources (`spec.resources`, CPU/mémoire).

Chaque groupe de champs est actionnable seulement si `canOverride(field)` est vrai pour le champ concerné (`FieldEnv`, `FieldNodeSelector`, `FieldTolerations`, `FieldResources` — mappe direct sur le registre `operator/pkg/policy/overrides.go`) ; sinon grisé, avec une explication (le pattern `portal.fixedSizing` existe déjà côté `PortalPage.tsx` pour le cas "resources non autorisé à la création" — inspire-t'en pour le libellé "non autorisé par ce modèle/cette politique").

**Backend — nouvel endpoint à créer** (rien de tel n'existe aujourd'hui, seuls `Create`/`Get`/`Delete`/`Pause`/`Resume`/`Connect`/volumes existent dans `WorkspaceHandler`) :
- `PATCH /api/v1/workspaces/{id}/overrides`, corps `{ env?, nodeSelector?, tolerations?, resources? }`.
- Nouvelle méthode `WorkspaceService.UpdateOverrides(ctx, actor, id, in)` : `fetchByID` (vérifie déjà la propriété), applique les champs fournis sur `ws.Spec.Overrides`/`ws.Spec.Resources` (remplacement complet du champ fourni, pas de fusion partielle — cohérent avec la sémantique "présence = override" déjà en place), `s.kube.Update(ctx, ws)`. Le webhook refait la vérification `CheckOverrides` automatiquement (comme pour `SetPaused`) : réutilise le helper `policyDenial(err)` déjà utilisé dans `SetPaused` (`workspace_service.go:352-378`) pour retourner un 403 `[ReasonCode]` propre côté API si le champ n'est en fait pas autorisé (défense en profondeur, le front ne doit pas être la seule ligne).
- Audit : suit le même principe que l'audit `workspace.overrides_applied` évoqué pour la création (noms de champs modifiés seulement, **jamais les valeurs d'env** — contrainte existante à respecter).

Aucune modification de `operator/pkg/policy` ni du webhook n'est nécessaire : c'est le même CR, la même voie d'admission Update que `Pause`/`Resume` empruntent déjà.

### B. Icône de changement en attente + reload manuel, à côté du statut "running"

Sur la card d'un workspace (`SessionCard.tsx`), quand une dérive est en attente (`target.templateDrifted`, que la cause soit un template édité **ou** un changement fait via la Feature A ci-dessus), l'icône doit :
1. Être visible à côté du badge de statut (c'est déjà le cas — étendre le badge existant, ne pas en créer un second).
2. Afficher au survol un tooltip **à puces** expliquant : ce qui va changer et pourquoi (généraliser le texte actuel qui ne parle que du template), et que ça s'applique automatiquement à la prochaine bascule scale-down/scale-up (pause/reprise ou arrêt planifié — texte déjà correct, à garder).
3. Être cliquable pour déclencher un **reload manuel immédiat** (scale-down puis scale-up forcé), avec confirmation ("le bureau va redémarrer, le travail non sauvegardé sera perdu").

**Backend — mécanisme suggéré** (à toi d'ajuster si tu trouves plus propre, mais reste dans l'idiome déjà établi par le repo : des annotations comme signal d'action one-shot consommé par le reconciler — voir `AnnotationManualStateAt`, `waas.xorhub.io/delete-home`, le label `waas.xorhub.io/cleanup`) :
- `POST /api/v1/workspaces/{id}/reload` (nouvelle route/handler/service method, à côté de `Pause`/`Resume`). **Ne touche pas `spec.paused` ni `AnnotationManualStateAt`** — un reload ne doit pas interférer avec la résolution de conflit du scheduler (règle B, `docs/workspace-lifecycle.md`) ni avec l'intention de pause de l'utilisateur.
- Stamp une annotation dédiée (ex. `waas.xorhub.io/reload-requested-at=<RFC3339>`) sur le CR.
- Dans le reconciler (`operator/internal/controller/workload.go`), quand cette annotation est plus récente que la dernière application connue et que le workload tourne (`!paused`), force une frontière de convergence ponctuelle : scale à 0 puis 1 (Deployment/StatefulSet) ou recréation (Pod) — réutilise le chemin déjà emprunté par la branche `wasDown || want == 0` de `ensureDeployment` (`workload.go:~240`), puis nettoie l'annotation une fois appliqué. Émets un Event K8s (`WorkloadReloaded`) en cohérence avec les autres transitions déjà instrumentées (`Provisioning`/`Ready`/`Paused`/`Stopped`/`TemplateDrifted`).
- Ajoute un test dans le style de `operator/internal/controller/kasm_config_test.go` (`TestKasmConfigBoundaryConvergence`) pour ce nouveau chemin.

**Frontend :**
- Nouveau hook `useReloadWorkspace()` (même forme que `useWorkspaceAction`, `frontend/src/hooks/useApi.ts:170-177`), `POST /api/v1/workspaces/{id}/reload`.
- Le "reload" est une capacité workspace-only (les workspaces distants n'ont pas de dérive de template) : ajoute-le au modèle `SessionTarget`/`capabilities` (`frontend/src/lib/target.ts`) suivant la règle documentée dans `docs/frontend-capabilities.md` ("How this shapes future features" — matrice de validation à mettre à jour), plutôt que de le brancher en dur dans `SessionCard`.
- Mets à jour `portal.drift.badge`/`portal.drift.full` (en/fr) pour couvrir les deux causes de dérive, et ajoute les clés du nouveau flux (confirmation, succès, erreur).

## Contraintes à respecter

- Le repo n'a **aucun TODO/FIXME** et le préfère ainsi (audit `docs/studies/audit-2026-07.md`) — livre complet ou pas du tout, pas de stub.
- Tests obligatoires : Go (`UpdateOverrides` service + handler + le nouveau chemin reconciler) et frontend (vitest — le hook, le gating `canOverride`, le badge). Le repo mesure sa couverture par zone ; ne baisse pas la barre existante.
- `gofmt` propre, `tsc -b` sans erreur (`strict: true`, zéro `any`), pas de nouveau `console.log`.
- Mets à jour la documentation existante plutôt que d'en créer une nouvelle isolée : `docs/adr/0001-template-boundary-convergence.md` (note additive sur le reload manuel), `docs/workspace-lifecycle.md`, `docs/frontend-capabilities.md`.
- i18n : toute chaîne visible passe par `frontend/src/i18n/locales/{en,fr}.json`, jamais de texte en dur dans les composants.

## Points ouverts (ton arbitrage)

- Faut-il distinguer dans le tooltip "c'est le template qui a changé" vs "ce sont vos propres réglages qui ont changé" ? Le signal backend actuel est un seul booléen (`TemplateDrifted`) qui ne fait pas cette distinction. Un texte générique ("la configuration de ce workspace a changé") couvre le besoin fonctionnel sans backend supplémentaire ; distinguer les deux est un nice-to-have, pas un blocant.
- Forme exacte du mini-éditeur de `tolerations` (une ligne par tolération avec les 4-5 champs, ou un textarea JSON minimal comme fallback) : les deux sont défendables, choisis en cohérence avec le reste de la page.
