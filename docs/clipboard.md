# Copier-coller : chaîne complète et matrice attendue

## Chaîne (et où chaque maillon s'applique)

```
WorkspacePolicy.spec.clipboard ──► token de connexion (grant, signé)
        │                                   │
        ▼                                   ▼
capabilities du /connect            wwt ClipboardFilter (ENFORCEMENT :
(l'overlay AFFICHE, n'applique      drop des streams "clipboard" +
 jamais)                            toggles live clampés au grant)
                                            │
        navigateur ◄── flux guac ──► guacd ◄──► bureau (VNC/RDP/SSH)
            │
   DesktopPane (intégration client) :
   onclipboard → presse-papiers local ; paste/focus → createClipboardStream
```

- **Policy** : `clipboard.copyFromWorkspace` / `pasteToWorkspace` de la
  policy résolue. Fail-closed : pas de policy résolue = pas de clipboard.
  Le grant part dans le JWT de connexion — wwt applique, l'UI reflète.
- **guacd** : aucun paramètre de connexion à passer — le clipboard fait
  partie du protocole guac ; il n'existe **pas** de défaut restrictif
  côté guacd qui l'éteindrait (`disable-copy`/`disable-paste` sont bannis
  du registre côté plateforme, l'enforcement est dans wwt).
- **wwt** : `ClipboardFilter` droppe les streams de la direction refusée
  (+ ack d'erreur 771 côté collage) et traite les toggles live
  `waas-clipboard` de l'overlay, clampés au grant.
- **Client web** (le maillon qui manquait — cause du « rien ne marche sur
  aucun protocole ») : `DesktopPane` relaie désormais les deux sens :
  - bureau → local : `client.onclipboard` → `navigator.clipboard.writeText`
    (best-effort) + tampon exposé à l'overlay (échange manuel) ;
  - local → bureau : événement DOM `paste` (Ctrl+V dans le pane —
    fonctionne partout, HTTP compris) + relecture du presse-papiers
    système au focus de la fenêtre (Chromium + HTTPS), avec garde
    anti-écho (`lib/clipboard.ts`, testé).

## Contexte sécurisé : ce que le navigateur autorise

| Contexte | copier (bureau→local) | coller (local→bureau) |
|---|---|---|
| HTTPS + Chromium | automatique (`writeText`) | automatique au focus (`readText`, permission demandée) + Ctrl+V |
| HTTPS + Firefox | automatique (`writeText`) | Ctrl+V dans le pane (pas de `readText`) |
| HTTP (dev k3d) | **échange manuel de l'overlay uniquement** | Ctrl+V dans le pane |

L'overlay (Ctrl+Alt+M → Presse-papiers → Échange manuel) montre le dernier
texte reçu du bureau et permet d'en envoyer un — c'est le chemin de
vérification indépendant des permissions navigateur.

## Matrice attendue {protocole × direction × policy}

L'enforcement (wwt) est indépendant du protocole : le tableau policy vaut
pour VNC, RDP et SSH.

| Direction | Policy ✔ | Policy ✘ |
|---|---|---|
| Copier depuis le workspace | le texte copié dans le bureau arrive (auto ou overlay) | stream droppé par wwt ; toggle grisé 🔒 |
| Coller vers le workspace | Ctrl+V / focus-sync colle dans le bureau | stream refusé (ack 771) ; toggle grisé 🔒 |
| Toggle overlay OFF puis ON | coupe puis rétablit en live (≤ grant) | reste OFF : la réponse wwt reflète l'état effectif |

Réalité par protocole (côté bureau, images `waas-images`) :

- **VNC** : chemin recommandé — Xvnc gère le cut-buffer nativement, les
  deux sens fonctionnent.
- **RDP** : xrdp sans sesman (backend libvnc) ne monte pas toujours le
  canal cliprdr — limitation d'image documentée depuis waas-images ; le
  filtre wwt s'applique de la même façon quand le canal existe.
- **SSH** : le terminal est rendu par guacd, qui possède son propre
  presse-papiers de terminal — les deux sens passent par les mêmes
  streams guac, mêmes règles.

## Tests

- wwt : `wwt/internal/guac/clipboard_test.go` — les deux directions ×
  grant × toggles live (clamp), acks des streams refusés.
- frontend : `src/lib/clipboard.test.ts` — dédup, garde anti-écho,
  tampon du fallback manuel.
- vérification en session : overlay → Échange manuel (indépendant des
  permissions navigateur), sur une session par protocole après
  `make smoke`.
