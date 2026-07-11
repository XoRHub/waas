# Prompt Fable 5 — Fix : refléter l'exclusivité de `kasmvnc` côté frontend et api-server

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable.

## Contexte : la règle existe déjà dans l'opérateur, nulle part ailleurs

Le webhook d'admission (`operator/internal/webhook/v1alpha1/workspacetemplate_webhook.go:93-98`)
rejette désormais tout `WorkspaceTemplate` qui déclare `kasmvnc` en
même temps qu'un autre protocole (`vnc`/`rdp`/`ssh`) :

```go
// kasmvnc is exclusive: it bypasses guacd, and its generated-password
// mechanism and the vnc/rdp one both inject VNC_PW under the same
// pod-copy Secret name — only one connection stack per template.
if seen[string(waasv1alpha1.ProtocolKasmVNC)] && len(seen) > 1 {
    return nil, v.deny(tpl, "protocol kasmvnc cannot be combined with vnc/rdp/ssh: it bypasses guacd and must be the template's only protocol")
}
```

Cette règle **n'est répliquée nulle part côté API/UI** :

- `api-server/internal/service/template_service.go`, fonction
  `specFromInput` (le pré-check qui existe précisément pour éviter
  qu'une erreur d'admission k8s remonte comme un 500 brut à l'admin —
  voir le commentaire ligne 285-287 sur `kasmvncConfig`) **rejoue déjà**
  la plupart des gardes du webhook (params registry, audio port,
  `defaults > 1`, `kasmvncConfig`) mais **pas** celle-ci, et **pas non
  plus** le garde "protocole déclaré deux fois" (webhook lignes 58-61,
  absent de `specFromInput`). Aujourd'hui, un admin qui déclenche l'un
  ou l'autre cas via l'API reçoit donc le refus d'admission k8s brut au
  lieu d'un `apierror.BadRequest` propre.
- Le frontend (`frontend/src/pages/admin/TemplatesPage.tsx`) construit
  déjà `unusedProtocols` (ligne 242) pour que le menu "+ Add a
  protocol" (`ProtocolTabs.tsx:123-150`) n'offre jamais un protocole
  déjà configuré — mais rien n'empêche aujourd'hui d'y ajouter
  `kasmvnc` à côté de `vnc`, ni d'ajouter `vnc` à côté de `kasmvnc`. Le
  seul filet de sécurité actuel est le message d'erreur brut du webhook
  affiché tel quel en pied de dialog (`TemplatesPage.tsx:285` :
  `{save.isError && <p ...>{save.error.message}</p>}`) — ça fonctionne
  déjà comme filet de secours (aucun changement requis là), mais ce
  prompt vise à éviter à l'admin de faire l'aller-retour serveur pour
  découvrir la règle.

## Ce qu'il faut livrer

### 1. api-server — mirror des deux gardes manquants

Dans `api-server/internal/service/template_service.go`, fonction
`specFromInput`, boucle sur `in.Protocols` (lignes 246-273) :

- Ajoute le garde "déclaré deux fois", même schéma que le webhook
  (`seen := map[string]bool{}`, `seen[p.Name]` avant l'ajout) :
  `apierror.BadRequest(fmt.Sprintf("protocol %q is declared twice", p.Name))`.
- Après la boucle, au même endroit que le check `defaults > 1` (ligne
  274-276) ou juste après, ajoute le garde d'exclusivité kasmvnc,
  même condition et même texte de message que le webhook pour rester
  cohérent sur toute la stack :
  `apierror.BadRequest("protocol kasmvnc cannot be combined with vnc/rdp/ssh: it bypasses guacd and must be the template's only protocol")`.

Utilise `waasv1alpha1.ProtocolKasmVNC` (déjà importé, cf. usage ligne
292) plutôt qu'un literal `"kasmvnc"` — suis la convention déjà en
place dans ce fichier (voir ligne 261 `string(waasv1alpha1.ProtocolVNC)`,
ligne 292 `string(waasv1alpha1.ProtocolKasmVNC)`).

Complète le commentaire de la ligne 243-244 (« Protocols: same
registry gate as the admission webhook ») si nécessaire pour que la
liste des gardes reflétés reste correcte — ne le réécris pas
entièrement.

### 2. Frontend — empêcher la combinaison au niveau du picker

Dans `frontend/src/pages/admin/TemplatesPage.tsx`, à côté du calcul de
`unusedProtocols` (ligne 238-242) :

- Si `kasmvnc` fait déjà partie de `protocols`, `unusedProtocols` doit
  être vide (aucun autre protocole n'est proposable).
- Si `protocols` contient au moins un protocole autre que `kasmvnc`
  (i.e. `vnc`/`rdp`/`ssh`), `kasmvnc` doit être retiré de
  `unusedProtocols` même s'il est présent dans le registre
  (`availableProtocols`).
- `vnc`+`rdp`+`ssh` doivent rester librement combinables entre eux,
  sans aucun changement de comportement.

Implémente ça comme un filtre supplémentaire sur `unusedProtocols`
(pas une nouvelle fonction de validation séparée à appeler au submit —
le pattern existant pour "un seul default" et "pas de doublon" est
déjà structurel, fais pareil ici plutôt que d'ajouter un validateur
appelé dans `onSubmit`). Garde le literal `'kasmvnc'` — c'est déjà le
pattern du fichier (`DEFAULT_PORTS` ligne 58 l'exclut déjà de la même
façon implicite, `protocols.some(p => p.name === 'kasmvnc')` ligne
596-626) ; il n'existe pas de type TS listant les protocoles connus
(confirmé : `WorkspaceProtocol.name` et `TemplateProtocolInput.name`
sont de simples `string`, les protocoles sont pilotés par le registre
`/api/v1/meta/protocols`), donc pas de nouvelle abstraction à
introduire pour un seul cas spécial.

Ne touche pas à `frontend/src/components/ProtocolTabs.tsx` : c'est un
composant partagé (utilisé aussi par
`frontend/src/dialogs/ConnectionSettingsDialog.tsx`,
`RemoteWorkspaceDialog.tsx` et `CreateWorkspaceDialog.tsx`), et
`kasmvnc` n'est de toute façon jamais proposé par
`RemoteWorkspaceDialog.tsx` (sa liste `unused` est câblée en dur sur
`['ssh', 'vnc', 'rdp']`, ligne 51 — les workspaces distants ne
supportent pas kasmvnc, ça reste hors sujet ici). Le filtrage doit donc
vivre uniquement dans `TemplatesPage.tsx`, propre au formulaire de
template.

**Ne gère pas de cas de "template existant déjà en violation" côté
UI** : un template pré-existant qui combine déjà `kasmvnc` et un
protocole guacd (grandfathering, cf. le prompt opérateur précédent)
s'affichera normalement en édition — le filtre ne s'applique qu'aux
protocoles *ajoutables*, il ne retire rien de la liste déjà chargée. Si
l'admin sauvegarde sans y toucher, le webhook le bloquera à
`ValidateUpdate` et l'erreur brute remontera via `save.error.message`
(`TemplatesPage.tsx:285`, déjà en place, rien à changer). C'est le
comportement voulu, pas un bug à corriger.

### 3. i18n — expliciter la règle dans le texte d'aide existant

`frontend/src/i18n/locales/en.json:271` (clé `protocolsHint`, sous la
légende "Protocols" du formulaire) et son miroir
`frontend/src/i18n/locales/fr.json:271` : le texte actuel ne mentionne
que le fonctionnement guacd-centrique. Ajoute une phrase indiquant que
`kasmvnc` court-circuite guacd et ne peut cohabiter avec aucun autre
protocole (rejeté à l'admission) — même angle que la doc déjà mise à
jour dans `docs/templates-and-protocols.md` par le prompt opérateur
précédent (vérifie sa formulation exacte pour rester cohérent, section
« Protocols », lignes ~38-63).

## Contraintes

- Ne duplique pas les gardes déjà corrects (`defaults > 1`, audio port,
  `kasmvncConfig`) — ce prompt ajoute strictement les deux gardes
  manquants côté api-server et le filtrage côté frontend, rien
  d'autre.
- Les messages d'erreur api-server doivent rester texte-identiques au
  webhook (même substring `"cannot be combined with vnc/rdp/ssh"` /
  `"is declared twice"`) pour qu'un test de cohérence inter-couches
  reste possible plus tard et que l'admin voie le même vocabulaire
  partout.
- `vnc`+`rdp`+`ssh` combinés (sans `kasmvnc`) doivent continuer à
  passer sans changement, aussi bien côté api-server que frontend —
  vérifie qu'aucun test existant sur ce cas ne casse.
- N'introduis pas de nouveau composant, hook ou type juste pour ce cas
  — le fichier `TemplatesPage.tsx` a déjà le contexte (`protocols`,
  `availableProtocols`) au bon endroit.

## Tests

- `api-server/internal/service/template_service_test.go` : ajoute une
  fonction de test dédiée (même style que
  `TestTemplateInputValidatesExposeAudioPort` ligne 10 et
  `TestTemplateInputValidatesKasmVNCConfig` ligne 61, avec le même
  helper `base(...)` à réutiliser ou adapter) couvrant :
  - `kasmvnc` + `vnc` → rejeté, message contenant `"cannot be combined"`.
  - `kasmvnc` + `rdp` → rejeté.
  - `kasmvnc` + `ssh` → rejeté.
  - `kasmvnc` + `vnc` + `rdp` + `ssh` → rejeté.
  - `kasmvnc` seul → accepté.
  - `vnc` + `rdp` + `ssh` (sans kasmvnc) → accepté.
  - un protocole déclaré deux fois (ex. `vnc` + `vnc`) → rejeté,
    message contenant `"declared twice"`.
- `frontend/src/pages/admin/TemplatesPage.test.tsx` : étends le fichier
  (le helper `base(protocol, kasmvncConfig?)` ligne 23 ne construit
  qu'un seul protocole — tu devras soit l'étendre pour accepter
  plusieurs protocoles, soit construire l'input directement dans le
  nouveau test) avec un nouveau `describe` couvrant :
  - avec `kasmvnc` déjà configuré, le menu "+ Add a protocol" ne doit
    proposer ni `vnc`, ni `rdp`, ni `ssh` (ou n'apparaît pas du tout si
    c'était les seuls protocoles du registre mocké).
  - avec `vnc` déjà configuré, le menu ne doit pas proposer `kasmvnc`
    (mais peut proposer `rdp`/`ssh`).
  - mock `/api/v1/meta/protocols` avec au moins `vnc`, `rdp`, `ssh`,
    `kasmvnc` pour ces tests (le mock actuel ligne 12-16 renvoie `[]`
    par défaut — passe une liste explicite dans les nouveaux tests
    sans changer le mock global des autres tests du fichier).
- `go build ./...`, `go test ./api-server/...`,
  `cd frontend && npm test` (ou la commande vitest déjà en place dans
  ce repo).

## Points ouverts (ton arbitrage)

- Si tu préfères exposer un tooltip explicatif sur le "+" quand
  aucun protocole n'est proposable à cause de cette règle (plutôt que
  le menu invisible silencieux actuel quand `unusedProtocols` est
  vide, cf. `ProtocolTabs.tsx:123` `{onAdd && (addable?.length ?? 0) > 0 && ...}`),
  documente ton choix — ce n'est pas demandé explicitement ici, la
  phrase ajoutée à `protocolsHint` (point 3) est considérée suffisante
  par défaut.
- Emplacement exact du garde "declared twice" dans `specFromInput`
  (dans la boucle vs. via une map construite avant) — les deux
  marchent, choisis le plus lisible à côté du `defaults` déjà accumulé
  dans la même boucle.
