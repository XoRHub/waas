# Placement des workloads — namespaces, naming, labels

Architecture validée : **les CR Workspace (et toute la gouvernance :
templates, policies, catalog, webhooks) restent dans le namespace
plateforme** ; seuls les workloads (Deployment/StatefulSet/Pod, Service,
PVC home, VM) partent dans un namespace cible. Défaut recommandé : **un
namespace par utilisateur** (`waas-{user}`).

## Namespace cible (`spec.targetNamespace`)

- Le template déclare un **pattern** : `spec.placement.namespace`, tokens
  `{user}` (username sanitisé) et `{workspace}` (displayName sanitisé).
  Exemples : `waas-{user}` (défaut recommandé, quota par user naturel),
  `waas-{user}-{workspace}` (isolation maximale par workspace).
- L'api-server **résout le pattern une fois à la création** et écrit la
  valeur explicite dans `workspace.spec.targetNamespace`. Le webhook la
  rend **immuable** (comme `owner`) : déplacer un workspace = recréer.
- Vide = comportement historique (workloads à côté du CR). Aucun
  workspace existant n'est affecté.
- Override à la création : `targetNamespace` explicite dans le payload,
  gated par le champ overridable **`placement`** (template ∩ policy,
  admins exempts).

### Sanitization (operator/pkg/naming — partagé api-server/webhook/operator)

1. décomposition NFKD, marques combinantes supprimées (`é`→`e`) ;
2. lowercase ;
3. toute suite hors `[a-z0-9]` → un seul `-` ;
4. trim des `-` ; résultat vide → `x` ;
5. troncature ≤ 63 (DNS-1123), jamais sur un `-`.

La normalisation est lossy (`Zoé` et `zoe` collident) : les collisions
sont départagées par un **suffixe déterministe** `-xxxxx` (5 hex de
sha256 de la valeur brute), appliqué uniquement quand la collision est
constatée à la création, puis figé dans le spec.

### Enforcement anti-usurpation (webhook, fail-closed)

- Namespaces système/plateforme refusés pour tous (`kube-*`, le
  namespace des CR), bypass compris.
- Pour un non-admin, `targetNamespace` doit soit matcher le préfixe
  `waas-<sanitize(username)>` **recalculé depuis l'identité de confiance**
  (annotations gelées / userInfo — jamais une valeur fournie), soit
  désigner un namespace existant portant `waas.xorhub.io/owner=<owner>`.
- Deuxième ligne : le reconciler re-vérifie via `policy.CheckOverrides`
  avant de créer du compute (workspaces appliqués par GitOps).

### Bootstrap du namespace (operator, create-only)

Créé au premier workload s'il n'existe pas — jamais modifié ensuite (les
réglages admin ne sont pas écrasés) :

- **Labels** : `app.kubernetes.io/managed-by=waas-operator`,
  `waas.xorhub.io/owner=<owner>`, Pod Security
  `enforce=baseline` / `warn=restricted` (les desktops tournent non-root
  mais peuvent exiger baseline ; warn fait remonter les candidats au
  durcissement) ; + labels/annotations du template
  (`placement.namespaceLabels/Annotations`, denylist appliquée, les clés
  plateforme gagnent toujours).
- **ResourceQuota `waas-quota`** dérivée des caps agrégés de la policy du
  propriétaire (`limits.aggregate` → requests/limits cpu/mémoire,
  requests.storage). Défense en profondeur : le webhook reste
  l'enforcement primaire.
- **NetworkPolicy `waas-default-ingress`** : ingress refusé sauf depuis
  le namespace des CR **et** le namespace release (`WAAS_PLATFORM_NAMESPACE`,
  injecté par le chart via la downward API) — c'est là que guacd/wwt
  tournent réellement et ils doivent joindre les desktops. Egress libre.
- **Pas de RBAC utilisateur** : les utilisateurs ne parlent jamais à
  l'API Kubernetes (tout passe par le portail) ; en créer serait de la
  surface d'attaque gratuite.

⚠️ **Contrainte secretKeyRef** : les `env.valueFrom.secretKeyRef` d'un
template se résolvent dans le namespace **du pod**, donc dans le
namespace cible. Un template placé qui référence un Secret du namespace
plateforme (ex. `dev-ssh-credentials`) casse au démarrage
(`CreateContainerConfigError`) : provisionner le Secret dans les
namespaces cibles (External Secrets/Vault) ou ne pas placer ce template.
Les `credentialsSecretRef` des protocoles ne sont PAS concernés : ils
sont résolus côté api-server dans le namespace plateforme.

### GC et cleanup

- Les ownerReferences ne traversent pas les namespaces : les workspaces
  placés portent le **finalizer `waas.xorhub.io/teardown`** ; à la
  suppression, l'operator supprime compute + service dans le namespace
  cible (le PVC home est conservé — contrat inchangé). Les watches sont
  remappées par labels (`waas.xorhub.io/workspace` +
  `waas.xorhub.io/workspace-namespace`).
- **Cleanup du namespace : `Retain` par défaut** (`placement.cleanup`).
  Justification : supprimer un namespace supprime ses PVC, or le home
  survit à la suppression du workspace — Retain est le seul défaut qui ne
  peut pas détruire de données. `DeleteWhenEmpty` (opt-in) ne supprime
  que si l'operator a créé le namespace ET qu'aucun objet waas n'y reste
  (PVC home inclus — typiquement après un TTL).

## Naming des workloads (`spec.workloadName`)

- L'api-server calcule `sanitize(displayName)` (fallback : nom du CR) +
  suffixe anti-collision par namespace, et le fige dans
  `spec.workloadName` (immuable). Deployment/Service = `<workloadName>`,
  PVC home = `<workloadName>-home`.
- **Renommer le displayName ne renomme jamais le compute** (un rename =
  recréation explicite si le besoin émerge).
- Le webhook refuse les collisions de workloadName dans un même
  namespace cible (noms legacy `ws-<cr>` comptés).

### Migration de l'existant

Aucune : `workloadName`/`targetNamespace` vides ⇒ noms (`ws-<cr>`) et
namespace historiques conservés, ownerReferences et GC inchangés. La
nouvelle convention ne s'applique qu'aux créations. Aucun PVC n'est
déplacé ni recréé.

## Labels/annotations custom

- Template : `placement.namespaceLabels/namespaceAnnotations` (namespace)
  et `workload.labels/annotations` (Deployment **et** pod template).
- Workspace : `overrides.labels/annotations`, gated par le champ
  overridable **`metadata`**.
- **Denylist serveur** (`operator/pkg/metakeys`, webhook + re-filtrage
  reconciler) par domaine : `kubernetes.io` et sous-domaines
  (pod-security, kubectl…), `k8s.io`, `xorhub.io` (labels plateforme),
  `argoproj.io`, injecteurs (`istio.io`, `linkerd.io`,
  `vault.hashicorp.com`), `cilium.io`, `openshift.io`.
- Les labels de l'operator (ownership, sélecteurs) sont appliqués après
  merge : **ils gagnent toujours**, et le sélecteur du Deployment reste
  `waas.xorhub.io/workspace`.

## RBAC ajouté (operator)

`namespaces` create/delete, `resourcequotas` create,
`networkpolicies` create, `workspaces` update (finalizer) — miroir Helm
vérifié par `internal/controller/rbac_test.go`.
