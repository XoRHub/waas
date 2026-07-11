# Prompt Fable 5 — Feature 14 : pouvoir désactiver le login local quand l'OIDC est configuré (`WAAS_LOGIN_OIDC_ONLY`)

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable.

## Contexte du repo

Aujourd'hui, le login local (username/password) et le login OIDC/SSO
coexistent **sans aucun moyen de désactiver le premier**, même quand un
IdP est correctement configuré :

- **Login local** — route publique `POST /api/v1/auth/login`
  (`api-server/internal/server/router.go:58`, hors du groupe
  `middleware.Auth`, commentaire explicite en tête de `New()`,
  `router.go:31-32` : _"Every /api/v1 route except login sits behind
  the JWT middleware — no bypass routes."_). Handler
  `AuthHandler.Login` (`api-server/internal/handler/auth_handler.go:37-49`)
  délègue à `AuthService.Login` (`api-server/internal/service/auth_service.go:38-80`) :
  lookup par username, refus générique si `user.PasswordHash == ""`
  (compte SSO-only, ligne 46-51 — volontairement le même message
  d'erreur qu'un mauvais mot de passe, anti-énumération), vérif bcrypt,
  puis signature JWT via `auth.NewAccessClaims` + `s.signer.Sign`
  (ligne 61-65).
- **Login OIDC** — entièrement optionnel : `OIDCConfig.Enabled()`
  (`api-server/internal/config/config.go:122-123`) ⇔ `IssuerURL != "" && ClientID != ""`.
  Dans `cmd/api-server/main.go:82-87`, `oidcSvc` reste `nil` si non
  configuré ; `AuthHandler` porte ce nil (`oidc *service.OIDCService`,
  `auth_handler.go:22`) et l'utilise déjà comme garde pour
  `OIDCStart`/`OIDCCallback` (404 `apierror.NotFound("SSO login is not
configured")`, lignes 84-87 et 109-112). `OIDCService.Callback`
  (`oidc_service.go:101-139`) rejoint **exactement le même chemin de
  délivrance de token** que le login local (`auth.NewAccessClaims` +
  `s.signer.Sign`, ligne 133-137) et retourne le même type
  `LoginResult`.
- **Le contrat frontend a DÉJÀ le champ qu'il faut, juste jamais
  branché.** `GET /api/v1/auth/providers` (`AuthHandler.Providers`,
  `auth_handler.go:63-77`) renvoie
  `{ local: bool, oidc: { enabled, name?, startUrl? } }` — mais `Local`
  est codé en dur à `true` (ligne 72 : `payload := struct{...}{Local: true}`).
  Côté frontend, `AuthProviders.local: boolean` existe déjà
  (`frontend/src/types.ts:325-333`) et `useAuthProviders()`
  (`frontend/src/hooks/useApi.ts:100-106`) le récupère, mais
  `LoginPage.tsx` (`frontend/src/pages/LoginPage.tsx:17`) ne
  déstructure que `providers.data?.data.oidc` — `local` n'est lu nulle
  part. Le formulaire username/password (lignes 34-74) est rendu
  **inconditionnellement** ; le bouton SSO (lignes 75-92) est seul à
  être conditionné, sur `oidc?.enabled && oidc.startUrl`.
- **Documentation d'invariant à réviser sciemment** :
  `OIDCService` porte un commentaire de conception explicite
  (`oidc_service.go:24-34`) — _"It exists NEXT TO local auth, never
  instead of it: local login stays available for the bootstrap admin
  and as break-glass when the IdP is down."_ — répété dans
  `config.go:68-71` (doc de `Config.OIDC`) et dans
  `helm/waas/values.yaml:93-96` (commentaire au-dessus du bloc `oidc:`).
  Cette feature contredit délibérément cet invariant pour les
  déploiements qui le demandent explicitement (`opt-in`, jamais le
  comportement par défaut) — voir § Tension bootstrap/break-glass,
  à traiter, pas à ignorer.
- **Bootstrap admin, sans rapport avec OIDC** :
  `UserService.EnsureBootstrapAdmin` (`api-server/internal/service/user_service.go:234-266`),
  appelé inconditionnellement à chaque démarrage
  (`cmd/api-server/main.go:120`) tant que la table `users` est vide,
  crée toujours un admin **avec mot de passe** (`cfg.AdminUsername`/
  `cfg.AdminPassword`, génération aléatoire + log si absent). Ce
  mécanisme ne regarde jamais l'OIDC et continuera de créer ce compte
  même quand le login local est désactivé pour tout le monde — c'est le
  cœur de la tension à trancher (§ ci-dessous).
- **Le mapping groupe OIDC → rôle admin existe DÉJÀ, séparément de
  cette feature** : `OIDCConfig.AdminGroups []string`
  (`config.go:108-114`, `WAAS_OIDC_ADMIN_GROUPS`) + `GroupsClaim`
  (`config.go:109-110`, `WAAS_OIDC_GROUPS_CLAIM`, défaut `"groups"`).
  Dans `oidc_service.go`, `syncUser` (lignes 176-232) assigne
  `auth.RoleAdmin` à la création **et à chaque login suivant** si
  l'utilisateur appartient à un groupe listé dans `AdminGroups`
  (`adminByGroups`, lignes 234-241) — mais **seulement si `AdminGroups`
  est configuré** (`oidc_service.go:219-221` :
  `if len(s.cfg.AdminGroups) > 0 { user.Role = role }`). Si
  `AdminGroups` est vide, tout nouvel utilisateur OIDC reçoit
  `auth.RoleUser` à la création et n'est plus jamais retouché — "roles
  stay admin-managed" (commentaire existant, `config.go:114`). Cette
  feature (§ Tension ci-dessous) doit composer avec ce mécanisme
  existant, pas l'ignorer.
- **Aucune trace existante** de cette feature : recherche
  `OIDC_ONLY|LoginMethod|AuthMethod|local_login|disableLocalLogin|OidcOnly`
  sur tout le repo → zéro résultat. Pas d'ADR sur l'auth
  (`docs/adr/` ne contient que 0001 template-boundary et 0002
  crd-evolution, aucun des deux ne mentionne l'auth).
- **Précédent de flag booléen à suivre** (env → Go → Helm) :
  `WAAS_METRICS_ENABLED`. Struct (`config.go:66`,
  `MetricsEnabled bool`), parsing inline dans `Load()`
  (`config.go:145`, `os.Getenv("WAAS_METRICS_ENABLED") == "true"` — pas
  de helper `envBool`, c'est l'idiome du fichier), consommation dans
  `main.go:72-74` et **deux** points dans `router.go` (middleware
  ligne 38-40, montage conditionnel de la route ligne 53-55). Côté
  Helm : `values.yaml:160` (`metrics.enabled: false`), variable émise
  **seulement si true** (jamais `value: "false"`) dans
  `templates/api-server.yaml:162-167` — le même idiome existe pour le
  bloc `oidc` (`{{- with .Values.apiServer.oidc }}{{- if .issuerURL }}`,
  `api-server.yaml:189-223`).

## Décision d'architecture

1. **Le nouveau champ vit dans `OIDCConfig`**, pas au niveau racine de
   `Config` — sémantiquement c'est une politique de login _à propos_
   de l'OIDC, et ça garde `Enabled()` et la nouvelle validation
   co-localisées :

   ```go
   // OIDCOnly disables local username/password login entirely once
   // OIDC is configured — every account must authenticate through the
   // IdP. Requires Enabled() == true (validated at startup); the
   // bootstrap admin account still EXISTS (EnsureBootstrapAdmin is
   // unconditional) but cannot log in locally while this is set — see
   // docs/studies/18-prompt-feature14-oidc-only-login.md for the
   // break-glass procedure.
   OIDCOnly bool
   ```

   Parsing dans `Load()`, à côté du reste du bloc OIDC
   (`config.go:151-162`) : `os.Getenv("WAAS_LOGIN_OIDC_ONLY") == "true"`
   — nom de variable délibérément **hors** du préfixe `WAAS_OIDC_*`
   (c'est une politique de login globale, pas un paramètre du
   provider), garde le préfixe `WAAS_LOGIN_` proposé par la demande
   initiale.

2. **Validation fail-closed au démarrage**, à côté du bloc existant
   `config.go:168-175` :

   ```go
   if cfg.OIDC.OIDCOnly && !cfg.OIDC.Enabled() {
       return nil, fmt.Errorf("WAAS_LOGIN_OIDC_ONLY requires OIDC to be configured (WAAS_OIDC_ISSUER/WAAS_OIDC_CLIENT_ID)")
   }
   ```

   Ça empêche le cas dangereux "flag mis à true par erreur, OIDC pas
   configuré ⇒ plus personne ne peut jamais se connecter" — le serveur
   refuse de démarrer plutôt que de silencieusement locker tout le
   monde dehors.

3. **`AuthHandler` porte déjà `oidcCfg config.OIDCConfig`**
   (`auth_handler.go:23`, injecté par `NewAuthHandler`,
   `auth_handler.go:27-29`) — **aucun changement de signature de
   constructeur nécessaire**. Utilise `h.oidcCfg.OIDCOnly` directement :
   - `Login` (`auth_handler.go:37-49`) : si `h.oidcCfg.OIDCOnly`, retourne
     `apierror.NotFound("local login is disabled — sign in via SSO")`
     avant même de décoder le body (même style que la garde
     `h.oidc == nil` de `OIDCStart`/`OIDCCallback`, lignes 84-87/109-112).
     Ça protège l'API même contre un appel direct qui court-circuite le
     frontend.
   - `Providers` (`auth_handler.go:63-77`) : `payload.Local = !h.oidcCfg.OIDCOnly`
     au lieu du `Local: true` en dur (ligne 72).
   - Garde le routing tel quel (`router.go:57-61`) — pas de montage
     conditionnel de route façon `/metrics` : le pattern déjà en place
     dans ce fichier pour "fonctionnalité optionnelle non configurée"
     est une garde en tête de handler + 404, pas une route absente
     (`OIDCStart`/`OIDCCallback` le font déjà pour le cas symétrique).
     Documente ce choix si tu préfères l'autre pattern, mais reste
     cohérent avec l'existant.

## Ce qu'il faut livrer

1. `api-server/internal/config/config.go` : champ `OIDCOnly bool` sur
   `OIDCConfig`, parsing `WAAS_LOGIN_OIDC_ONLY`, validation fail-closed
   décrite ci-dessus.
2. `api-server/internal/handler/auth_handler.go` : garde dans `Login`,
   `payload.Local` dynamique dans `Providers` — voir § Décision.
3. `frontend/src/pages/LoginPage.tsx` : quand
   `providers.data?.data.local === false`, ne rend PAS le formulaire
   username/password (lignes 39-59) ni son bouton submit (lignes
   68-74) — seulement le bloc OIDC (lignes 75-92, retire la condition
   du séparateur "ou" qui n'a plus de sens sans formulaire à côté).
   Gère aussi l'état de chargement de `useAuthProviders()` : ne flashe
   pas le formulaire pendant que la requête est en vol puis ne le
   fait pas disparaître d'un coup — un état "chargement" minimal
   (spinner ou simplement rien) le temps que `providers.isSuccess`
   soit vrai, avant de décider quoi afficher.
4. `frontend/src/types.ts` : aucun changement — `AuthProviders.local`
   existe déjà.
5. Helm — `helm/waas/values.yaml`, dans le bloc `apiServer.oidc:`
   (lignes 93-115), nouvelle clé après `providerName` :

   ```yaml
   oidc:
     ...
     providerName: OIDC
     # Disables local username/password login entirely once set —
     # every account must go through the IdP above. Requires
     # issuerURL/clientID to be set (the api-server refuses to start
     # otherwise). The bootstrap admin account is still CREATED but
     # cannot log in locally while this is true — see
     # docs/studies/18-prompt-feature14-oidc-only-login.md.
     #
     # Set adminGroups (below) at the same time, or no account will
     # ever be able to reach the admin role through SSO — only the
     # bootstrap admin has it, and it becomes unreachable once this
     # flag is true.
     disableLocalLogin: false
   ```

   `helm/waas/templates/api-server.yaml` : **n'émets PAS** cette
   variable à l'intérieur du bloc `{{- if .issuerURL }}` existant
   (lignes 190-221) — si tu le fais, un admin qui active
   `disableLocalLogin: true` en oubliant `issuerURL` verrait la
   variable silencieusement disparaître au lieu de déclencher l'erreur
   de démarrage du § Décision point 2. Émets-la juste après le bloc
   `{{- with .Values.apiServer.oidc }}` (donc au même niveau que le
   `{{- if .issuerURL }}`, pas dedans), seulement si `true` (même
   idiome que `WAAS_METRICS_ENABLED`, `api-server.yaml:162-167`) :

   ```yaml
   {{- with .Values.apiServer.oidc }}
   {{- if .disableLocalLogin }}
   - name: WAAS_LOGIN_OIDC_ONLY
     value: "true"
   {{- end }}
   {{- if .issuerURL }}
   ...bloc existant inchangé...
   {{- end }}
   {{- end }}
   ```

6. `helm/waas/values.yaml` (ou `docs/` équivalent) : si le repo a un
   doc de paramètres généré (`make docs-params` mentionné dans
   d'autres études) régénère-le pour que la nouvelle clé y apparaisse.

## Tension bootstrap admin / break-glass — à trancher explicitement

Le commentaire de conception existant dit que le login local doit
_toujours_ rester disponible pour le bootstrap admin et comme
break-glass si l'IdP tombe. Cette feature le contredit délibérément,
mais **de façon opt-in et documentée**, pas silencieusement. Ne code
PAS de bypass caché (par ex. "sauf pour `cfg.AdminUsername`") — ce
serait une porte dérobée non documentée dans le payload `Providers`
(le frontend afficherait "local désactivé" alors que l'admin, lui,
pourrait quand même). Le choix retenu :

- `EnsureBootstrapAdmin` reste inchangé et inconditionnel — le compte
  admin est toujours créé au premier démarrage, flag ou pas.
- Le flag coupe le login local pour **tout le monde sans exception**,
  admin compris — cohérence avec le payload `Providers` que voit le
  frontend.
- Le **break-glass documenté** devient : redéployer temporairement
  avec `disableLocalLogin: false` (ou `WAAS_LOGIN_OIDC_ONLY` absent)
  pour rouvrir le login local, se connecter avec le compte admin,
  corriger l'IdP, puis remettre le flag à `true`. C'est un acte
  d'admin de cluster (accès Helm/kubectl), pas un contournement de
  code — dans le même esprit que la doctrine déjà appliquée ailleurs
  dans ce repo pour les bypass admin ("reste une policy visible et
  auditable, jamais un chemin de code caché", voir
  `docs/studies/17-prompt-feature13-direct-image-deploy-waas.md` § Bypass
  admin pour le précédent).
- Documente ce compromis dans le commit/PR **et** dans le commentaire
  Helm du champ `disableLocalLogin` (déjà rédigé ci-dessus) — un futur
  lecteur qui active ce flag doit comprendre immédiatement la procédure
  de secours avant d'en avoir besoin en urgence.

### Cas de verrouillage total : `disableLocalLogin=true` sans `AdminGroups`

Le break-glass ci-dessus suppose qu'il existe *un* chemin pour obtenir
un rôle admin après coup. Ce n'est vrai que si `WAAS_OIDC_ADMIN_GROUPS`
est configuré (§ Contexte, mapping groupe→rôle). Si un opérateur active
`disableLocalLogin: true` **sans** configurer `AdminGroups` :

- le compte bootstrap créé par `EnsureBootstrapAdmin` est inatteignable
  (login local coupé) ;
- tout utilisateur qui se connecte via OIDC reçoit `auth.RoleUser` à la
  création et n'est **jamais** promu automatiquement (`AdminGroups`
  vide ⇒ pas de sync de rôle, `oidc_service.go:219-221`) ;
- résultat : **aucun compte admin n'est atteignable au quotidien**. Le
  break-glass décrit ci-dessus (rouvrir le login local) reste
  techniquement possible pour se connecter avec le compte bootstrap —
  mais uniquement parce qu'`EnsureBootstrapAdmin` continue de le créer
  en silence ; rien dans l'UI/API ne signale ce piège avant qu'un
  opérateur ne s'y cogne.

Ce n'est pas un cas exotique : c'est le résultat probable d'une
première mise en service où l'opérateur active `disableLocalLogin`
pour "faire propre" sans avoir encore mappé les groupes IdP. Livre au
minimum :

- Dans le commentaire Helm de `disableLocalLogin` (§ Ce qu'il faut
  livrer, point 5) : mentionner explicitement qu'il faut configurer
  `adminGroups`/`groupsClaim` en même temps, sinon personne ne peut
  jamais obtenir le rôle admin par ce biais.
- Un log d'avertissement au démarrage (pas une erreur fatale — ça reste
  démarrable via le break-glass bootstrap admin) quand
  `cfg.OIDC.OIDCOnly && len(cfg.OIDC.AdminGroups) == 0` : un message
  clair du type `"WAAS_LOGIN_OIDC_ONLY is set but WAAS_OIDC_ADMIN_GROUPS
  is empty — no account can become admin via SSO; only the bootstrap
  admin (currently unreachable while this flag is set) has the admin
  role"`. Décide toi-même de l'emplacement (à côté de la validation
  fail-closed existante, `config.go:168-175`, ou dans `main.go` après
  `Load()`) et documente ce choix — voir aussi § Points ouverts.

## Contraintes

- Le flag doit être fail-closed au démarrage (§ Décision point 2) — ne
  laisse jamais un déploiement dans un état où `OIDCOnly=true` et
  `Enabled()=false` cohabitent silencieusement.
- Ne touche pas à `OIDCService`/`OIDCStart`/`OIDCCallback` — le chemin
  SSO est déjà correct et hors scope de ce fix, à l'exception du
  commentaire de conception (`oidc_service.go:24-34`) à nuancer pour
  refléter que le "never instead of it" a maintenant une exception
  opt-in documentée.
- Ne crée pas de bypass caché pour le bootstrap admin (§ Tension
  ci-dessus) — le flag coupe le login local pour tout le monde ou pour
  personne.
- Le montage de la route `/auth/login` (`router.go:58`) ne change pas
  — la garde vit dans le handler, pas dans le router (cohérence avec
  `OIDCStart`/`OIDCCallback`).
- `frontend/src/pages/LoginPage.tsx` ne doit jamais afficher un
  formulaire vide/cassé pendant le chargement de `useAuthProviders()`
  ni un flash formulaire→disparition.

## Tests

- Go, `api-server/internal/config` : `Load()` avec
  `WAAS_LOGIN_OIDC_ONLY=true` seul (sans issuer/clientID) → erreur ;
  avec issuer+clientID+secret+redirectURL → OK,
  `cfg.OIDC.OIDCOnly == true`.
- Go, `api-server/internal/handler` (ou test d'intégration existant
  sur `auth_handler`) : `Login` avec `oidcCfg.OIDCOnly=true` → 404,
  quel que soit le couple username/password (même valide) ; `Providers`
  renvoie `local: false` dans ce cas, `local: true` sinon (cas par
  défaut actuel, non régressé).
- Vitest, `LoginPage.tsx` : `providers.data.local === false` ⇒ pas de
  `<form>`/champs username-password dans le DOM, bouton SSO présent ;
  `local === true` (ou absent, valeur par défaut avant fetch) ⇒
  comportement actuel inchangé ; état de chargement ne montre pas le
  formulaire puis ne le fait pas disparaître (pas de flash).
- `go build ./...` + tests Go sur `api-server` ; `tsc -b` + vitest sur
  `frontend` ; `helm template` sur le chart avec
  `apiServer.oidc.disableLocalLogin=true` sans `issuerURL` pour
  vérifier que la variable `WAAS_LOGIN_OIDC_ONLY` est bien émise même
  dans ce cas (c'est `config.go` qui doit refuser de démarrer, pas
  Helm qui doit la cacher).

## Points ouverts (ton arbitrage)

- Nom exact de la clé Helm (`disableLocalLogin` proposé, dans le bloc
  `oidc:` existant) — libre de renommer si tu trouves plus clair,
  documente le choix. => arbitrage ok avec ça
- Faut-il un message dédié sur `LoginPage.tsx` expliquant pourquoi le
  formulaire a disparu (ex. "Cette organisation exige une connexion
  SSO") plutôt que de simplement l'omettre ? Pas strictement
  nécessaire pour la feature, mais améliore l'UX si un utilisateur
  arrive avec un ancien favori/bookmark. Ajoute une clé i18n dans
  `frontend/src/i18n/locales/{en,fr}.json` (section `login.*`, => "non pas besoin"
  lignes 17-27 des deux fichiers) si tu choisis de l'implémenter.
- Le commentaire de conception `oidc_service.go:24-34` — reformulation
  libre tant que l'exception opt-in est mentionnée explicitement.
- Warning de démarrage pour `OIDCOnly=true && AdminGroups vide` (§
  Tension bootstrap admin / break-glass, sous-section "Cas de
  verrouillage total") : log d'avertissement au démarrage (recommandé,
  non fatal — le déploiement reste utilisable via bootstrap admin +
  break-glass) vs. rien de plus que le commentaire Helm/doc. Si tu
  choisis de ne rien coder, justifie-le explicitement dans le commit —
  ne laisse pas ce silence implicite comme dans la version initiale de
  cette étude.
