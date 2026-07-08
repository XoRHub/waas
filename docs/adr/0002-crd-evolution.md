# ADR 0002 — Stratégie d'évolution des CRDs

**Statut** : accepté (2026-07-08), stratégie « classique ».

1. **Tant que `v1alpha1`** : changements **additifs uniquement** —
   champs optionnels, enums extend-only (gardés par les tests
   d'exhaustivité : protocoles, overridable fields). Jamais de
   rename/retype in-place. Tout besoin breaking ⇒ nouvelle version.
2. **Passage à `v1beta1`** : déclenché par le premier utilisateur
   externe OU le premier besoin breaking. Mécanique : conversion
   webhook (controller-runtime), double-serve `v1alpha1`+`v1beta1`
   pendant au moins une release, bascule du storage version ensuite.
   **Prérequis : la suite envtest** (T10 du plan post-audit) — une
   conversion non testée contre un vrai apiserver n'est pas une
   conversion.
3. **Dépréciation** : marqueur `// +deprecated` sur le champ + mention
   release-notes ; retrait au plus tôt deux releases plus tard.
4. **Mécanique existante réutilisée** : CRDs générés par
   `make manifests` et drift-checkés en CI ; release-please porte
   l'annonce des changements de CRD dans les notes de release.
