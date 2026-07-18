package controller

// Template-driven metadata on the home PVC (spec.homeVolume): stamped at
// creation, re-synced on every reconcile so template edits — including
// key REMOVALS — propagate to already-provisioned volumes. The driving
// use case is Longhorn recurring-job enrollment, which is driven by
// labels on the PVC. Deliberately outside the pod-template fingerprint:
// enabling a backup never rolls a desktop.

import (
	"encoding/json"
	"sort"

	corev1 "k8s.io/api/core/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/metakeys"
)

// templateMetaLedger is the JSON shape of AnnotationTemplateMeta: the
// keys the operator stamped from the template, i.e. the only keys it is
// allowed to remove later. Admin-set keys never appear here.
type templateMetaLedger struct {
	Labels      []string `json:"labels,omitempty"`
	Annotations []string `json:"annotations,omitempty"`
}

// syncHomeVolumeMeta converges the home PVC's template-driven metadata:
// desired keys are stamped (denylist-filtered — platform keys can never
// be overwritten since the platform domain itself is denied), keys
// recorded in the ledger but absent from the template are removed, and
// the ledger annotation is rewritten (dropped when empty). Admin-set
// keys, never in the ledger, are left alone. Returns true when the PVC
// changed and needs an Update.
func syncHomeVolumeMeta(pvc *corev1.PersistentVolumeClaim, tpl *waasv1alpha1.WorkspaceTemplate) bool {
	var desiredLabels, desiredAnnotations map[string]string
	if tpl != nil && tpl.Spec.HomeVolume != nil {
		desiredLabels = tpl.Spec.HomeVolume.Labels
		desiredAnnotations = tpl.Spec.HomeVolume.Annotations
	}
	// Absent or invalid JSON = empty ledger: a corrupted ledger must
	// never fail the reconcile — it repairs itself when rewritten below.
	var ledger templateMetaLedger
	if raw, ok := pvc.Annotations[waasv1alpha1.AnnotationTemplateMeta]; ok {
		_ = json.Unmarshal([]byte(raw), &ledger)
	}

	changed := false
	pvc.Labels, ledger.Labels, changed = syncMetaSide(pvc.Labels, desiredLabels, ledger.Labels, changed)
	pvc.Annotations, ledger.Annotations, changed = syncMetaSide(pvc.Annotations, desiredAnnotations, ledger.Annotations, changed)

	prev, hadLedger := pvc.Annotations[waasv1alpha1.AnnotationTemplateMeta]
	if len(ledger.Labels) == 0 && len(ledger.Annotations) == 0 {
		if hadLedger {
			delete(pvc.Annotations, waasv1alpha1.AnnotationTemplateMeta)
			changed = true
		}
		return changed
	}
	next, _ := json.Marshal(ledger)
	if !hadLedger || prev != string(next) {
		if pvc.Annotations == nil {
			pvc.Annotations = map[string]string{}
		}
		pvc.Annotations[waasv1alpha1.AnnotationTemplateMeta] = string(next)
		changed = true
	}
	return changed
}

// syncMetaSide converges one metadata map (labels or annotations):
// ledgered keys gone from desired are removed, desired keys are
// stamped, and the new ledger (sorted, for a deterministic annotation)
// lists exactly what was stamped. Both directions re-filter through the
// denylist: a desired key the webhook should have refused is skipped,
// and a ledger entry naming a platform key (corrupted or hand-edited
// ledger) must never delete platform metadata.
func syncMetaSide(current, desired map[string]string, ledgered []string, changed bool) (map[string]string, []string, bool) {
	for _, key := range ledgered {
		if metakeys.CheckKey(key) != nil {
			continue
		}
		if _, still := desired[key]; still {
			continue
		}
		if _, present := current[key]; present {
			delete(current, key)
			changed = true
		}
	}
	stamped := make([]string, 0, len(desired))
	for key, value := range desired {
		if metakeys.CheckKey(key) != nil {
			continue
		}
		stamped = append(stamped, key)
		if cur, ok := current[key]; ok && cur == value {
			continue
		}
		if current == nil {
			current = map[string]string{}
		}
		current[key] = value
		changed = true
	}
	sort.Strings(stamped)
	return current, stamped, changed
}
