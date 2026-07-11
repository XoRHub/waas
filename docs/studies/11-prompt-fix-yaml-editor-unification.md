# Prompt Fable 5 — Fix : un seul composant YAML, celui du champ « workload (advanced) » fait foi

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable.

## Contexte du repo

Le frontend a aujourd'hui **trois implémentations différentes** pour
« afficher/éditer du YAML », sans convergence :

1. **`YamlEditor`** (`frontend/src/components/YamlEditor.tsx:86-161`) —
   le composant le plus abouti : textarea transparente superposée à un
   mirror avec coloration syntaxique ligne par ligne (`highlight()`,
   L42-78), gouttière de numéros de ligne, surbrillance des lignes en
   erreur, et validation live via `parseYaml()` (L21-37, parse AST avec
   la lib `yaml`, `YamlIssue[]` ancrées à la ligne + un callback
   `validate?` pour de la validation sémantique par-dessus la syntaxe).
   Utilisé aujourd'hui à seulement deux endroits : le champ « workload
   (advanced) » de `TemplatesPage.tsx` (section commentée `/* ----------------
workload (advanced) ---------------- */`, L749-765, avec
   `validateWorkload` L23-34 qui vérifie que c'est un mapping YAML et
   que `kind` est `Deployment`/`StatefulSet`/`Pod`) et le champ policy de
   `GovernancePage.tsx:488`. **Ne touche pas ces deux usages-là**, ils
   sont déjà la référence.
2. **Un `<textarea>` brut** pour le champ admin `kasmvncConfig`, **dans
   le même fichier** `TemplatesPage.tsx:606-613` — aucune coloration,
   aucune gouttière, aucune validation live, alors que 150 lignes plus
   bas ce même fichier utilise `YamlEditor` pour le champ workload.
3. **`KasmVNCConfigView`** (`frontend/src/components/ProtocolTabs.tsx:182-209`)
   — la vue lecture seule côté utilisateur : un simple
   `<pre className="max-h-48 overflow-auto ...">{config}</pre>`
   (L200-203), sans coloration ni numérotation. Deux variantes
   `'template' | 'effective'` pilotent seulement le texte d'aide
   au-dessus (L196-198), pas le rendu du bloc lui-même. Utilisé dans
   `CreateWorkspaceDialog.tsx:447-451` (variant `'template'`, texte brut
   du template, le workspace n'est pas encore né) et dans
   `ConnectionSettingsDialog.tsx:204-208` (variant `'effective'`, le
   contenu fusionné lu via `useWorkspaceKasmVNCConfig`, cf.
   `docs/studies/10-prompt-feature12-kasmvncconfig-admin-default-merge.md`
   pour la fusion 3 couches côté contrôleur).

**Décidé** : `YamlEditor` est le composant qui fait foi. Les usages 2 et
3 doivent converger vers lui — pas l'inverse, et pas une quatrième
implémentation.

## Ce qu'il faut livrer

1. **Ajoute un mode lecture seule à `YamlEditor`** (`YamlEditor.tsx`) :
   un prop `readOnly?: boolean` qui pose l'attribut HTML `readOnly` sur
   le `<textarea>` (L135-147) — garde la sélection/le scroll/le copier-
   coller natifs, bloque juste la saisie. Le prop `onChange` reste
   requis dans la signature (pas de refactor de l'API), les appelants
   lecture-seule passeront un no-op (`() => {}`) — c'est le choix le
   plus simple, ne complique pas la signature pour un cas qui n'a pas
   besoin d'être optionnel. Garde le gutter et la coloration actifs en
   lecture seule (c'est tout l'intérêt de la bascule vers ce
   composant) ; le panneau d'erreurs (L149-158) ne s'affiche que si
   l'appelant passe un `validate`, ce qui restera le cas par défaut pour
   les deux vues lecture seule ci-dessous (pas de `validate` passé =
   pas de panneau, comportement inchangé pour elles).
2. **`kasmvncConfig` admin (`TemplatesPage.tsx:606-613`)** : remplace le
   `<textarea>` par `<YamlEditor value={input.kasmvncConfig ?? ''}
onChange={(text) => set({ kasmvncConfig: text })} rows={8} validate={...} />`.
   Écris `validateKasmVNCConfig` sur le même modèle que
   `validateWorkload` (L23-34) mais plus léger : vérifie uniquement que
   le contenu parsé est un mapping YAML (objet, pas un scalaire ni une
   liste) quand le texte n'est pas vide — **ne duplique pas** la
   validation des 3 clés clipboard-DLP interdites (webhook
   `workspacetemplate_webhook.go:150-167`) : la Feature 12 a déjà tranché
   ce point (« pas de validation dupliquée côté client, juste ne pas
   surprendre l'admin sur le retour d'erreur ») — le webhook renvoie
   déjà un message explicite en cas de tentative, laisse ce chemin
   d'erreur tel quel.
3. **Vue lecture seule utilisateur (`KasmVNCConfigView`,
   `ProtocolTabs.tsx:182-209`)** : remplace le `<pre>` (L200-203) par
   `<YamlEditor value={config} onChange={() => {}} readOnly rows={...} />`,
   à l'intérieur du même `if (config.trim() !== '')` — garde le message
   italique « vide » (L204-205) inchangé pour le cas vide, ne fais pas
   passer une chaîne vide dans `YamlEditor` pour ce cas-là. Calcule
   `rows` dynamiquement à partir du nombre de lignes du contenu, clampé
   pour rester visuellement proche de l'ancien `max-h-48` (~12rem ; avec
   `lineHeight: 1.25rem` dans `YamlEditor`, ça correspond à environ 9-10
   lignes visibles avant scroll interne) plutôt qu'un nombre de lignes
   fixe arbitraire — un template avec 3 lignes de config ne doit pas
   afficher un cadre vide de 10 lignes.
4. Les deux textes d'aide selon `variant` (L196-198) et le titre
   (L192-194) restent inchangés — seule la zone de rendu du contenu
   change de composant.

## Contraintes

- N'ajoute pas de nouvelle dépendance (pas de Monaco/CodeMirror) — le
  CSP interdit les CDN, `YamlEditor` est déjà 100% local, c'est
  précisément pourquoi c'est lui la référence.
- Ne touche pas aux deux usages déjà sur `YamlEditor` (`TemplatesPage.tsx`
  workload, `GovernancePage.tsx`).
- i18n : aucune nouvelle chaîne attendue (les textes d'aide existants ne
  bougent pas) sauf si tu ajoutes un message d'erreur pour
  `validateKasmVNCConfig` — dans ce cas, clé sous
  `admin.templatesPage.*` dans `en.json`/`fr.json`, comme le reste du
  fichier.

## Tests

- `YamlEditor.test.ts` : le mode `readOnly` bloque bien la saisie
  (l'attribut est posé) sans casser le rendu de la coloration/gutter.
- `TemplatesPage.test.tsx` : le champ `kasmvncConfig` reste
  fonctionnel en round-trip save/reload une fois migré sur
  `YamlEditor` (le test existant tapait probablement dans un
  `<textarea>` par role/testid — adapte le sélecteur, ne supprime pas
  le test).
- `ProtocolTabs.test.ts` (ou nouveau test si absent pour
  `KasmVNCConfigView`) : le rendu non-vide passe bien par `YamlEditor`
  en lecture seule (pas d'`onChange` déclenché par une frappe utilisateur
  simulée), le rendu vide garde le message italique.
- `tsc -b` sur `frontend`.

## Points ouverts (ton arbitrage)

- Nombre exact de `rows` par défaut pour la vue lecture seule (formule
  de clamp précise) — donne la priorité à ne pas afficher un cadre
  disproportionné par rapport au contenu réel. => arbitrage, en 7 rows ,mais l'utilisaeur doit pouvoir scroller dedans
