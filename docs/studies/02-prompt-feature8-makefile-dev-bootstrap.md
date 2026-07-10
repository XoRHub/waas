# Prompt Fable 5 — Feature 8 : cible make de bootstrap complet + reload qui rebuild aussi les images workspace

Colle ce document tel quel comme prompt d'implémentation. Il part du principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Contexte du repo

L'environnement de dev local tourne sur k3d, piloté par le `Makefile` racine (`/Makefile`). Il existe déjà un jeu de cibles bien découpées, mais (1) aucune cible unique n'enchaîne tout le bootstrap de zéro (cluster → build → load → deploy → images workspace), il faut aujourd'hui invoquer 4-5 commandes `make` dans l'ordre documenté seulement en commentaire, et (2) `dev-reload`, la boucle de rechargement rapide en cours de dev, ne rebuild/recharge que les 4 images de service (operator/api-server/wwt/frontend) — jamais les images desktop de `waas-images/`.

## Ce qui existe déjà (à connaître avant de coder — le Makefile actuel dans son intégralité pertinente)

```
GO_MODULES     := shared operator api-server wwt
CLUSTER_NAME   := waas-dev
DEV_NAMESPACE  := waas
IMAGE_TAG      := dev
DEV_IMAGES     := operator api-server wwt frontend
WORKSPACE_BASE_IMAGES := ubuntu-base-vnc ubuntu-base-rdp   # build-only, jamais importées
WORKSPACE_IMAGES      := ubuntu-xfce ubuntu-firefox dev-ssh
```

- `all: build` (ligne 22) — build Go uniquement, sans rapport avec le dev k3d.
- `dev-up` (75-83) : crée le cluster k3d (`hack/dev/k3d-config.yaml`) + installe cert-manager. Idempotent (`k3d cluster list ... || k3d cluster create ...`).
- `dev-build` (90) = `docker-build` (61-65) : build les 4 images de service, tag `ghcr.io/xorhub/waas/<img>:dev`.
- `dev-load` (92-96) : `k3d image import` en boucle sur `DEV_IMAGES`.
- `dev-deploy` (98-110) : `kubectl apply -f helm/waas/crds/` (les CRDs ne sont jamais mises à jour par un `helm upgrade`, seulement à l'install — d'où cet apply explicite systématique) + `helm upgrade --install waas ...` + seed du secret ssh + `dev-url`.
- `dev-reload` (112-119) : **déjà** `dev-build dev-load dev-deploy` + `kubectl rollout restart` sur les 4 deployments de service. Le commentaire au-dessus (112-116) explique que `dev-deploy` y est non-optionnel exprès : un incident réel (lockout netpol guacd) est arrivé parce qu'un restart seul réutilisait le rendu Helm précédent sans re-render du chart. **Donc le "reload qui rebuild les images" demandé existe déjà pour les 4 images de service** — le vrai manque est qu'il ne touche jamais aux images desktop.
- `dev-build-images` (122-126) : boucle `$(MAKE) -C waas-images build IMAGE=$$img` sur `WORKSPACE_BASE_IMAGES` + `WORKSPACE_IMAGES`.
- `dev-load-images` (128-142, dépend de `dev-build-images`) : `k3d image import waas-local/$$img:dev` sur `WORKSPACE_IMAGES` seulement (les images de base ne sont jamais importées, elles ne tournent jamais en pod) + reseed secret ssh + `kubectl apply` sur `images-dev.yaml`/`templates-dev.yaml`/`gitops/governance/policies.yaml`. Nécessite que le namespace existe déjà (donc après `dev-deploy`).
- Le flux "typique" est documenté seulement en commentaire (68-73) :
  ```
  make dev-up
  make dev-build dev-load dev-deploy
  make dev-load-images
  make dev-reload      # après modif de code
  make dev-down
  ```
- `smoke` (159-161) : lance `test/smoke/` contre l'URL dev (`WAAS_SMOKE_URL`, défaut `http://waas.127.0.0.1.nip.io:8080`).

Le `README.md:38-51` décrit un flux "Quickstart" simplifié (mise/`k3d cluster create waas`/`helm install waas helm/waas`) qui **ne correspond déjà plus** au vrai flux Makefile (mauvais nom de cluster, pas de mention des cibles réelles) — c'est un doc à corriger en même temps si tu le touches, mais ce n'est pas le cœur de cette feature.

## Ce qu'il faut livrer

### A. Une cible de bootstrap complet

Ajoute une cible (ex. `dev-bootstrap`) qui enchaîne tout, dans l'ordre correct découvert ci-dessus, en une seule invocation depuis un environnement totalement vierge :

```
dev-bootstrap: dev-up dev-build dev-load dev-deploy dev-load-images
	@echo "==> environnement dev prêt : $$(make dev-url)"
```

Vérifie que chaque cible listée est bien idempotente si la cible globale est relancée sur un environnement partiellement monté (c'est déjà globalement le cas — `dev-up` teste l'existence du cluster, `dev-deploy` fait un `upgrade --install`, `seed-ssh-secret.sh` est documenté idempotent) — ne dois rien réécrire dans les cibles existantes pour ça, juste vérifier que l'enchaînement ne casse rien. Ajoute-la à `.PHONY` (ligne 17-20) et au bloc de commentaire "Typical flow" (68-73) pour qu'il reste exact.

### B. Un reload qui rebuild aussi les images workspace

`dev-reload` couvre déjà les 4 images de service (voir ci-dessus — ne duplique pas ce mécanisme). Ajoute une variante qui inclut aussi le rebuild/rechargement des images desktop, par exemple :

```
dev-reload-all: dev-build dev-load dev-deploy dev-build-images dev-load-images
	kubectl -n $(DEV_NAMESPACE) rollout restart \
		deploy/waas-operator deploy/waas-api-server deploy/waas-wwt deploy/waas-frontend
```

Documente clairement la différence d'usage entre `dev-reload` (rapide, code des 4 services Go/frontend) et `dev-reload-all` (plus lent, inclut les images `waas-images/` — utile après une modif dans `waas-images/`) dans un commentaire au-dessus de chaque cible, sur le modèle du commentaire déjà présent pour `dev-reload` (112-116).

## Contraintes à respecter

- N'invente pas de nouveau mécanisme de build/import : réutilise strictement les cibles atomiques existantes (`dev-build`, `dev-load`, `dev-deploy`, `dev-build-images`, `dev-load-images`) en dépendances `make`, ne duplique pas leur logique inline.
- Corrige `README.md:38-51` pour qu'il pointe vers `make dev-bootstrap` plutôt que la séquence manuelle `k3d cluster create waas`/`helm install` qui ne correspond plus au Makefile réel (nom de cluster différent, cibles absentes).
- Ajoute un test/vérification CI légère si le repo en a un pour le Makefile (vérifie `.github/workflows/ci.yml` — sinon, un simple `make -n dev-bootstrap` en CI pour vérifier que la cible résout sans erreur de dépendance circulaire/cible manquante suffit, pas besoin de faire tourner un vrai k3d en CI si ce n'est pas déjà le cas ailleurs).
- Documente ces deux nouvelles cibles dans le bloc de commentaire "Typical flow" (lignes 68-73) et dans toute doc dev existante (`docs/*.md` mentionnant k3d/dev, si trouvée).

## Points ouverts (ton arbitrage)

- Nom des cibles (`dev-bootstrap`/`dev-reload-all` proposés) — choisis un nom cohérent avec les conventions déjà en place (`dev-*`) si tu préfères un autre nom, documente le choix.
- Faut-il que `dev-bootstrap` appelle aussi `smoke` à la fin pour valider que l'environnement fonctionne réellement (connexion réelle par protocole), ou laisser ça comme étape manuelle séparée comme aujourd'hui — les deux sont défendables ; si tu l'ajoutes, rends-le opt-out (ex. variable `SKIP_SMOKE=1`) car `smoke` prend plusieurs minutes.
