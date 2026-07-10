# Prompt Fable 5 — Feature 11 : combler le trou de gouvernance kasmvnc (API exposée + clipboard)

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable. **Priorité : c'est la prochaine feature kasmvnc à
implémenter**, avant toute nouvelle fonctionnalité sur ce protocole —
elle remplace et absorbe `docs/studies/04-prompt-feature4-kasmvnc-clipboard-enforcement.md`,
resté inachevé (seul un correctif d'affichage a été livré, commit
`87464e8a7865` — aucun enforcement réel).

## Contexte du repo

WaaS pilote KasmVNC via un chemin séparé de guacd :
`wwt/internal/kasm/kasm.go` fait un reverse-proxy HTTP/WebSocket brut
vers le pod KasmVNC (`proxyTo`, `kasm.go:167-202`), sans filtrage de
chemin — toute requête sous `/kasm/{sid}/…` est relayée telle quelle,
avec des identifiants Basic Auth injectés côté serveur (`kasm.go:187`,
`pr.Out.SetBasicAuth(username, info.Password)`, `username` par défaut
`"kasm_user"`).

**Ce que ce prompt corrige** : une recherche externe (doc officielle
KasmVNC + deux issues GitHub tranchées par un mainteneur upstream) a
mis à jour deux choses documentées dans
`docs/studies/kasm-images-feasibility.md` §« Mise à jour 2026-07-10 »
— lis cette section avant de commencer, elle contient les sources
exactes :

1. KasmVNC standalone expose une **API HTTP `/api/…` de neuf
   endpoints** (liste et détail dans la section citée ci-dessus),
   tous documentés « require owner credentials ». Le proxy wwt ne
   distingue pas ces endpoints du reste du trafic : si `kasm_user` a
   le rôle `owner` de sa propre session (à confirmer, voir tâche A),
   n'importe quel utilisateur WaaS y a accès aujourd'hui sans qu'aucun
   code du repo n'ait décidé de l'exposer.
2. Parmi ces neuf, un seul recoupe une capacité que WaaS gouverne
   explicitement ailleurs : `/api/downloads` (téléchargement de
   fichiers session → navigateur). Sur les protocoles guacd,
   l'équivalent (`enable-sftp`, `enable-drive`) est bloqué
   `TierPlatform` « until the file-transfer feature ships with its own
   policy gate » (`operator/pkg/params/params.go:365-371`). Rien
   d'équivalent n'existe côté kasmvnc — pas par choix documenté, par
   angle mort.
3. Le clipboard kasmvnc reste, comme documenté par la Feature 4
   d'origine, **affiché honnêtement mais toujours pas gouverné** :
   `resolveClipboardGrant` (`api-server/internal/service/workspace_service.go:608-647`)
   ne prend toujours aucun paramètre de protocole — vérifié à la date
   de ce prompt. `SessionOverlay.tsx:251-256` affiche un texte statique
   pour `protocol === 'kasmvnc'` au lieu de lire `capabilities`.

## Ce qui existe déjà (à connaître avant de coder)

- **Grant clipboard protocol-agnostic partout dans la chaîne** :
  CRD `WorkspacePolicySpec.Clipboard` sans champ protocole
  (`operator/api/v1alpha1/workspacepolicy_types.go:63-68`),
  `policy.ClipboardOf` sans paramètre protocole
  (`operator/pkg/policy/policy.go:161-174`), consommé identiquement par
  `workspace_service.go:607` et `remote_workspace_service.go:437`
  (kasmvnc est de toute façon banni des remote workspaces depuis
  `5e1e737d9a00`, donc seul le premier appel compte ici).
- **`kasmvncConfig`** (`WorkspaceTemplate.spec.kasmvncConfig`) est
  aujourd'hui le seul canal de configuration KasmVNC : une chaîne YAML
  opaque, jamais parsée par le repo, matérialisée en ConfigMap et
  montée à `<homeMountPath>/.vnc/kasmvnc.yaml`
  (`operator/internal/controller/kasm_config.go`). Un admin peut déjà y
  écrire à la main des directives DLP KasmVNC (clipboard, etc.) — rien
  ne les dérive automatiquement de `WorkspacePolicySpec.Clipboard`
  aujourd'hui.
- **Aucun point d'accroche existant** pour piloter dynamiquement le
  clipboard ou l'API kasmvnc depuis l'opérateur : contrairement aux
  autres injections (Secret `VNC_PW`, `kasm_credentials.go`), il n'y a
  pas de mécanisme qui recalcule une config kasmvnc à partir de la
  policy au reconcile.
- **Tests existants** : aucun test ne couvre le comportement
  protocole-spécifique de `resolveClipboardGrant`, ni l'exposition (ou
  non) des endpoints `/api/…` par le proxy wwt.

## Ce qu'il faut livrer

Traite dans cet ordre — B et C dépendent du choix arbitré en A.

### A. Auditer et trancher l'exposition de l'API kasmvnc (obligatoire, en premier)

1. En session live (k3d dev, `make` — vérifie `docs/studies/02-prompt-feature8-makefile-dev-bootstrap.md` pour le bootstrap si besoin), confirme concrètement : quel rôle porte `kasm_user` sur sa propre session (owner ? autre ?), et lesquels des neuf endpoints `/api/…` répondent réellement à travers le proxy wwt (`/kasm/{sid}/api/downloads`, etc.) avec le cookie de session standard. Ne suppose rien — l'inventaire dans `kasm-images-feasibility.md` vient de la doc développeur upstream, pas d'un test contre l'image réellement utilisée par WaaS.
2. Sur cette base, tranche : `wwt/internal/kasm/kasm.go` doit-il continuer à tout proxifier sans filtrage, ou introduire un allowlist/denylist de chemins pour les huit endpoints sans équivalent de gouvernance ailleurs (`get_screenshot`, `create_user`/`update_user`/`remove_user`, `send_full_frame`, `get_bottleneck_stats`, `get_frame_stats`, `clear_clipboard`) ? Pour une session mono-utilisateur qui s'administre elle-même, la plupart sont probablement sans conséquence (l'utilisateur a déjà le clavier et l'écran) — documente ce raisonnement plutôt que de bloquer par réflexe. `/api/downloads` est le seul qui a un traitement obligatoire, voir B.

### B. Gouverner `/api/downloads` comme le reste du transfert de fichiers

1. Décide et implémente un des deux traitements, cohérent avec la raison déjà actée pour `enable-sftp`/`enable-drive` (`params.go:365-371`, « until the file-transfer feature ships with its own policy gate ») :
   - soit **bloquer** `/api/downloads` au niveau du proxy wwt tant qu'aucune policy de transfert de fichiers n'existe (cohérence avec guacd : aucune fuite de fichiers par aucun chemin, pas d'exception pour kasmvnc) ;
   - soit le **conditionner** à un grant explicite si tu identifies qu'une policy de transfert existe déjà ou est en cours ailleurs dans le repo (vérifie avant de choisir cette option — à la date de ce prompt, `TierPlatform` est un blocage inconditionnel, pas un gate configurable).
2. Implémente le choix dans `wwt/internal/kasm/kasm.go` (c'est le seul point qui voit chaque requête proxifiée) — reste cohérent avec le style du fichier (pas de dépendance nouvelle si évitable, gestion d'erreur explicite plutôt que silencieuse).
3. Test Go : une requête vers `/kasm/{sid}/api/downloads` doit être rejetée (ou grantée selon ton choix) de façon déterministe et testée, pas seulement documentée.

### C. Finir le clipboard (reprise de la Feature 4 d'origine)

1. **Grant honnête** : rends `resolveClipboardGrant` protocole-conscient pour kasmvnc — soit il force `false`/`false` tant que l'enforcement réel n'existe pas, soit tu introduis un état explicite distinct de `true`/`false` (`ungoverned`) remonté dans `SessionCapabilities` (`api-server/internal/model/model.go`) pour que le frontend affiche un état fidèle plutôt qu'un texte statique déconnecté de `capabilities`.
2. **Enforcement réel** : investigue la configuration DLP de KasmVNC (doc officielle `kasmweb.com/kasmvnc/docs/latest/configuration.html` — ne l'invente pas, les noms de directives n'ont pas été vérifiés dans cette étude) pour déterminer comment désactiver le clipboard depuis `kasmvnc.yaml` ou via l'API `/api/clear_clipboard` (qui, elle, EST inventoriée — voir si elle peut servir de mécanisme de contrainte plutôt que de simple action ponctuelle). Câble : quand `policy.ClipboardOf` renvoie `(false, false)` pour un workspace kasmvnc, la configuration effective du conteneur doit réellement désactiver le copier/coller — pas juste cacher un bouton. Passe par l'opérateur (`kasm_config.go`, même mécanisme que `ensureKasmConfig`), pas par une logique ad hoc côté wwt.
3. **UI fidèle** : remplace le texte statique de `SessionOverlay.tsx:251-256` par un rendu qui lit `capabilities.clipboardCopy`/`clipboardPaste` comme les autres protocoles, avec une mention « clipboard KasmVNC natif » si utile pour distinguer du mécanisme guacd.

### D. Documentation

- Une fois A/B/C tranchés et livrés, mets à jour `kasm-images-feasibility.md` (les ❓ de la section « Mise à jour 2026-07-10 » doivent devenir des faits vérifiés) et `protocol-feature-matrix-2026-07-10.md` (note 7, ajoutée pour pointer ici — remplace-la par l'état final une fois ce prompt terminé).

## Contraintes à respecter

- Ne touche pas au chemin guacd (`ClipboardFilter`, `disable-copy`/`disable-paste`) — ce prompt est strictement le chemin kasmvnc.
- `go build ./...` + tests Go sur `wwt`, `operator`, `api-server` ; `tsc -b` + tests vitest sur le frontend.
- i18n : toute nouvelle chaîne passe par `frontend/src/i18n/locales/{en,fr}.json`.
- Ce prompt touche la surface de sécurité du proxy kasmvnc (filtrage de requêtes vers un pod utilisateur) : passe le diff final par `/security-review` avant de le considérer terminé, indépendamment des tests unitaires.
- Chaque tâche (A, B, C) est livrable seule, mais **B et C ne peuvent pas être arbitrées sans A** — ne saute pas l'audit live pour aller directement au code.

## Points ouverts (ton arbitrage)

- Rôle réel de `kasm_user` sur sa propre session (owner ou non) — determine toute la suite de la tâche A, à vérifier en premier.
- Allowlist stricte des huit endpoints non liés au transfert de fichiers, vs. statu quo documenté (laisser passer, avec la justification « session mono-utilisateur, pas de tiers à protéger contre l'utilisateur lui-même ») — ton jugement produit après l'audit A.2, pas une évidence technique.
- Noms exacts des directives DLP clipboard dans `kasmvnc.yaml` — à vérifier sur la doc officielle avant de coder, pas à deviner.
- État triple (`granted`/`denied`/`ungoverned`) vs bool simple pour `SessionCapabilities.ClipboardCopy/Paste` — change le contrat, documente le choix si tu introduis ce triple état (même point ouvert que dans la Feature 4 d'origine).
