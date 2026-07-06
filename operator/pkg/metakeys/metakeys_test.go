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
