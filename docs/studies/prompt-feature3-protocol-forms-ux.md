# Prompt Fable 5 — Feature 3 : toggles pour les booléens + dropdowns pour les options à défaut, dans les formulaires de paramètres protocole

Colle ce document tel quel comme prompt d'implémentation. Il part du principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Contexte du repo

WaaS pilote des sessions VNC/RDP/SSH via guacd. Tous les paramètres de connexion guacd (couleur, audio, presse-papier, mise en page clavier, etc.) viennent d'un **registre unique** côté Go, `operator/pkg/params/params.go` : chaque `Param` a un `Kind` (`string`/`bool`/`int`/`enum`), un `Tier` (`ui`/`advanced`/`platform`), un `Default` (documente la valeur par défaut de guacd lui-même, jamais envoyée telle quelle — une valeur vide = "laisser guacd appliquer son défaut"), et pour `enum` un `Enum []string`. Ce registre est exposé tel quel au frontend via `GET /api/v1/meta/protocols` et rendu par un seul composant de formulaire — **tout changement dans ce composant s'applique automatiquement à tous les écrans qui affichent des paramètres protocole**, pas seulement Connection Settings (voir "Portée" plus bas).

## Ce qui existe déjà (à connaître avant de coder)

Le rendu par paramètre est **entièrement centralisé** dans `frontend/src/components/ParamField.tsx` :

```tsx
switch (meta.kind) {
  case 'enum': return <select>...<option value="">({meta.default})</option>...meta.enum.map(...)</select>;
  case 'bool': return <select>...<option value="">({meta.default})</option><option value="true">true</option><option value="false">false</option></select>;
  case 'int':  return <input type="number" placeholder={meta.default} .../>;
  default:     return <input placeholder={meta.default} .../>;  // string
}
```

- **`bool` est déjà un `<select>` tri-état** (vide = hérite du défaut guacd, `true`, `false`) — jamais une checkbox. C'est ce `<select>` qu'il faut transformer en toggle visuel.
- **`enum` est déjà un dropdown** avec le défaut en première option vide — rien à faire ici, c'est le modèle à suivre pour le reste.
- **`int`/`string` (sans enum) affichent le défaut seulement en `placeholder`** — texte grisé qui disparaît dès que l'utilisateur tape, et qui ne distingue pas visuellement "champ vide = défaut appliqué" de "champ vide = rien n'a encore été saisi".

`ParamField` est appelé par `ProtocolParamsForm` (`frontend/src/components/ProtocolTabs.tsx:163-219`), lui-même utilisé par :

- `ConnectionSettingsDialog.tsx` (la cible explicite de cette feature),
- `CreateWorkspaceDialog.tsx` (section protocole à la création),
- `RemoteWorkspaceDialog.tsx` (paramètres des machines distantes),
- `TemplatesPage.tsx` (éditeur admin de template, avec en plus la case "user-overridable" par paramètre),
- `SessionOverlay` (réglages en session).

**Portée — décision à prendre en connaissance de cause :** la demande porte sur "les menus de connection Settings en rapport avec chaque protocole". Techniquement, `ParamField`/`ProtocolParamsForm` n'ont pas de variante par écran — modifier le rendu du `Kind` s'applique partout où le composant est monté. Notre recommandation : traiter ça comme une amélioration du composant partagé (donc visible partout), plutôt que de forker le rendu par écran — un paramètre `bool` a le même sens partout, il n'y a pas de raison qu'il soit une case à cocher ici et un select ailleurs. Si tu préfères limiter l'effet à Connection Settings uniquement, il faudra ajouter une prop de variante à `ParamField`/`ProtocolParamsForm` et l'assumer comme un choix explicite (documente-le).

## Ce qu'il faut livrer

### A. Les paramètres booléens deviennent des toggles

Remplace le rendu `case 'bool'` de `ParamField.tsx` par un composant toggle/switch visuel au lieu du `<select>` actuel.

**Point d'attention non trivial** : le `<select>` actuel est **tri-état** (vide/true/false), pas binaire — "vide" signifie explicitement "pas de préférence, guacd applique son propre défaut" (ex. `read-only` par défaut `false`, `disable-copy` par défaut `false`, `ignore-cert` en RDP par défaut `true`). Un toggle classique est binaire (on/off) et ne peut pas représenter nativement ce troisième état "hérité". Deux directions possibles, à trancher toi-même :

1. **Toggle à 3 positions** (segmented control : "Défaut" / "Activé" / "Désactivé") — préserve exactement la sémantique actuelle, un peu plus de travail visuel., dans ce cas il vaut mieux considere ça comme un dropdown avec les bonnes valeurs, qu'en penses tu ?
2. **Toggle binaire** dont l'état visuel initial reflète le `Default` du registre (ex. `disable-copy` par défaut `false` → toggle visuellement OFF quand la valeur est vide), plus un petit affordance "réinitialiser au défaut" à côté (cohérent avec le badge `live` déjà présent, `ParamField.tsx:85-89`) pour revenir explicitement à l'état vide/hérité plutôt que d'envoyer `"false"` en dur. C'est plus proche de l'intuition "toggle" mais demande de bien distinguer visuellement "hérité = false" de "explicitement réglé = false" (sinon on perd de l'information : un admin qui veut _forcer_ `false` sur un paramètre dont le défaut guacd est `true`, comme `ignore-cert`, doit pouvoir le faire explicitement).

Dans les deux cas : ne change rien à la valeur envoyée au backend (toujours `""`/`"true"`/`"false")`, ni au contrat `ParamMeta`/`meta.kind === 'bool'` côté validation serveur (`operator/pkg/params`, webhook, `api-server` Connect) — c'est un changement de rendu uniquement.

### B. Les paramètres avec un défaut deviennent des dropdowns

Pour tout paramètre `kind !== 'enum'` dont `meta.default` est non vide, remplace l'`<input>`/`<input type="number">` actuel (placeholder seul) par un contrôle qui **liste le défaut comme une option explicitement sélectionnable**, sur le modèle de ce qui existe déjà pour `enum`/`bool` (`<option value="">({meta.default})</option>`).

Paramètres réellement concernés aujourd'hui (extraits du registre, pour te donner la vraie portée avant de coder — ne les invente pas, relis `operator/pkg/params/params.go` si le registre a changé) :

- `int` avec défaut : `font-size` (6–48, défaut 12, SSH), `scrollback` (0–100000, défaut 1000, SSH avancé), `backspace` (1–255, défaut 127, SSH avancé).
- `string` avec défaut : `terminal-type` (défaut `linux`, SSH avancé). `clipboard-encoding` a un défaut mais est déjà `enum`.

**Tension à arbitrer** : un `<select>` pur n'a de sens que sur un ensemble fini et raisonnable de valeurs. Pour `scrollback` (plage 0–100000), lister toutes les valeurs possibles en dropdown est absurde. Notre recommandation : dropdown **hybride** — une liste déroulante qui propose "(défaut : X)" comme premier choix sélectionnable et éventuellement quelques valeurs usuelles, PLUS la possibilité de taper une valeur libre dans les bornes `min`/`max` (par exemple un `<input list="...">` avec `<datalist>`, qui garde le clavier natif tout en donnant une liste déroulante réelle — ou un petit composant "select ou custom" avec une option "Personnalisé…" qui bascule vers l'input numérique). Pour `terminal-type` (string libre), une vraie liste déroulante de valeurs `TERM` courantes (`linux`, `xterm`, `xterm-256color`, `vt100`, `screen`) + une option "Personnalisé…" est raisonnable puisque guacd n'impose pas d'énumération mais l'usage réel est très concentré sur une poignée de valeurs.

Documente ton choix dans le composant (commentaire au-dessus du switch `meta.kind`, à côté du commentaire existant qui explique déjà le mapping kind→widget).

## Contraintes à respecter

- Zéro changement de contrat côté Go (`operator/pkg/params`, validation webhook, validation `Connect` côté `api-server`) — cette feature est strictement une amélioration de rendu frontend sur des données déjà exposées par `GET /api/v1/meta/protocols`.
- `tsc -b` sans erreur, `strict: true`, zéro `any` (contraintes déjà tenues sur tout le repo, `docs/studies/audit-2026-07.md` §Frontend).
- Tests vitest sur le nouveau rendu (`ParamField.test.tsx` si le fichier existe déjà, sinon crée-le à côté de `ParamField.tsx` en suivant la convention des autres tests de composants du repo) — au minimum : un toggle bool reflète `value`/`meta.default` correctement dans les 3 états (hérité/true/false), un champ à défaut expose bien l'option "(défaut)" et retourne la bonne valeur au parent.
- i18n : toute nouvelle chaîne (ex. "Personnalisé…", libellés du toggle 3 états) passe par `frontend/src/i18n/locales/{en,fr}.json`.
- N'oublie pas `TemplatesPage.tsx` : l'éditeur admin ajoute un slot par paramètre (`renderParamExtra`, la case "user-overridable") — vérifie que ton nouveau rendu ne casse pas la mise en page à cet endroit (colonnes, alignement) puisque tu touches un composant partagé par cet écran aussi.

## Points ouverts (ton arbitrage)

- Toggle 3 états vs. toggle binaire + reset explicite pour les booléens (§A) — les deux sont défendables, tranche et documente.
- Forme exacte du dropdown hybride pour `int`/`string` à défaut (§B) — `datalist`, select+"Personnalisé…", ou autre : choisis la solution la plus cohérente avec le reste du design system Tailwind déjà en place (`fieldClass` dans `ParamField.tsx:3-4`).
- Portée (composant partagé vs. variante par écran) — recommandation ci-dessus, à confirmer ou à réviser si tu identifies un écran où le nouveau rendu serait réellement inapproprié (ex. `TemplatesPage.tsx` où l'admin veut peut-être forcer une vraie valeur libre sans dropdown).
