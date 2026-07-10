# CI/CD — pipeline GitLab et procédure de release

Un seul point d'entrée : `.gitlab-ci.yml` (racine), factorisé en templates
locaux sous `.gitlab/ci/`. La CI ne déploie **jamais** sur le cluster :
elle produit des artefacts versionnés (images + tag Git portant le chart)
qu'ArgoCD consomme.

## Vue d'ensemble

```
MR (merge_request_event) — builds sélectifs par rules:changes
├─ lint      go-lint ×5 (golangci-lint) · frontend-typecheck (tsc -b)
│            helm-render (lint+template) · crd-schemas → kubeconform
│            go-generated-drift (controller-gen/CRDs/docs regénérés == commit)
│            hadolint · shellcheck
├─ test      go-test ×4 (-race, couverture cobertura dans la MR)
│            frontend-test (vitest + couverture)
├─ security  gitleaks (secrets) · trivy-deps (go.mod + package-lock) — BLOQUANTS
├─ build     par composant modifié : build-<c>-amd64 (runner `amd`)
│            + build-<c>-arm64 (runner `arm`) → merge-<c> (manifest list)
│            tags poussés : mr-<iid>-<short-sha>
├─ scan      scan-<c> : trivy image sur le manifest mergé — BLOQUANT
└─ validate  smoke-connections (k3d + session guacd réelle par protocole)
             manuel en MR mais allow_failure:false → gate de merge

main — mêmes gates, build de TOUS les composants (tags <short-sha> + main)
       smoke-connections automatique. Chaque SHA de main est releasable.

tag vX.Y.Z — PROMOTION, zéro rebuild
├─ release-verify   Chart.yaml bumpé (version=X.Y.Z, appVersion="vX.Y.Z"),
│                   images <short-sha> présentes, tags vX.Y.Z encore libres
├─ release-images   imagetools create <short-sha> → vX.Y.Z (digests identiques
│                   à ce qui a été testé/scanné) + signature cosign obligatoire
├─ release-notes    git-cliff (cliff.toml) + table des digests
└─ release-create   GitLab Release sur le tag
```

`workflow:rules` : pipeline MR prioritaire (jamais de doublon branche+MR),
branche par défaut, tags `v*` uniquement. Tous les jobs sont en DAG
(`needs`), les stages ne servent qu'à la lisibilité.

## Multi-arch (amd64/arm64)

Builds **natifs par architecture** : les runners amd64 portent le tag
`amd`, les runners arm64 (Turing RK1) le tag `arm`. Chaque job pousse
`<tag>-amd64` / `<tag>-arm64` (cache buildx registry par composant et par
arch : `<registry>/cache:<composant>-<arch>`), puis `merge-<c>` assemble
la manifest list finale via `docker buildx imagetools create`. Aucun
QEMU : les Dockerfiles Go cross-compilent depuis `$BUILDPLATFORM` et le
frontend builde nativement des deux côtés.

Prérequis runners : executor Docker avec dind (privileged), tags `amd` et
`arm` posés. Les jobs sans besoin d'arch spécifique vont sur `amd` (flotte
la plus puissante) via `default:tags`.

Les images desktop (repo `waas-images`, **séparé du monorepo depuis le
2026-07-10**) suivent la même stratégie dans leur propre pipeline : un
job de build natif par arch (smoke + scan Trivy sur **chaque** arch,
avant push) puis un job de merge qui assemble la manifest list, publie
le tag `<version>` immuable (main) et signe. Le job trigger
`waas-images:` du pipeline racine a été supprimé avec le split — aucun
changement de ce repo ne peut plus affecter ces images.

## Couper une release

1. **Bumper le chart** (c'est le tag Git qu'ArgoCD déploie, le chart au
   tag doit se référencer lui-même) :
   ```yaml
   # helm/waas/Chart.yaml
   version: 1.2.0        # X.Y.Z
   appVersion: "v1.2.0"  # vX.Y.Z — devient le tag d'image par défaut
   ```
   Commit via MR (`release: v1.2.0`), merge, **attendre le pipeline main
   vert** (il produit les images `<short-sha>` qui seront promues).
2. **Tagger le commit de merge** :
   ```sh
   git tag v1.2.0 <sha-du-merge> && git push origin v1.2.0
   ```
3. Le pipeline de tag promeut, signe, génère changelog + Release. S'il
   échoue en `release-verify`, rien n'a été poussé : corriger et re-tagger
   (nouvelle version — jamais réutiliser un tag).
4. **Côté ArgoCD** : bump `targetRevision: v1.2.0` (Application pointant
   ce repo, path `helm/waas`) — seul geste GitOps nécessaire.

### Immutabilité des tags

- `release-verify` échoue si `vX.Y.Z` existe déjà dans la registry.
- À configurer côté GitLab (une fois) : **Settings → Repository →
  Protected tags → `v*`** (création réservée aux Maintainers, pas de
  suppression) ; la registry GitLab ne protège pas les tags d'image en
  écrasement, c'est le couple verify + tag Git protégé qui garantit
  l'immutabilité.

## Variables CI/CD requises (Settings → CI/CD → Variables)

| Variable | Type | Usage |
|---|---|---|
| `COSIGN_PRIVATE_KEY` | masked | clé de signature (la même que le repo waas-images) — la release **échoue** sans elle |
| `COSIGN_PASSWORD` | masked | passphrase de la clé |

Tout le reste (registry, tokens) passe par les variables prédéfinies
`CI_REGISTRY_*`. `TRIVY_SEVERITY` / `TRIVY_EXIT_CODE` existent pour la
réponse à incident (ex. passer un scan en report-only le temps d'un fix
upstream) — jamais à poser en permanence.

## Nettoyage des images éphémères

Les tags `mr-<iid>-<sha>`, `<sha>` et `<sha>-{amd64,arm64}` sont
éphémères. À configurer (une fois) : **Settings → Packages and
registries → Container registry → Cleanup policies** :

- run : toutes les semaines ;
- **keep** : regex `^(v\d+\.\d+\.\d+|main|\d+\.\d+\.\d+.*)$` + 5 tags
  les plus récents par image ;
- **remove** : regex `.*`, plus vieux que 14 jours.

Les policies ne suppriment que les tags ; la récupération des blobs est
la garbage collection de l'instance GitLab (`registry-garbage-collect`,
côté admin). Le repo de cache buildx (`<registry>/cache`) s'écrase en
continu et ne grossit pas indéfiniment.

## Débugger un job

- **Reproduire en local** : chaque job est une image + un script courts.
  `go-lint` ≈ `cd <module> && golangci-lint run` ; `kubeconform` ≈
  `python3 hack/ci/crd_to_jsonschema.py helm/waas/crds /tmp/s && helm
  template waas helm/waas | kubeconform -strict …` ; les builds ≈
  `sh .gitlab/ci/build-app-image.sh` avec `COMPONENT/ARCH/BUILD_CONTEXT/
  APP_TAG` posés.
- **Tester une MR sur un cluster** : les images `mr-<iid>-<short-sha>`
  sont dans la registry du projet ; `helm upgrade --install waas
  helm/waas --set image.registry=$CI_REGISTRY_IMAGE --set image.tag=mr-…`.
- **`go-generated-drift` rouge** : lancer `make generate manifests
  docs-params` et committer le résultat.
- **`release-verify` rouge** : le message dit quoi corriger (Chart.yaml
  pas bumpé, tag posé sur un SHA sans pipeline main vert, ou tag d'image
  déjà existant).
- **Smoke** : voir `docs/smoke-connections.md`.

## Renovate

`renovate.json` (racine) couvre : `FROM` des Dockerfiles, images de jobs
dans `.gitlab-ci.yml` + `.gitlab/ci/*.yml`, pins d'outils dans les
scripts (`buildkit`, `trivy`, `cosign`, `binfmt`), `go run tool@vX`,
`CONTROLLER_GEN_VERSION`, `git-cliff`. Depuis le split du 2026-07-10, le
repo `waas-images` porte à nouveau son propre `renovate.json` (le config
racine l'avait absorbé à l'époque du monorepo). Règle d'hygiène :
**aucun `latest`** — toute nouvelle image de job doit être pinnée (tag
exact, digest ajouté par Renovate via `docker:pinDigests`).
