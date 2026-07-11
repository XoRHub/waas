# Prompt Fable 5 — Fix : le protocole `kasmvnc` doit être exclusif (rejet si combiné à vnc/rdp/ssh)

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable.

## Contexte du repo : un choix laissé sans arbitrage explicite

`WorkspaceProtocol.Name` accepte `vnc`, `rdp`, `ssh` (brokerés par
guacd) et `kasmvnc` (endpoint web natif de kasmweb/*, reverse-proxié
par wwt, guacd hors jeu) —
`operator/api/v1alpha1/workspacetemplate_types.go:207-213`. Un
`WorkspaceTemplate.spec.protocols` est une **liste** : rien aujourd'hui
n'empêche un admin de déclarer `kasmvnc` à côté de `vnc`/`rdp`/`ssh`
sur le même template.

Une itération précédente (génération des credentials desktop) a
découvert que ce cas de figure posait un problème concret : les deux
mécanismes de mot de passe généré (`kasmPasswordGenerated`,
`operator/internal/controller/kasm_credentials.go:49`, et
`desktopPasswordGenerated`,
`operator/internal/controller/desktop_credentials.go:44`) nomment tous
les deux leur copie pod-namespace `computeName(ws)` (obligatoire pour
que le sweep de teardown par nom la retrouve) et injectent toutes les
deux `VNC_PW`. Un template `kasmvnc` + `vnc` aurait donc produit deux
Secrets en collision. Le correctif de l'époque a été de rendre les
deux mécanismes **mutuellement exclusifs au niveau runtime** :
`desktopPasswordGenerated` cède la main si `kasmPasswordGenerated`
répond vrai (kasm gagne) — testé par
`TestDesktopCredentialsYieldToKasm`
(`operator/internal/controller/desktop_credentials_test.go:153`).

**C'était un pansement, pas un arbitrage produit.** Le repo continue
d'accepter, à l'admission, un template qui mélange `kasmvnc` avec un
protocole guacd — un cas qui n'a jamais de sens fonctionnellement
(deux mécanismes de connexion radicalement différents sur le même
poste de travail, dont un seul peut réellement gagner la bataille du
mot de passe). L'arbitrage produit, maintenant tranché : **`kasmvnc`
est exclusif**. Un template qui le déclare ne peut déclarer aucun
autre protocole (`vnc`, `rdp`, `ssh`). `vnc`/`rdp`/`ssh` restent
librement combinables entre eux, comme aujourd'hui (ex.
`ubuntu-xfce`, commenté dans `desktop_credentials_test.go:14` : « serves
vnc AND rdp »).

## Ce qu'il faut livrer

1. **Webhook** (`operator/internal/webhook/v1alpha1/workspacetemplate_webhook.go`,
   fonction `validate`, autour de la boucle `for i := range
   tpl.Spec.Protocols` qui alimente déjà `seen map[string]bool`) :
   après la boucle (une fois `seen` complet), si `seen["kasmvnc"]` est
   vrai et que `seen` contient au moins un autre nom, refuse la
   création/mise à jour avec un message explicite, par exemple :
   `protocol kasmvnc cannot be combined with vnc/rdp/ssh: it bypasses
   guacd and must be the template's only protocol`. Suis le style des
   refus existants (`v.deny(tpl, fmt.Sprintf(...))`, cf. les autres
   `return nil, v.deny(...)` de la même fonction) plutôt que
   d'introduire un nouveau schéma de message. Ça s'applique aussi bien
   à `ValidateCreate` qu'à `ValidateUpdate` (les deux appellent déjà
   `validate`).
2. **GoDoc du type** (`workspacetemplate_types.go:207-213`, commentaire
   de `WorkspaceProtocol.Name`) : ajoute une phrase qui documente
   l'exclusivité (ex. « kasmvnc is exclusive: a template declaring it
   may declare no other protocol, admission-enforced »). Regénère les
   manifests dérivés du GoDoc si ton outillage le permet
   (`make manifests`/`make generate` selon ce qu'expose le Makefile —
   vérifie que ça retouche bien `helm/waas/crds/...` et
   `operator/config/crd/bases/...`, déjà modifiés dans l'arbre de
   travail courant par d'autres changements en cours ; ne les écrase
   pas à l'aveugle, régénère par-dessus).
3. **Doc utilisateur** (`docs/templates-and-protocols.md:38-63`,
   section « Protocols ») : la phrase actuelle (« A template may
   declare several protocols in guacd terms ») ne dit rien de
   l'exclusivité. Ajoute une ligne claire : `vnc`/`rdp`/`ssh` sont
   librement combinables entre eux, `kasmvnc` ne peut cohabiter avec
   aucun autre protocole (rejeté à l'admission).
4. **Nettoyage du commentaire runtime, sans supprimer le garde-fou** :
   les commentaires de `kasm_credentials.go` et
   `desktop_credentials.go` expliquent la mutuelle exclusion comme si
   c'était la seule protection existante (« Mutually exclusive with
   the kasm mechanism: both inject VNC_PW and share the pod-copy
   Secret name, so only one may generate. »). Mets ces commentaires à
   jour pour référencer le nouveau garde webhook comme protection
   primaire, et explique pourquoi le garde runtime reste nécessaire
   malgré ça (point 5 juste en dessous) — ne le supprime pas.
   `TestDesktopCredentialsYieldToKasm` doit continuer de passer tel
   quel : il teste une fonction Go pure, indépendante du webhook.

## Contraintes

- **Le webhook admission ne protège que les créations/mises à jour, pas
  les objets déjà en base.** Un `WorkspaceTemplate` existant qui
  combine déjà `kasmvnc` et `vnc`/`rdp`/`ssh` (s'il en existe) ne sera
  pas rejeté rétroactivement — il continuera de passer par le
  contrôleur tel quel jusqu'à sa prochaine écriture (`ValidateUpdate`
  s'appliquera alors et le bloquera, sauf s'il redevient conforme).
  **C'est précisément pourquoi le garde runtime
  (`desktopPasswordGenerated` cède à `kasmPasswordGenerated`) doit
  rester en place** : defense-in-depth pour ce cas de grandfathering,
  pas du code mort. Ne le retire pas sous prétexte que le webhook rend
  la combinaison « normalement » impossible.
- Ne touche pas au reste de `validate()` (params registry, audio port,
  `kasmvncConfig`, schedule, placement, workload labels) — hors scope.
- `vnc` + `rdp` + `ssh` combinés entre eux (sans `kasmvnc`) doivent
  rester acceptés sans changement — vérifie qu'aucun test existant
  couvrant ce cas ne casse.
- Vérifie `hack/dev/templates-dev.yaml` et tout autre fixture YAML du
  repo (`grep -rn "name: kasmvnc" -A2` autour de chaque `protocols:`) :
  aucune ne doit aujourd'hui combiner `kasmvnc` avec un protocole
  guacd (vérifié en amont de ce prompt, mais reconfirme après ton
  changement — un fixture qui casserait la CI serait un signal que
  quelque chose t'a échappé).

## Tests

- `operator/internal/webhook/v1alpha1/workspacetemplate_webhook_test.go` :
  étends `TestTemplateWebhookValidatesParamsAgainstRegistry` (ou une
  nouvelle fonction de test dédiée si tu préfères isoler ce cas) avec :
  - `kasmvnc` + `vnc` → rejeté ;
  - `kasmvnc` + `rdp` → rejeté ;
  - `kasmvnc` + `ssh` → rejeté ;
  - `kasmvnc` + `vnc` + `rdp` + `ssh` (les trois en même temps) →
    rejeté, message mentionnant bien `kasmvnc` ;
  - `kasmvnc` seul → toujours accepté (déjà couvert par le cas
    existant « clean kasmvnc », `workspacetemplate_webhook_test.go:92`
    — vérifie juste qu'il passe toujours) ;
  - `vnc` + `rdp` + `ssh` sans `kasmvnc` → toujours accepté (ajoute le
    cas s'il n'existe pas déjà).
- `operator/test/envtest/webhook_admission_test.go` : si ce fichier
  fait déjà tourner un scénario d'admission bout-en-bout sur des
  templates `kasmvnc`, ajoute-y le cas de rejet en combinaison (sinon,
  la couverture unitaire ci-dessus suffit — n'ajoute pas un test
  envtest juste pour dupliquer le test unitaire).
- `go build ./...` + `go test ./operator/...`.

## Points ouverts (ton arbitrage)

- Emplacement exact de la nouvelle vérification dans `validate()`
  (dans la boucle principale vs. juste après, sur `seen`) — les deux
  marchent, choisis celui qui te semble le plus lisible à côté du
  check `defaults > 1` déjà présent juste après la boucle.
- Si tu ajoutes une régénération CRD (`make manifests`/`make
  generate`) et qu'elle touche des fichiers déjà modifiés dans l'arbre
  de travail (`helm/waas/crds/...`,
  `operator/config/crd/bases/waas.xorhub.io_workspacetemplates.yaml`)
  pour d'autres
  raisons en cours, documente-le clairement dans le commit pour éviter
  toute confusion sur l'origine du diff.
