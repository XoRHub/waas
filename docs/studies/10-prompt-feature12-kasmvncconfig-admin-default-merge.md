# Prompt Fable 5 — Feature 12 : `kasmvncConfig` éditable depuis le panel admin, avec défaut plateforme fusionné (l'explicite gagne)

Colle ce document tel quel comme prompt d'implémentation. Il part du
principe que tu (Fable 5) n'as aucun contexte de conversation
préalable.

## Contexte du repo

`WorkspaceTemplate.spec.kasmvncConfig` (`operator/api/v1alpha1/workspacetemplate_types.go:327-339`)
est une chaîne YAML opaque, jamais parsée comme schéma — matérialisée
en ConfigMap et montée en lecture seule à
`<homeMountPath>/.vnc/kasmvnc.yaml`
(`operator/internal/controller/kasm_config.go`). La Feature 11
(`docs/studies/08-prompt-feature11-kasmvnc-governance-gap.md`, livrée
2026-07-10) a ajouté une fusion partielle : le contrôleur stamp
inconditionnellement 3 clés DLP clipboard dérivées de
`WorkspacePolicy.Clipboard` par-dessus le `kasmvncConfig` de l'admin
(`applyClipboardPolicy`, `kasm_config.go:80-104`), et le webhook de
validation refuse qu'un admin écrive ces 3 clés lui-même
(`workspacetemplate_webhook.go:93-113,146-167`).

Ce prompt corrige deux écarts non traités par la Feature 11 :

1. **Aucun formulaire admin n'existe pour ce champ.** Le plumbing
   backend est complet de bout en bout — CRD → `TemplateInput`
   (`api-server/internal/service/template_service.go:53-55,220,364`) →
   `model.WorkspaceTemplate` (`api-server/internal/model/model.go:307-308`)
   → `types.gen.ts:357-359` — mais `frontend/src/pages/admin/TemplatesPage.tsx`
   ne référence `kasmvncConfig` **nulle part** : c'est un champ
   gitops/kubectl only aujourd'hui (confirmé dans
   `kasm-images-feasibility.md` ligne ~236, note de la Feature 11).
2. **Il n'existe aucun défaut plateforme.** Si l'admin laisse
   `kasmvncConfig` vide, le ConfigMap effectif ne contient QUE le bloc
   DLP clipboard des 3 clés stampées — aucune configuration de base
   (résolution, nom, etc.) n'est jamais appliquée. Le commentaire du
   champ CRD ment déjà sur ce point : *« Empty = no mount, the image
   default applies »* (`workspacetemplate_types.go:336`) — c'est faux
   depuis la Feature 11, le mount est **inconditionnel** dès qu'un
   protocole `kasmvnc` est déclaré (`workload.go:94-121`,
   `kasmConfig` n'est jamais `""` pour un template kasmvnc puisque le
   bloc clipboard y est toujours stampé). Ce prompt rend ce commentaire
   vrai en lui donnant un vrai contenu, au lieu de simplement le corriger.

## Ce qui existe déjà (à connaître avant de coder)

- **`applyClipboardPolicy(rawConfig, copyAllowed, pasteAllowed)`**
  (`kasm_config.go:85-104`) : parse `rawConfig` en `map[string]any`,
  stampe 3 chemins via `setNested` (`kasm_config.go:110-121`, écrase
  toute valeur non-map rencontrée sur le chemin), remarshal. Deux
  points d'appel indépendants font exactement le même calcul :
  `ensureKasmConfig` (`kasm_config.go:126-173`, produit le ConfigMap)
  et la construction du volume dans `workload.go:94-121` (calcule le
  hash qui roule le pod). Garde cette duplication en tête — la fusion à
  3 couches que tu introduis doit rester identique aux deux endroits.
- **Webhook** : `tpl.Spec.KasmVNCConfig != "" && !seen[kasmvnc]` → refus
  (`workspacetemplate_webhook.go:95-96`) ; `policyManagedClipboardKeys`
  (`:150-167`) refuse que l'admin écrive lui-même les 3 clés
  clipboard-DLP ou `runtime_configuration.allow_client_to_override_kasm_server_settings`.
  **Ne change pas cette logique** — le défaut plateforme introduit ici
  ne doit jamais, lui non plus, écrire ces clés autrement qu'à travers
  `applyClipboardPolicy` (sinon le webhook et le contrôleur divergent).
- **Montage utilisateur déjà read-only** : `workload.go`, le
  `VolumeMount` du ConfigMap a `ReadOnly: true` (subPath sur le volume
  home). C'est déjà l'état voulu par la demande *« côté utilisateur la
  ConfigMap n'est que RO, informative »* — ne le régresse pas, il n'y a
  rien à ajouter ici, juste à vérifier qu'aucune tâche ci-dessous n'introduit
  une écriture utilisateur (API ou UI) sur ce champ.
- **Pattern de champ protocole-conditionnel dans l'éditeur admin** :
  `exposeAudioPort` est déjà un champ conditionné à un protocole précis
  dans `TemplatesPage.tsx` (`currentProto.exposeAudioPort`,
  ligne ~491-492, passé à `ProtocolParamsForm`). `kasmvncConfig` est en
  revanche un champ **de template**, pas par-protocole (une seule
  chaîne dans `WorkspaceTemplateSpec`, pas dans `WorkspaceProtocol`) —
  le pattern à reprendre est donc la garde côté validation
  (`kasmvncConfig requires a kasmvnc protocol entry`), pas le mécanisme
  de state per-protocole d'`exposeAudioPort`.

## Ce qu'il faut livrer

### A. Une couche de défaut plateforme, fusionnée au reconcile — PAS persistée dans le CR

**Décidé** : le défaut vit comme une **constante Go**, appliquée
uniquement au moment où le contrôleur calcule le contenu effectif
(même endroit que le stamp clipboard actuel) — ni schéma CRD
(`+kubebuilder:default`), ni webhook mutant qui écrirait le défaut dans
`spec.KasmVNCConfig` à la création. Raison à documenter dans le commit :
un défaut matérialisé dans le CR (CRD ou webhook mutant) se fige dans
chaque template au moment de sa création — une évolution ultérieure du
défaut plateforme ne toucherait plus jamais les templates déjà créés,
et chaque template GitOps porterait le blob complet du défaut même
quand l'admin n'a rien à y redire, ce qui casse le modèle « le CR ne
porte que ce qui dévie ». La fusion au reconcile, elle, propage un
changement de défaut à tous les templates qui n'ont pas surchargé la
clé concernée, via le même mécanisme de hash/rollout qui existe déjà
(`annotationKasmConfigHash`).

1. **Décidé — source du contenu** : `defaultKasmVNCConfig` doit
   reproduire le `kasmvnc.yaml` par défaut **tel que livré par l'image
   kasmweb/\* elle-même**, pas une sélection inventée de directives. En
   session live (k3d dev, cf.
   `docs/studies/02-prompt-feature8-makefile-dev-bootstrap.md` pour le
   bootstrap), démarre un workspace kasmvnc et récupère le fichier de
   config par défaut du conteneur (probablement `/etc/kasmvnc/kasmvnc.yaml`
   et/ou `~/.vnc/kasmvnc.yaml` avant tout montage WaaS — vérifie lequel
   fait foi, la doc officielle citée en Feature 11
   (`kasmweb.com/kasmvnc/docs/latest/configuration.html`) documente la
   hiérarchie de fichiers si ambiguïté). Copie-le verbatim comme
   constante Go plutôt que de deviner ou de le réduire à un sous-ensemble
   « minimal » — c'est un fichier déjà validé par l'éditeur amont, le
   reproduire fidèlement est moins risqué que d'en extraire un extrait
   partiel. Le bloc clipboard-DLP qu'il contient éventuellement n'a pas
   besoin d'être retiré à la main : la couche 3 (policy) l'écrase de
   toute façon sur les 3 clés gérées (§2 ci-dessous), quel que soit son
   contenu dans la couche 1.
2. Remplace `applyClipboardPolicy` par une fusion à **3 couches**, dans
   cet ordre de priorité croissante :
   `defaultKasmVNCConfig` (base) → `tpl.Spec.KasmVNCConfig` de l'admin
   (surcharge **clé par clé**, pas un remplacement du document entier —
   une clé absente du template hérite du défaut, une clé présente dans
   les deux gagne côté template) → les 3 clés clipboard-DLP dérivées de
   la policy (toujours forcées en dernier, comportement inchangé).
3. Écris la fusion comme une fonction récursive générique sur
   `map[string]any` (deux maps en entrée, la seconde gagnant clé par
   clé en descendant dans les sous-maps ; toute valeur non-map — scalaire
   ou liste — dans la couche prioritaire remplace entièrement la valeur
   correspondante de la couche de base, pas de fusion de listes). Garde
   le style explicite déjà en place (`setNested`, pas de bibliothèque de
   deep-merge externe). Renomme/réorganise `applyClipboardPolicy` comme
   tu veux tant que les deux points d'appel (`kasm_config.go:143`,
   `workload.go:104`) utilisent la même fonction avec les 3 couches
   dans le même ordre.
4. Vérifie qu'un template dont `kasmvncConfig` est vide obtient
   désormais un ConfigMap contenant le défaut + le bloc clipboard (pas
   seulement le bloc clipboard comme aujourd'hui), et qu'un template
   qui ne surcharge qu'une seule clé du défaut (ex. juste la résolution)
   garde le reste du défaut intact — c'est le comportement que le
   commentaire actuel du champ CRD prétend déjà à tort.

### B. Le rollout doit suivre la routine de reload déjà en place — pas de nouveau mécanisme

Un changement de `kasmvncConfig` (ou du défaut plateforme introduit en
§A) doit rouler les pods concernés en suivant **exactement** le circuit
déjà câblé, sans en ajouter un parallèle :

1. `WorkspaceReconciler.SetupWithManager` regarde déjà
   `WorkspaceTemplate` (`workspace_controller.go:1040-1041`,
   `Watches(&waasv1alpha1.WorkspaceTemplate{}, ...mapTemplateToWorkspaces)`) :
   toute édition de template (donc de `kasmvncConfig`) réenqueue déjà
   chaque workspace qui en dérive (`mapTemplateToWorkspaces`,
   `workspace_controller.go:1057-1073`, filtre sur `spec.templateRef`).
2. Le contenu fusionné (§A) alimente déjà `annotationKasmConfigHash`
   sur le pod template (`workload.go`, juste après le calcul de
   `kasmConfig`) — un contenu différent change le hash, change
   l'annotation, donc change le pod template, donc déclenche le rollout
   du Deployment/StatefulSet par le mécanisme générique déjà en place
   (pas spécifique à kasmvnc).
3. **Ta tâche ici n'est donc PAS d'ajouter un watch ou un trigger** :
   c'est de vérifier, avec un test d'intégration/reconcile (ou en
   session live), que le passage à 3 couches (§A) n'a rien cassé dans
   cette chaîne — en particulier que le hash change bien quand seule la
   couche défaut change (ex. un bump futur de `defaultKasmVNCConfig`)
   ET quand seule la couche admin change, pas seulement quand la
   couche policy change (le seul cas déjà testé aujourd'hui). Si tu
   trouves que la fusion à 3 couches introduit un cas où le hash ne
   bouge pas alors que le contenu effectif a changé (ex. bug d'ordre
   des clés dans le YAML remarshalé produisant un hash instable), c'est
   un bug à corriger dans cette tâche, pas un nouveau mécanisme à
   construire.

### D. Champ éditable dans le panel admin (`TemplatesPage.tsx`)

1. Ajoute un `<textarea>` pour `kasmvncConfig` dans le formulaire de
   template, désactivé/masqué tant qu'aucun protocole `kasmvnc` n'est
   présent dans `protocols` (même garde que le webhook — ne dépend pas
   de `activeProto`, c'est un test sur la liste entière des protocoles
   du template, pas sur l'onglet actif).
2. **Décidé — contenu du texte d'aide** (i18n, `en.json`/`fr.json`,
   clés sous `admin.templatesPage.*` comme le reste du fichier). Il
   doit couvrir deux points, pas plus :
   - **Propagation** : ce champ est fusionné (clé par clé, le template
     gagne sur le défaut plateforme) puis propagé tel quel dans le
     workspace de l'utilisateur — matérialisé en ConfigMap et monté en
     lecture seule dans le conteneur ; les 3 clés clipboard-DLP restent
     refusées ici et dérivées de la policy (le webhook renvoie déjà un
     message explicite en cas de tentative — pas de validation
     dupliquée côté client, juste ne pas surprendre l'admin sur le
     retour d'erreur).
   - **Lien vers la doc** : référence la doc officielle KasmVNC des
     directives disponibles (`kasmweb.com/kasmvnc/docs/latest/configuration.html`,
     déjà citée en Feature 11) directement dans le texte d'aide ou en
     lien à côté du textarea, pour que l'admin sache où chercher les
     noms de clés valides sans deviner.
3. **Décidé — pas d'aperçu de fusion dans l'UI.** Le textarea reste
   l'édition de la couche de surcharge brute, pas un rendu de l'état
   fusionné final ; ce n'est pas demandé et ajouterait une surface (il
   faudrait appeler le contrôleur ou dupliquer la fusion côté
   api-server/frontend). Si tu identifies que c'est trivial une fois §A
   fait (ex. un endpoint qui expose juste le YAML fusionné en lecture),
   documente-le comme option plutôt que de l'implémenter sans qu'on te
   l'ait demandé.

### E. Corriger les commentaires devenus faux

- `workspacetemplate_types.go:336` : remplace *« Empty = no mount, the
  image default applies »* par une description fidèle à la fusion à 3
  couches introduite en §A (empty = le défaut plateforme + le stamp
  policy s'appliquent ; non-empty = surcharge clé par clé par-dessus le
  même défaut).
- `kasm_config.go:20-39` (commentaire de package en tête de fichier) :
  mentionne la 3ᵉ couche (défaut plateforme) en plus des deux déjà
  documentées (config admin + policy clipboard).

### F. Tests

- Go, sur la fonction de fusion : admin vide → défaut + clipboard
  seuls ; admin ne surchargeant qu'une clé → reste du défaut préservé ;
  admin définissant une clé absente du défaut → conservée telle quelle ;
  ordre de priorité vérifié sur un cas où défaut ET admin définissent la
  même clé (l'admin gagne) puis où policy ET admin visent la même clé
  clipboard (déjà interdit à l'admission par le webhook — teste que la
  fusion au reconcile reste défensivement correcte si jamais un objet
  invalide existait déjà en base avant un durcissement du webhook,
  sans en faire une garantie API nouvelle).
- Go : les deux points d'appel (`ensureKasmConfig`,
  construction du volume dans `workload.go`) produisent un contenu
  identique pour les mêmes entrées (évite la régression de duplication
  citée en “Ce qui existe déjà”).
- Go, sur §B (rollout) : un changement de `defaultKasmVNCConfig` seul
  (simulable en test en injectant une variante) et un changement de
  `tpl.Spec.KasmVNCConfig` seul produisent chacun un
  `annotationKasmConfigHash` différent — pas seulement un changement de
  policy clipboard, seul cas déjà couvert avant ce prompt.
- Vitest : le textarea `kasmvncConfig` apparaît/disparaît selon la
  présence du protocole `kasmvnc` dans la liste ; round-trip
  save/reload du champ ; clés i18n présentes dans les deux locales.

## Contraintes à respecter

- Le défaut plateforme n'est **jamais** persisté dans
  `spec.KasmVNCConfig` — ni par un webhook mutant, ni par un défaut de
  schéma CRD. C'est l'arbitrage central de ce prompt, ne le relitige pas.
- Les 3 clés clipboard-DLP restent interdites à l'admin dans
  `kasmvncConfig` (webhook inchangé) et restent stampées en dernier,
  après le défaut ET après la surcharge admin.
- Ne touche pas au chemin guacd — ce prompt est strictement kasmvnc.
- `go build ./...` + tests Go sur `operator` (et `api-server` si tu
  touches au DTO, ce qui ne devrait pas être nécessaire — le plumbing
  y est déjà complet) ; `tsc -b` + tests vitest sur le frontend.
- i18n complet (`en.json` et `fr.json`) pour toute nouvelle chaîne.
- Régénère `docs/guacd-parameters.md` seulement si tu y touches
  (a priori non concerné par ce prompt, `kasmvncConfig` reste hors du
  registre `params.go`).

## Points ouverts (ton arbitrage)

Les arbitrages de fond sont tranchés ci-dessus (source du défaut,
priorité de fusion, non-persistance dans le CR, routine de rollout
réutilisée, contenu du texte d'aide). Il ne reste qu'un choix
d'implémentation sans enjeu de comportement :
- Nom exact de la fonction de fusion à 3 couches et emplacement du
  fichier (`kasm_config.go` vs nouveau `kasm_defaults.go`) — choisis ce
  qui te semble le plus cohérent avec l'organisation actuelle du
  package `controller` (a priori : nouveau fichier si la constante
  `defaultKasmVNCConfig` est volumineuse, sinon reste dans
  `kasm_config.go` à côté de la fonction qu'elle nourrit).
