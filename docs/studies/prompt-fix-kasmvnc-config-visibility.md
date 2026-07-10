# Fix — Visibilité read-only du kasmvncConfig côté utilisateur

*2026-07-10 — implémenté. L'utilisateur VOIT la config KasmVNC qui
s'applique à son workspace ; l'édition reste strictement admin (page
Templates), aucun nouveau chemin d'écriture n'est ouvert.*

## Problème

Pour un protocole kasmvnc, le registre de paramètres
(`operator/pkg/params`) est volontairement vide — la config ne transite
pas par userParams/guacd. Résultat : le dialogue de création
(`CreateWorkspaceDialog`), les réglages de connexion
(`ConnectionSettingsDialog`) et l'overlay de session affichaient le
message générique `portal.noTunableParams` (« aucun paramètre modifiable
sur ce modèle »). Trompeur : une config existe, s'applique réellement au
conteneur (readOnly, résolution, DLP…), elle est juste invisible.

## Deux valeurs différentes selon le contexte

1. **Création (workspace pas encore né)** : la seule valeur qui existe
   est le **texte brut de l'admin** (`template.kasmvncConfig`). Elle
   arrivait déjà au frontend sans filtrage (TemplateService.List ne
   prend pas d'Actor) — manque purement d'affichage.
2. **Workspace existant** : la valeur qui compte est le **contenu
   effectif fusionné** que l'opérateur matérialise dans la ConfigMap
   par-workspace (admin + couche clipboard dérivée de la
   WorkspacePolicy, `ensureKasmConfig`/`applyClipboardPolicy`). C'est ce
   fichier qui est monté dans le pod, pas le champ brut du template.

## Implémentation

### api-server — lecture de la ConfigMap effective

- `WorkspaceService.EffectiveKasmVNCConfig(ctx, actor, id)` : passe par
  `fetchByID` (même périmètre que `Get` : owner ou admin, 404 sans fuite
  d'existence), puis lit la ConfigMap à
  `{ns: ws.EffectiveTargetNamespace(), name: ws.EffectiveWorkloadName()}`,
  clé `kasmvnc.yaml`. **Aucune convention de nommage re-dérivée** : ce
  sont les mêmes méthodes exportées du CRD que l'opérateur utilise via
  `computeName`/`computeNamespace`. ConfigMap absente (template non
  kasmvnc, ou pas encore réconcilié) → 404 propre.
- Endpoint `GET /api/v1/workspaces/{id}/kasmvnc-config` → `{config}`.
  Un endpoint dédié plutôt qu'un champ sur la réponse workspace : la
  lecture de ConfigMap ne se paie que quand l'UI en a besoin, pas à
  chaque List/Get (polling 3–15 s du portail).
- Pas de mutation symétrique, volontairement.

### frontend — affichage read-only, jamais d'édition

- `KasmVNCConfigView` (ProtocolTabs.tsx) : bloc lecture seule (`<pre>`,
  couleurs à base d'opacité pour fonctionner dans les dialogs clairs ET
  l'overlay sombre), deux variantes de sous-titre : `template` (« votre
  policy s'y superposera au démarrage ») et `effective` (« la config
  réellement appliquée : modèle + policy »). Config vide → « les valeurs
  par défaut de l'image s'appliquent » (KasmVNC merge la config user
  par-dessus ses défauts, cf. Feature 12).
- `ProtocolParamsForm` : nouvelle prop `kasmvncConfig?: {content,
  variant}`. Si protocole kasmvnc **et** prop fournie → le viewer
  remplace `noTunableParams`. Prop absente → comportement inchangé
  (les machines distantes kasmvnc, RemoteWorkspaceDialog, gardent le
  message : leur config n'est pas gérée par WaaS). La clé i18n
  `noTunableParams` n'est pas réutilisée : nouvelles clés
  `portal.kasmvncManagedConfig*` (en/fr).
- Branchements :
  - `CreateWorkspaceDialog` → `template.kasmvncConfig` (brut, variant
    `template`) ;
  - `ConnectionSettingsDialog` → nouveau hook
    `useWorkspaceKasmVNCConfig` (effectif, variant `effective`) ;
  - `SessionOverlay` → même hook, fetch uniquement quand le panneau est
    ouvert sur une session kasmvnc in-cluster ; section dédiée au-dessus
    des paramètres de reconnexion.

## Tests

- api-server : `TestEffectiveKasmVNCConfig` — owner OK, admin OK, autre
  utilisateur = 404 (pas de fuite), workspace sans ConfigMap = 404
  propre, adressage par les helpers CRD.
- frontend : `ProtocolParamsForm.test.tsx` (contenu affiché à la place
  de `noTunableParams`, variante effective, état vide, cas remote
  inchangé) ; `SessionOverlay.test.tsx` (section affichée avec le
  contenu de l'endpoint sur kasmvnc, ni fetch ni section sur les
  protocoles guacd).
