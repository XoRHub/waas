# Prompt Fable 5 — Feature 9 : la barre "Leave Session" bloque le menu Applications XFCE en haut de l'écran

Colle ce document tel quel comme prompt d'implémentation. Il part du principe que tu (Fable 5) n'as aucun contexte de conversation préalable.

## Contexte du repo

Dans un workspace VNC exécutant un bureau XFCE (image Ubuntu), le panneau XFCE affiche son menu "Applications" en haut à gauche de l'écran distant. Ce menu est actuellement impossible à cliquer : le rectangle de survol/clic de la barre "Leave Session" du frontend WaaS s'étend sur toute la largeur de l'écran en haut, et intercepte les clics même là où rien n'est visuellement affiché.

## Ce qui existe déjà (bug localisé précisément)

Le composant en cause n'est **pas** `SessionOverlay.tsx` (qui est le menu réglages en bas à droite, `absolute bottom-4 right-4`, un petit bouton 36×36 — sans rapport avec ce bug). Le vrai coupable est la barre "Leave Session" rendue inline dans `DesktopView`, `frontend/src/pages/ConnectPage.tsx:216-224` :

```tsx
{state === 'connected' && (
  <div className="group absolute inset-x-0 top-0 z-10 flex justify-center">
    <div className="absolute top-0 h-2 w-40 rounded-b-md bg-white/20 transition group-hover:opacity-0" />
    <div className="-translate-y-full rounded-b-lg bg-slate-900/90 px-4 py-2 text-sm text-white shadow-lg backdrop-blur transition-transform duration-200 group-hover:translate-y-0">
      <button onClick={leave} className="font-medium text-blue-400 hover:text-blue-300">
        {t('connect.leave')}
      </button>
    </div>
  </div>
)}
```

**Cause racine** : le `<div>` externe a `absolute inset-x-0 top-0` — il s'étend donc sur **toute la largeur du viewport**, collé en haut. Son enfant (le label "Leave Session") n'est déplacé hors champ que par un `transform: translateY(-100%)` (`-translate-y-full`), qui est un effet **de peinture uniquement** — il ne retire pas l'élément du flux ni ne réduit la boîte de hit-test du parent. Résultat : le wrapper externe garde une zone cliquable pleine largeur, d'une hauteur ~36-40px (celle du label), collée en haut de l'écran, **même quand le label est visuellement hors champ et que seul le petit pull-tab centré (`w-40`, 160px) est visible**. Comme ce wrapper n'a pas `pointer-events-none`, il vaut `pointer-events: auto` par défaut et intercepte tous les clics sur toute cette bande — y compris le coin en haut à gauche où vit le menu Applications XFCE. `z-10` le place au-dessus de `DesktopPane` (aucun `z-index`/`pointer-events-none` contre-mesure trouvé dans ce composant).

**Historique** : cette structure (wrapper `absolute inset-x-0 top-0` + label caché par transform) a été introduite au commit `01496d16705a` ("split view, workspace folders, protocol settings and theme toggle") quand `ConnectPage` est devenu un wrapper léger autour du nouveau `DesktopPane`, et n'a jamais été retouchée depuis (`3f7f25053652`, `c99623d0e4ce`, `f8558abf685d`). Elle n'a jamais été scopée par type de session — elle s'applique identiquement à toutes les sessions in-cluster (kasmvnc/remote en sont exclus par ailleurs pour d'autres raisons, mais pas celle-ci).

## Ce qu'il faut livrer

Corrige la zone de hit-test pour qu'elle corresponde à la zone réellement visible/interactive :

1. Ajoute `pointer-events-none` sur le `<div>` externe (`absolute inset-x-0 top-0`, ligne 217).
2. Ajoute `pointer-events-auto` explicitement sur le(s) élément(s) interne(s) réellement cliquables/survolables — a minima le petit pull-tab visuel (ligne 218) pour capter le survol qui déclenche `group-hover`, et le bloc label+bouton (ligne 219-223) pour que le bouton "Leave Session" reste cliquable une fois affiché.
3. Vérifie après le fix que :
   - le survol du pull-tab (les 160px centrés en haut) fait toujours apparaître le label et reste cliquable,
   - le bouton "Leave Session" reste cliquable quand le label est visible,
   - un clic n'importe où ailleurs sur la bande du haut (notamment le coin gauche et le coin droit) traverse désormais vers le contenu distant (XFCE, ou tout autre bureau) sans interception.

## Contraintes à respecter

- Fix CSS/Tailwind ciblé sur ce composant (`ConnectPage.tsx:216-224`) — ne touche pas `SessionOverlay.tsx` (composant différent, sans rapport avec ce bug).
- Le comportement de hover/transition existant (le pull-tab qui laisse apparaître le label au survol) doit rester identique après le fix — c'est une correction de zone de clic, pas un changement de design.
- Ajoute un test (vitest + testing-library si la convention du repo le permet pour ce genre de layout, ou a minima un test qui vérifie la présence de `pointer-events-none` sur le wrapper externe et `pointer-events-auto` sur les éléments interactifs internes, pour éviter une régression silencieuse si quelqu'un retouche ce JSX plus tard).
- Teste manuellement sur l'environnement de dev k3d (`make dev-up dev-build dev-load dev-deploy`, un workspace VNC XFCE réel) que le menu Applications en haut à gauche redevient cliquable — ce bug n'est pas détectable par un test unitaire seul puisqu'il dépend de la superposition avec un contenu distant réel affiché par le canvas Guacamole.

## Points ouverts (ton arbitrage)

- Faut-il aussi restreindre `w-40`/la largeur du pull-tab visuel lui-même, ou seulement corriger `pointer-events` sur le wrapper (ce qui suffit déjà à débloquer le clic ailleurs, sans changer le design visuel) — recommandation : ne change que `pointer-events`, le pull-tab visuel centré reste inchangé.
