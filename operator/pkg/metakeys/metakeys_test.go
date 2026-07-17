package metakeys

import "testing"

func TestCheckKey(t *testing.T) {
	denied := []string{
		"waas.xorhub.io/workspace",           // operator selector
		"waas.xorhub.io/owner",               // ownership
		"app.kubernetes.io/managed-by",       // *.kubernetes.io
		"pod-security.kubernetes.io/enforce", // PSA escalation
		"kubectl.kubernetes.io/default-container",
		"kubernetes.io/arch",
		"argocd.argoproj.io/sync-options", // GitOps ownership
		"sidecar.istio.io/inject",         // injector
		"linkerd.io/inject",
		"vault.hashicorp.com/agent-inject",
		"node.k8s.io/foo",
		"",
	}
	for _, key := range denied {
		if err := CheckKey(key); err == nil {
			t.Errorf("CheckKey(%q) must be denied", key)
		}
	}
	allowed := []string{
		"team",
		"cost-center",
		"example.com/team",
		"monitoring.grafana.com/scrape",
		"notkubernetes.io-lookalike", // no "/" → plain name, allowed
	}
	for _, key := range allowed {
		if err := CheckKey(key); err != nil {
			t.Errorf("CheckKey(%q): %v", key, err)
		}
	}
}

func TestCheckMapReportsFirstReservedKey(t *testing.T) {
	if err := Check(map[string]string{"team": "red", "cost-center": "42"}); err != nil {
		t.Fatalf("clean map rejected: %v", err)
	}
	if err := Check(map[string]string{"team": "red", "kubernetes.io/arch": "amd64"}); err == nil {
		t.Fatal("reserved key must fail the whole map")
	}
	if err := Check(nil); err != nil {
		t.Fatalf("empty map rejected: %v", err)
	}
}

func TestMergeAllowedEmptyStaysNil(t *testing.T) {
	// nil in, nil out — callers stamp the result straight into
	// ObjectMeta, where nil and {} serialize differently.
	if out := MergeAllowed(nil, nil); out != nil {
		t.Fatalf("MergeAllowed(nil, nil) = %v, want nil", out)
	}
	if out := MergeAllowed(map[string]string{}, nil); out != nil {
		t.Fatalf("MergeAllowed({}, nil) = %v, want nil", out)
	}
}

func TestMergeAllowedPlatformWins(t *testing.T) {
	out := MergeAllowed(
		map[string]string{
			"team":                     "red",
			"waas.xorhub.io/workspace": "spoofed", // silently dropped
		},
		map[string]string{"waas.xorhub.io/workspace": "real"},
	)
	if out["team"] != "red" {
		t.Fatalf("user key lost: %v", out)
	}
	if out["waas.xorhub.io/workspace"] != "real" {
		t.Fatalf("platform key must win: %v", out)
	}
}
