# Cartographie clipboard : ordre de priorité actuel (analyse, pas un prompt)

Document d'analyse, pas un prompt d'implémentation — objectif :
documenter fidèlement l'état RÉEL du code (vérifié fichier par
fichier, ligne par ligne) pour permettre un arbitrage sur "est-ce que
`WorkspacePolicy.spec.clipboard`, `WorkspaceTemplate.spec.protocols[].params`,
`.userParams` et le menu de session font doublon". Verdict à la fin,
mais lis d'abord la cartographie : la réponse n'est pas la même selon
la famille de protocole.

## Constat central : DEUX mécanismes disjoints, pas un seul

Le clipboard n'est pas gouverné par UN chemin de résolution, mais par
DEUX, sans aucun code partagé au-delà de `WorkspacePolicy.spec.clipboard`
et `policy.ClipboardOf()` :

- **guacd (`vnc`/`rdp`/`ssh`)** : 4 couches (policy, template `params`,
  template `userParams`, override de connexion), résolues à CHAQUE
  connexion, enforcées par le proxy wwt via le token de connexion.
- **`kasmvnc`** : 1 seule couche (policy), résolue au RECONCILE (pas à
  la connexion), bakée dans `~/.vnc/kasmvnc.yaml` par l'opérateur.
  `params`/`userParams` du template n'ont **aucun effet** — vérifié :
  `disable-copy`/`disable-paste` ne sont enregistrés que pour
  `vnc`/`rdp`/`ssh` (`operator/pkg/params/params.go:122-130`,
  `Protocols: []string{"vnc", "rdp", "ssh"}`), et le webhook template
  (`operator/internal/webhook/v1alpha1/workspacetemplate_webhook.go:68-69`,
  `params.ValidateUserParamNames`) rejette dès la création un
  `userParams` citant ces noms sur une entrée `kasmvnc` — de toute
  façon impossible à combiner avec `vnc`/`rdp`/`ssh` sur le même
  template (L93-97, `kasmvnc` est exclusif). Ce n'est donc pas un piège
  silencieux : le webhook empêche déjà la configuration incohérente.

## Cartographie des points de configuration

| Champ | Qui l'édite | Portée protocole | Quand évalué | Rôle réel |
|---|---|---|---|---|
| `WorkspacePolicy.spec.clipboard` (`CopyFromWorkspace`/`PasteToWorkspace`) | admin, CR | tous | à chaque connexion (guacd) / à chaque reconcile (kasmvnc) | **plafond de sécurité, seule autorité pour kasmvnc** |
| `WorkspaceTemplate.spec.protocols[].params["disable-copy"/"disable-paste"]` | admin, CR template | `vnc`/`rdp`/`ssh` uniquement | à chaque connexion, template refetché | valeur par défaut appliquée si l'utilisateur ne soumet pas d'override |
| `WorkspaceTemplate.spec.protocols[].userParams` | admin, CR template | `vnc`/`rdp`/`ssh` uniquement | à chaque connexion | **délégation de NOMS**, pas de valeurs : quels paramètres l'utilisateur peut soumettre en override |
| `ConnectInput.Params` (dialog "paramètres de connexion", `ConnectionSettingsDialog.tsx`) | utilisateur connecté (ou owner du template / admin) | `vnc`/`rdp`/`ssh` uniquement, noms délégués seulement | à chaque connexion, éphémère — jamais persisté sur un CR | valeur effective demandée pour CETTE session |
| Menu de session (`SessionOverlay.tsx`, overlay en session) | lecture seule | tous | affichage uniquement | reflète le résultat déjà clampé + explique QUI a bloqué (`ClipboardLockPolicy` vs `ClipboardLockParams`) |

Le "menu de session" **n'est jamais un point de décision** — c'est un
miroir du résultat déjà calculé côté serveur (`clipboardCapabilities`,
`api-server/internal/service/workspace_service.go:632-656`). Il n'y a
donc pas 3 autorités qui pourraient se contredire, seulement 1 plafond
(policy) et des restrictions en cascade.

## Chaîne de résolution exacte — guacd (`vnc`/`rdp`/`ssh`)

Code : `WorkspaceService.Connect`, `workspace_service.go:396-515`,
`clampClipboardGrant`/`mergeParams`/`clipboardCapabilities` L609-656.

```
1. policyGrant = policy.ClipboardOf(policy résolue du CONNECTÉ)
   → résolution échouée (pas de user, pas de policy matchée) = fail
     closed, (false, false)

2. Pour chaque direction (copy / paste), valeur effective du param :
   - si l'utilisateur a soumis une valeur dans ConnectInput.Params
     ET que ce nom est autorisé
       (nom ∈ params.ResolveUserParamNames(protocol, entry.UserParams)
        OU acteur == admin OU acteur == owner déclaré du template)
       ET la policy du connecté autorise le champ FieldProtocolParams
       (intersection template.Overrides.AllowedFields × policy.Overrides.AllowedFields)
     → cette valeur est utilisée (mergeParams : override ÉCRASE le défaut)
   - sinon, valeur du template (entry.Spec.Params[name]) si présente
   - sinon, absente (aucune restriction de cette couche)

3. effectiveGrant.direction = policyGrant.direction
                               AND NOT(valeur effective == true)
   → un param ne peut QUE restreindre, jamais élargir au-delà de la
     policy (test "false params never override a policy denial",
     connect_clipboard_test.go)

4. Le token de connexion signé embarque effectiveGrant — c'est lui que
   wwt fait respecter par tunnel, pas les capabilities.
   clipboardCapabilities(policyGrant, effectiveGrant) construit
   UNIQUEMENT la vue d'affichage (menu de session) + le label de la
   raison de blocage (policy gagne le label si les deux bloquent —
   retirer le param ne changerait rien).
```

**Point clé** : `params` (verrouillé) et `userParams` (délégué) ne
sont **pas mutuellement exclusifs dans le schéma** — un nom peut
apparaître dans les deux à la fois (`params` fournit une valeur par
défaut, `userParams` autorise l'utilisateur à la changer pour SA
session). Un admin qui veut un vrai verrou (aucune négociation
possible) doit simplement **omettre** le nom de `userParams` — le
mettre dans `params` seul suffit à fixer la valeur pour tout le
monde, sans qu'il soit nécessaire de le lister ailleurs pour que ce
soit "verrouillé". Ce n'est pas un bug, mais le mot "locked" dans les
commentaires du code peut prêter à confusion si on s'attend à ce que
`params` et `userParams` soient une paire mutuellement exclusive
(genre "soit fixé, soit délégué") — ce n'est pas le modèle réel.

## Chaîne de résolution exacte — `kasmvnc`

Code : `WorkspaceReconciler.ensureKasmConfig`/`kasmClipboardGrant`/
`applyClipboardPolicy`, `operator/internal/controller/kasm_config.go:76-190`.

```
1. Au RECONCILE (pas à la connexion) : policy.ClipboardOf(policy
   résolue du PROPRIÉTAIRE du workspace, pas du connecté — commentaire
   explicite : "Container-level DLP can only enforce ONE policy per
   workload, so it follows the owner").
   → résolution échouée = fail closed, (false, false).

2. Ces deux booléens sont stampés dans le kasmvnc.yaml effectif
   (admin's kasmvncConfig + ces clés en dernier, donc AUTORITAIRES) :
     data_loss_prevention.clipboard.server_to_client.enabled = copyAllowed
     data_loss_prevention.clipboard.client_to_server.enabled = pasteAllowed
     allow_client_to_override_kasm_server_settings = false (toujours,
       pour empêcher le client KasmVNC de rouvrir ce qui est fermé)

3. Le template `params`/`userParams` n'intervient JAMAIS ici — aucun
   levier de restriction ou de délégation supplémentaire n'existe pour
   kasmvnc, uniquement la policy.

4. Séparément, à la connexion, l'api-server calcule des `capabilities`
   pour l'affichage (menu de session) — mais avec la policy du
   CONNECTÉ, pas celle du propriétaire (`clipboardGrant` réutilise la
   même fonction `resolveClipboardGrant` que le chemin guacd, sans
   distinction propriétaire/connecté).
```

**Point de vigilance repéré (documenté dans le code, pas un secret,
mais à examiner si tu changes le modèle de partage)** : sur un
workspace `kasmvnc` PARTAGÉ (RW/RO — voir
[[waas-admin-workspace-scope-fix15]]), la policy réellement appliquée
dans le conteneur est celle du **propriétaire**, alors que ce que
l'utilisateur invité voit dans son menu de session (`capabilities`)
reflète SA PROPRE policy à lui. Le commentaire du code l'assume
explicitement : "The two agree on personal kasmvnc workspaces (owner
== connecting user); the operator follows the workspace owner because
container-level DLP is one-per-workload." Sur un partage entre deux
utilisateurs à policies différentes, le menu de session d'un invité
peut donc afficher un droit qui ne correspond pas à ce que le
conteneur applique réellement. Pas un chemin d'exploitation (DLP reste
fail-closed côté conteneur, donc jamais plus permissif que ce qui est
affiché dans le pire cas où l'invité aurait une policy PLUS
permissive que le propriétaire — dans ce cas le menu mentirait dans le
sens "plus restrictif que réel" ; l'inverse — menu optimiste, DLP
réel plus restrictif — est le cas gênant en UX, pas en sécurité).

## Verdict

**Pas un doublon** : dans les deux familles, `WorkspacePolicy.spec.clipboard`
reste la seule autorité de sécurité. Le template ne peut jamais
l'assouplir — côté guacd il peut seulement la restreindre davantage
(AND logique, jamais OR), côté kasmvnc il n'a aucun levier du tout.
Il n'y a pas 3 endroits qui "décident" indépendamment de la même
chose : un seul plafond, des restrictions en cascade, un webhook qui
empêche déjà la configuration incohérente (kasmvnc + userParams
clipboard rejeté à la création).

**Ce qui mérite ton arbitrage, pas un bug mais un choix de design** :
1. **Asymétrie structurelle assumée** entre guacd (4 couches
   négociables) et kasmvnc (1 couche, tout-ou-rien) — cohérent avec le
   fait que kasmvnc n'a pas de tunnel guacd à instrumenter en direct,
   mais ça veut dire qu'un admin ne peut JAMAIS déléguer le clipboard
   à l'utilisateur sur un workspace kasmvnc, même partiellement, alors
   qu'il le peut sur vnc/rdp/ssh.
2. **Divergence menu/réalité sur kasmvnc partagé** (ci-dessus) — à
   corriger si le partage RW/RO de workspaces kasmvnc devient un
   usage réel (aujourd'hui documenté comme acceptable parce que rare).
3. **Sémantique `params`+`userParams` non mutuellement exclusive** —
   fonctionne comme prévu mais le vocabulaire ("locked") peut induire
   en erreur un admin qui lirait le schéma sans le code ; à clarifier
   dans la doc CRD si tu gardes ce modèle tel quel.
