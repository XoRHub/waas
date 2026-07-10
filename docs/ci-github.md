# CI GitHub Actions

GitHub est le dépôt canonique ; le pipeline GitLab (`docs/ci.md`) reste en
l'état jusqu'à son retrait. Un seul workflow : `.github/workflows/ci.yml`.

## Carte du pipeline

```
PR — sélectif par chemin (job `changes`, dorny/paths-filter)
├─ go-lint / go-test        matrice DYNAMIQUE par module, suit le graphe réel :
│                           api-server ← operator + shared ; wwt ← shared ;
│                           test/smoke ← operator (lint seul, tests = cluster)
├─ go-generated-drift       operator/** — regénérer doit être un no-op
├─ frontend                 typecheck + vitest (frontend/**)
├─ helm-manifests           lint + render + kubeconform vs CRDs de CE commit
├─ security                 gitleaks, trivy fs, hadolint, shellcheck — TOUJOURS
└─ build-images             par composant impacté × {amd64, arm64},
                            push:false, scan trivy local (amd64)

push main — TOUT est construit (invariant : chaque SHA de main porte le jeu
│           d'images complet, prérequis de la promotion)
├─ mêmes gates + build-images → push <short-sha>-<arch>
├─ merge-manifests           manifest list <short-sha> + tag mobile `main`
├─ scan-images               trivy bloquant sur la manifest list
└─ release-please            EN FIN de pipeline (needs sur tout le reste)
   └─ promote  (si release_created)   ZÉRO rebuild :
      verify (Chart.yaml = version, sources présentes, tags libres)
      → retag <short-sha> ⇒ vX.Y.Z → cosign keyless → table des digests
        ajoutée aux notes de Release
```

## Release (release-please, SemVer, conventional commits)

- Mode manifest : `release-please-config.json` + `.release-please-manifest.json`,
  **un seul package racine** — la plateforme sort en un tag `vX.Y.Z` unique
  (targetRevision ArgoCD, promotion en bloc). Les images desktop sont
  hors scope (repo `waas-images` séparé depuis le 2026-07-10, versionné
  par image).
- La release-PR bump `version.txt`, `CHANGELOG.md` et `helm/waas/Chart.yaml`
  (marqueurs `x-release-please-start-version`/`end` ; le `v` d'`appVersion`
  survit car l'updater ne matche que la partie numérique).
- **Procédure : merger la release-PR, c'est tout.** Le run main qui suit
  rebuilde/scanne le SHA mergé, crée tag + Release, puis promeut et signe.
- **Pas de PAT / GitHub App** : le tag créé avec `GITHUB_TOKEN` ne déclenche
  aucun workflow (anti-boucle GitHub) — c'est voulu. La promotion est
  chaînée dans le MÊME run via l'output `release_created`, ce qui garantit
  au passage que les images `<short-sha>` promues viennent d'être poussées
  par ce run (un `release.yml` séparé sur push main serait en course avec
  les builds). Un PAT ne serait nécessaire que pour re-déclencher un
  workflow `on: push: tags` séparé.
- Retry idempotent : `promote` tolère un tag release déjà présent **si le
  digest est identique** (re-run après échec partiel) et refuse sinon
  (immutabilité).

## Multi-arch : QEMU vs runners natifs

Variable de dépôt `WAAS_BUILD_STRATEGY` (Settings → Variables) :

| valeur | leg arm64 |
|---|---|
| `native` | `ubuntu-24.04-arm` (gratuit repos publics) |
| autre / absente | `ubuntu-latest` + QEMU (défaut sûr, repo privé) |

Le leg amd64 est toujours natif. Passer à `native` une fois le repo public.

## Signature cosign — écart avec GitLab

GitLab signe avec une **clé** (`COSIGN_PRIVATE_KEY`) ; GitHub signe en
**keyless OIDC** (Fulcio/Rekor, `id-token: write`). Vérification :

```sh
cosign verify \
  --certificate-identity-regexp 'https://github.com/<owner>/<repo>/\.github/workflows/ci\.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  <image>@<digest>
```

Conséquences : plus de secret à faire tourner, mais l'identité du repo est
inscrite dans le log public Rekor (OK, le repo devient public), et un
policy-controller cluster doit vérifier **l'identité de certificat** côté
GitHub vs la **clé publique** côté GitLab tant que les deux coexistent.

## Épinglage Renovate

Actions épinglées par SHA de commit (`helpers:pinGitHubActionDigests`),
images `docker run` des workflows couvertes par un customManager regex
(gitleaks, kubeconform, hadolint). Pins non gérés automatiquement (bump
manuel) : `version:` de golangci-lint-action et setup-helm, `node-version`.

## Écarts assumés / non porté (encore)

- **waas-images** : GitLab seulement, pas d'équivalent GitHub. Depuis le
  split du 2026-07-10 le sujet appartient au repo `waas-images` (voir son
  `docs/RECIPE-STUDY.md`, § CI GitHub Actions).
- **smoke-connections** (k3d + sessions guacd réelles) : trop lourd pour un
  runner hébergé 7 Go ; à porter sur runner self-hosted ou à garder GitLab.
- Images PR **non poussées** (le token des PR de forks ne peut pas écrire
  ghcr) — GitLab pousse des tags `mr-*` ; le scan PR se fait sur l'image
  chargée localement (amd64).
- `merge-manifests` attend TOUTES les paires build (GitHub ne fait pas de
  `needs` par leg de matrice, contrairement au DAG GitLab par composant).
- GitLab `release-verify` grep `appVersion` : les marqueurs release-please
  sont sur des lignes séparées, les greps existants restent valides.
