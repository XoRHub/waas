package service

// The scaffold contract of the governance editors: a FRESHLY GENERATED
// scaffold must pass the corresponding upsert validation without any
// edit. This is the automated gate demanded after the "subjects: kind
// must be User or Group (got \"\")" class of bugs — placeholder elements
// inside collections can never come back silently.

import (
	"context"
	"reflect"
	"testing"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/xorhub/waas/api-server/internal/scaffold"
)

// roundTrip mimics the editor pipeline exactly: the YAML text is parsed
// and submitted as JSON to the PUT endpoint's input type.
func roundTrip[T any](t *testing.T, out *T) {
	t.Helper()
	raw, err := scaffold.YAML(reflect.TypeOf(*out))
	if err != nil {
		t.Fatalf("generating scaffold: %v", err)
	}
	jsonBytes, err := sigsyaml.YAMLToJSON([]byte(raw))
	if err != nil {
		t.Fatalf("scaffold is not valid YAML: %v\n%s", err, raw)
	}
	if err := sigsyaml.Unmarshal(jsonBytes, out); err != nil {
		t.Fatalf("scaffold does not round-trip into the input type: %v\n%s", err, raw)
	}
}

func TestFreshPolicyScaffoldPassesValidation(t *testing.T) {
	svc := newGovernanceFixture(t, nil, nil)
	var in UpsertPolicyInput
	roundTrip(t, &in)
	if _, err := svc.AdminUpsertPolicy(context.Background(), Actor{ID: "admin", Username: "admin"}, "scaffolded", in); err != nil {
		t.Fatalf("a fresh policy scaffold must pass validation unmodified: %v", err)
	}
}

func TestFreshImageScaffoldPassesValidation(t *testing.T) {
	svc := newGovernanceFixture(t, nil, nil)
	var in UpsertImageInput
	roundTrip(t, &in)
	if _, err := svc.AdminUpsertImage(context.Background(), Actor{ID: "admin", Username: "admin"}, "scaffolded", in); err != nil {
		t.Fatalf("a fresh image scaffold must pass validation unmodified: %v", err)
	}
}
