package policy

// The override-rights registry: the ONE place tying the Workspace spec
// surface to the OverridableField rights of templates and policies.
// Enforcement (CheckOverrides) derives what a workspace USES from the
// claims table below by reflection, so it can never drift from it; the
// exhaustiveness test (overrides_registry_test.go) fails whenever a new
// WorkspaceOverrides field or governed spec field is neither claimed here
// nor explicitly exempted — every future spec evolution must decide its
// governance explicitly instead of becoming silently ungoverned.

import (
	"reflect"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// overrideClaims maps every WorkspaceOverrides JSON field onto the right
// its use consumes. Two JSON fields may share one right (volumes +
// volumeMounts, labels + annotations).
var overrideClaims = map[string]waasv1alpha1.OverridableField{
	"env":                waasv1alpha1.FieldEnv,
	"securityContext":    waasv1alpha1.FieldSecurityContext,
	"podSecurityContext": waasv1alpha1.FieldPodSecurityContext,
	"volumes":            waasv1alpha1.FieldVolumes,
	"volumeMounts":       waasv1alpha1.FieldVolumes,
	"nodeSelector":       waasv1alpha1.FieldNodeSelector,
	"tolerations":        waasv1alpha1.FieldTolerations,
	"protocol":           waasv1alpha1.FieldProtocol,
	"schedule":           waasv1alpha1.FieldSchedule,
	"labels":             waasv1alpha1.FieldMetadata,
	"annotations":        waasv1alpha1.FieldMetadata,
}

// specClaims are the governed inputs living directly on WorkspaceSpec
// (not under .overrides). Their usage tests are special-cased in
// CheckOverrides (placement compares against the resolved default;
// resources is non-nil = override) but the CLAIM is declared here so the
// exhaustiveness test sees one complete picture.
var specClaims = map[string]waasv1alpha1.OverridableField{
	"targetNamespace": waasv1alpha1.FieldPlacement,
	"resources":       waasv1alpha1.FieldResources,
}

// specExempt are WorkspaceSpec fields that deliberately consume NO
// override right, each with the reason. The exhaustiveness test rejects
// any spec field that is neither claimed nor listed here.
var specExempt = map[string]string{
	"templateRef":    "selects the template, it does not deviate from it",
	"owner":          "trusted identity, immutable (webhook)",
	"displayName":    "cosmetic only, no workload impact",
	"paused":         "lifecycle action; pausing frees compute and is never policy-gated",
	"workloadName":   "frozen naming derived by the api-server, immutable (webhook)",
	"homeVolumeName": "volume adoption, ownership-checked by the webhook for every caller",
	"overrides":      "container of the claimed fields above",
}

// connectTimeRights are rights consumed outside the Workspace spec, at
// session time; the api-server enforces them where the input arrives.
var connectTimeRights = map[waasv1alpha1.OverridableField]string{
	waasv1alpha1.FieldProtocolParams: "connect-time guacd parameter tweaks, enforced by the api-server on /connect (template userParams stays the fine-grained filter)",
}

// overridesUsage derives, by reflection over the claims table, which
// rights ws.Spec.Overrides actually consumes. A field counts as used
// when it is non-empty (nil-or-empty collections and empty strings are
// "not set" — an explicit empty list does not consume a right).
func overridesUsage(ov *waasv1alpha1.WorkspaceOverrides) map[waasv1alpha1.OverridableField]bool {
	used := map[waasv1alpha1.OverridableField]bool{}
	if ov == nil {
		return used
	}
	v := reflect.ValueOf(*ov)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field, ok := overrideClaims[jsonName(t.Field(i))]
		if !ok {
			// Unclaimed fields are a bug caught by the exhaustiveness
			// test; skipping here keeps enforcement fail-closed at build
			// time rather than panicking at admission time.
			continue
		}
		if valueUsed(v.Field(i)) {
			used[field] = true
		}
	}
	return used
}

// jsonName extracts the JSON field name of a struct field ("env" from
// `json:"env,omitempty"`).
func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			return tag[:i]
		}
	}
	return tag
}

// valueUsed reports whether a spec field is actually set: non-empty
// collection, non-nil pointer, non-empty string.
func valueUsed(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.Len() > 0
	case reflect.Pointer, reflect.Interface:
		return !v.IsNil()
	case reflect.String:
		return v.String() != ""
	default:
		return !v.IsZero()
	}
}
