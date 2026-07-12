# Prompt Fable 5 — Fix : `disable-copy`/`disable-paste` (connection settings) n'influencent ni le filtre clipboard wwt ni le menu session

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable.

## Contexte du repo : deux sources de vérité clipboard qui ne se parlent pas

Le registre de paramètres protocole (`operator/pkg/params/params.go:122-129`)
déclare deux booléens pour `vnc`/`rdp`/`ssh` :

```go
Name: "disable-copy", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindBool,
Description: "Block copying FROM the remote desktop to the local clipboard. Enforced by the wwt proxy, live-toggleable.",

Name: "disable-paste", Protocols: []string{"vnc", "rdp", "ssh"}, Kind: KindBool,
Description: "Block pasting FROM the local clipboard to the remote desktop. Enforced by the wwt proxy, live-toggleable.",
```

C'est un champ des « connection settings » du template/workspace (comme
tout param du registre : verrouillable côté template, éditable en
`userParams`/`protocolParams` selon les droits — cf.
[[waas-protocol-forms-feature3]] pour l'UI ParamField et
[[waas-userparams-cat-syntax-feature10]] pour la résolution `cat:`).

**Constat vérifié dans le code (pas une supposition)** : `disable-copy`/
`disable-paste` ne sont lus NULLE PART en dehors du registre lui-même
(définition + validation `ValidateUserOverrides`/`ValidateTemplateParams`
+ formulaire). `grep -rn "disable-copy\|disable-paste"` hors
`params.go`/tests/docs ne remonte que leur transit générique dans la map
de params — jamais un lookup par nom.

Pendant ce temps, deux mécanismes bien réels et DÉCONNECTÉS l'un de
l'autre gouvernent le clipboard :

1. **guacd lui-même**, via `ConnectionInfo.Params` →
   `guac.ConnectionParams.Extra` → `handshake.go:paramValue` (Extra
   gagne sur les défauts intégrés). Les valeurs de `entry.Params`
   (template) fusionnées avec `session.Params` (overrides de connexion,
   `workspace_service.go:644-659`) partent donc bien vers guacd, qui
   pour RDP applique nativement `disable-copy`/`disable-paste` comme
   restriction de canal presse-papiers FreeRDP. **Ce chemin-là semble
   correct** — à revérifier en pratique dans ta phase de vérification
   (point Tests ci-dessous), pas juste par lecture de code.
2. **Le filtre clipboard de wwt** (`wwt/internal/guac/clipboard.go`,
   `ClipboardFilter`) et **la capacité affichée au menu session**
   (`SessionOverlay.tsx`, toggles « copy from workspace » / « paste to
   workspace », lignes 138-139 et 273-303) partagent la MÊME source :
   `clipboardGrant`/`resolveClipboardGrant`
   (`api-server/internal/service/workspace_service.go:526-585`), qui ne
   regarde QUE `policy.ClipboardOf(pol)` — c'est-à-dire
   `WorkspacePolicy.Spec.Clipboard.{CopyFromWorkspace,PasteToWorkspace}`
   (`operator/pkg/policy/policy.go:164-174`). Le grant qui en sort est
   signé dans le token (`auth.NewConnectionClaims`,
   `workspace_service.go:478` et `remote_workspace_service.go:438`) et
   sert à la fois à construire le `ClipboardFilter` côté wwt
   (`wwt/internal/proxy/proxy.go:142`,
   `guac.NewClipboardFilter(claims.Clipboard.Copy, claims.Clipboard.Paste)`)
   ET à peupler `ConnectResult.Capabilities` (`workspace_service.go:488-491`)
   que le frontend affiche tel quel.

**Résultat concret (le bug rapporté)** : un workspace avec
`disable-copy: true` dans ses connection settings continue d'afficher
« copy from workspace : autorisé » dans le menu session dès que la
`WorkspacePolicy` de l'utilisateur autorise le copy — et le filtre wwt
laisse réellement passer le flux clipboard `guacd→browser`, puisqu'il
ne consulte jamais ce paramètre. Le commentaire de code
`workspace_service.go:468-476` (« Enforced by the wwt proxy ») documente
une intention jamais câblée : `resolveClipboardGrant` est
protocole-agnostic ET param-agnostic — jusqu'ici seul l'angle kasmvnc de
ce trou avait été noté ([[waas-kasmvnc-governance-feature11]]), mais il
touche tout aussi bien vnc/rdp/ssh via ces deux params dédiés.

## Ce qu'il faut livrer

1. **Calculer le grant effectif = policy ET params, jamais plus
   permissif que l'un des deux.** `disable-copy: true` doit forcer
   `copyFrom = false` quelle que soit la policy ; `disable-copy` absent
   ou `false` laisse la policy décider seule (ce n'est PAS un « force
   allow »). Même règle symétrique pour `disable-paste`/`pasteTo`.
2. **Brancher ça aux deux call sites de `resolveClipboardGrant`**
   (`workspace_service.go:477` dans `Connect`, et
   `remote_workspace_service.go:437` dans son équivalent remote) en leur
   passant la map de params EFFECTIVE de la session, càd le même merge
   que `ConnectionInfo` fait déjà : params du template/de l'entrée
   registrée d'abord, puis `session.Params`/`in.Params` par-dessus
   (`workspace_service.go:650-656` pour le local ; à répliquer pour le
   remote où `entry.Params` — le stocké — et `in.Params` jouent le même
   rôle, cf. `remote_workspace_service.go:418-430`).
   - Piège concret dans `Connect()` (`workspace_service.go:395-452`) :
     le template n'est fetché QUE si `len(in.Params) > 0 ||
in.Protocol != ""`. Un workspace dont `disable-copy: true` vit
     uniquement dans le template (aucun override à la connexion) ne
     déclenche ni l'un ni l'autre — il faut donc résoudre `entry.Params`
     indépendamment de cette condition avant d'appeler `clipboardGrant`
     (ligne 477), sans dupliquer l'appel Kube existant si le template a
     déjà été fetché plus haut dans le même appel.
3. **Un point de conversion `map[string]string` → override booléen**,
   probablement une petite fonction dans `workspace_service.go` (ou le
   package `params` si tu préfères la co-localiser avec le registre) du
   genre `clipboardParamOverride(params map[string]string) (blockCopy,
blockPaste bool)` lisant `params["disable-copy"]`/`["disable-paste"]`
   via `strconv.ParseBool`. **Fail-closed sur erreur de parsing** (une
   valeur malformée doit bloquer, pas être ignorée) — c'est déjà la
   doctrine du commentaire existant ligne 526-528 (« Resolution failure
   fails closed: session yes, clipboard no »), applique-la à l'identique
   ici plutôt que d'inventer un autre comportement.
4. **Vérifie/ajuste `EndSession`/reconnect** : confirme qu'aucun autre
   chemin ne recalcule un grant à partir de la policy seule sans
   reappliquer ce clamp (`grep -rn resolveClipboardGrant` pour être
   exhaustif — à ce jour seuls les deux call sites listés existent, mais
   reconfirme après ton changement).

## Contraintes

- Ne touche pas à `ClipboardFilter`/`clipboard.go` (le mécanisme de
  filtrage lui-même est correct et déjà testé) — le bug est en amont,
  dans la RÉSOLUTION du grant qu'on lui passe, pas dans son
  enforcement.
- Ne transforme pas `disable-copy`/`disable-paste` en un mécanisme qui
  pourrait ASSOUPLIR la policy : ces params ne peuvent que restreindre
  davantage, jamais outrepasser un refus de la `WorkspacePolicy`. Si tu
  hésites sur un cas limite, le test de non-régression le plus utile
  est : policy refuse + param absent/false → toujours refusé ; policy
  autorise + param `true` → refusé ; policy refuse + param `true` →
  refusé ; policy autorise + param absent/false → toujours autorisé
  (comportement actuel inchangé).
- `resolveClipboardGrant` reste utilisable sans params dans les
  contextes où ils ne sont pas pertinents (garde une signature qui ne
  casse pas d'appelant existant si tu ajoutes un paramètre — vérifie
  tous les call sites avant de changer la signature).
- Ne redocumente pas `params.go:122-129` sauf si le texte devient
  factuellement faux après ton fix (il devrait au contraire devenir
  vrai).

## Tests

- `api-server/internal/service/workspace_service_test.go` (ou fichier
  équivalent) : cas policy×param croisés listés ci-dessus, pour
  `Connect` ET pour l'équivalent remote-workspace, en couvrant
  spécifiquement le cas « `disable-copy` uniquement dans le template,
  aucun override à la connexion » (le piège du point 2).
- Vérifie que `ConnectResult.Capabilities` ET le grant signé dans le
  token portent la MÊME valeur clampée (pas de divergence entre ce que
  wwt enforce et ce que le menu affiche — c'est exactement le bug
  d'origine).
- Si l'environnement de dev est disponible, une vérification e2e sur un
  workspace RDP avec `disable-copy: true` dans les connection settings :
  le menu session doit afficher « copy from workspace : bloqué » ET un
  copy réel remote→host doit échouer au niveau du proxy (pas seulement
  au niveau FreeRDP) — utile pour confirmer que les deux mécanismes
  (guacd natif + wwt) sont maintenant cohérents plutôt que
  redondants-par-chance. Cf. [[waas-guacd-clipboard-fix13]] pour la
  méthode de vérif clipboard en dev (HTTPS :8443 requis).
- `go build ./...` + `go test ./api-server/...`.

## Points ouverts (ton arbitrage)

- Emplacement de la fonction de conversion params→override
  (`workspace_service.go` local vs package `params` partagé) — les deux
  marchent, choisis selon ce qui te semble le plus proche de l'usage
  (le package `params` n'a aujourd'hui aucune logique de lecture de
  valeur, seulement du registre/validation ; ça peut être une raison de
  la garder côté service). arbitrage coté service
- Si tu factorises le merge template-params + session-params (déjà
  dupliqué entre `ConnectionInfo` et le nouveau besoin de `Connect`),
  une petite fonction partagée est bienvenue mais n'est pas le but
  premier de ce fix — ne te lance pas dans un refactor plus large que
  nécessaire., si le refactor n'est pas immense et est abordable via une petite fnction c'est une bonne idée de faire ça maitnenant
