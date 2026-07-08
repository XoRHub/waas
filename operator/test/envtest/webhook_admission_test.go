package envtest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// End-to-end admission: the request travels client → apiserver →
// ValidatingWebhookConfiguration → the operator's webhook server. One
// accept and a few representative rejects per webhook — the validation
// matrix itself is covered by the unit tests in internal/webhook.

// seedGovernance creates the trio a workspace admission needs in ns:
// an enabled catalog entry, a template using it, and a subjects-less
// (match-everyone) policy.
func seedGovernance(t *testing.T, ns string) {
	t.Helper()
	ctx := context.Background()
	img := &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: ns},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "XFCE",
			Image:       "ghcr.io/xorhub/waas/desktop-xfce:1.0.0",
			Protocols:   []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC},
			Enabled:     true,
		},
	}
	tpl := &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: ns},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			DisplayName: "XFCE Desktop",
			OS:          waasv1alpha1.OSLinux,
			Image:       "ghcr.io/xorhub/waas/desktop-xfce:1.0.0",
			// Without protocols the template defaults to vnc, which the
			// image above does not serve — the webhook (rightly) denies.
			Protocols: []waasv1alpha1.WorkspaceProtocol{{Name: "kasmvnc", Port: 6901, Default: true}},
		},
	}
	pol := &waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: ns},
		Spec:       waasv1alpha1.WorkspacePolicySpec{},
	}
	if err := adminCli.Create(ctx, img); err != nil {
		t.Fatalf("seeding image: %v", err)
	}
	if err := adminCli.Create(ctx, tpl); err != nil {
		t.Fatalf("seeding template: %v", err)
	}
	if err := adminCli.Create(ctx, pol); err != nil {
		t.Fatalf("seeding policy: %v", err)
	}
}

func TestTemplateWebhookThroughAPIServer(t *testing.T) {
	requireEnv(t)
	ns := newNS(t, "wh-tpl")
	ctx := context.Background()

	twoDefaults := &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "two-defaults", Namespace: ns},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			DisplayName: "Broken", OS: waasv1alpha1.OSLinux,
			Image: "ghcr.io/xorhub/waas/desktop-xfce:1.0.0",
			Protocols: []waasv1alpha1.WorkspaceProtocol{
				{Name: "kasmvnc", Port: 6901, Default: true},
				{Name: "ssh", Port: 22, Default: true},
			},
		},
	}
	if err := adminCli.Create(ctx, twoDefaults); err == nil || !strings.Contains(err.Error(), "at most one protocol may be marked default") {
		t.Fatalf("two default protocols must be denied by the webhook, got %v", err)
	}

	valid := twoDefaults.DeepCopy()
	valid.Name, valid.ResourceVersion = "valid", ""
	valid.Spec.Protocols[1].Default = false
	if err := adminCli.Create(ctx, valid); err != nil {
		t.Fatalf("valid template must be admitted: %v", err)
	}
}

func TestWorkspaceWebhookIdentityModel(t *testing.T) {
	requireEnv(t)
	ns := newNS(t, "wh-identity")
	seedGovernance(t, ns)
	ctx := context.Background()

	ws := func(name, owner string) *waasv1alpha1.Workspace {
		return &waasv1alpha1.Workspace{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: owner},
		}
	}

	// An untrusted caller may not stamp identity annotations …
	selfGranted := ws("self-granted", "alice")
	selfGranted.Annotations = map[string]string{waasv1alpha1.AnnotationUsername: "root"}
	if err := aliceCli.Create(ctx, selfGranted); err == nil || !strings.Contains(err.Error(), "trusted writer") {
		t.Fatalf("self-granted identity must be denied, got %v", err)
	}
	// … nor claim someone else's ownership.
	if err := aliceCli.Create(ctx, ws("stolen", "bob")); err == nil || !strings.Contains(err.Error(), "does not match your authenticated identity") {
		t.Fatalf("owner != caller must be denied, got %v", err)
	}

	// Honest self-service create passes the whole chain (identity,
	// policy, catalog, tag discipline).
	honest := ws("honest", "alice")
	if err := aliceCli.Create(ctx, honest); err != nil {
		t.Fatalf("honest create must be admitted: %v", err)
	}

	// spec.owner is immutable — a workspace cannot change hands. The
	// running reconciler mutates the object too (finalizer, status), so
	// retry across optimistic-concurrency conflicts until the WEBHOOK
	// answers.
	waitFor(t, 10*time.Second, "owner-mutation denial", func() error {
		got := &waasv1alpha1.Workspace{}
		if err := adminCli.Get(ctx, types.NamespacedName{Namespace: ns, Name: "honest"}, got); err != nil {
			return err
		}
		got.Spec.Owner = "mallory"
		err := aliceCli.Update(ctx, got)
		if err == nil {
			t.Fatal("owner mutation must be denied, but was admitted")
		}
		if !strings.Contains(err.Error(), "immutable") {
			return fmt.Errorf("not the webhook denial yet: %w", err)
		}
		return nil
	})

	// The trusted writer (platform api-server) may act for other users
	// and stamp their identity.
	brokered := ws("brokered", "bob-uuid")
	brokered.Annotations = map[string]string{
		waasv1alpha1.AnnotationUsername: "bob",
		waasv1alpha1.AnnotationGroups:   "devs",
	}
	if err := trustedCli.Create(ctx, brokered); err != nil {
		t.Fatalf("trusted-writer create must be admitted: %v", err)
	}
}

func TestWorkspaceWebhookFailsClosedWithoutPolicy(t *testing.T) {
	requireEnv(t)
	ns := newNS(t, "wh-nopolicy")
	ctx := context.Background()

	// Template and catalog exist, but NO WorkspacePolicy: the platform
	// denies rather than defaulting open.
	img := &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: ns},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "XFCE", Image: "ghcr.io/xorhub/waas/desktop-xfce:1.0.0",
			Protocols: []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC}, Enabled: true,
		},
	}
	tpl := &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: ns},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			DisplayName: "XFCE", OS: waasv1alpha1.OSLinux,
			Image: "ghcr.io/xorhub/waas/desktop-xfce:1.0.0",
		},
	}
	if err := adminCli.Create(ctx, img); err != nil {
		t.Fatal(err)
	}
	if err := adminCli.Create(ctx, tpl); err != nil {
		t.Fatal(err)
	}

	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "orphan", Namespace: ns},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: "alice"},
	}
	if err := aliceCli.Create(ctx, ws); err == nil || !strings.Contains(err.Error(), "no WorkspacePolicy matches") {
		t.Fatalf("policy-less create must fail closed, got %v", err)
	}
}
