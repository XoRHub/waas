package envtest

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// These tests apply the COMMITTED CRD manifests (config/crd/bases) to a
// real apiserver and probe the schema constraints nothing else
// evaluates: CEL rules, enums, required fields, defaults. They are a
// behavioral drift check on the generated YAML — if `make manifests`
// ever stops emitting a rule, a test here goes red, not production.

func newNS(t *testing.T, name string) string {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: name + "-"}}
	if err := adminCli.Create(context.Background(), ns); err != nil {
		t.Fatalf("creating namespace: %v", err)
	}
	// No cleanup: envtest cannot finish namespace deletion (no
	// controller-manager) and the whole control plane dies with the run.
	return ns.Name
}

func TestWorkspaceImageCELOneOf(t *testing.T) {
	requireEnv(t)
	ns := newNS(t, "crd-cel")
	ctx := context.Background()

	base := func(name string) *waasv1alpha1.WorkspaceImage {
		return &waasv1alpha1.WorkspaceImage{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: waasv1alpha1.WorkspaceImageSpec{
				DisplayName: "Test",
				Protocols:   []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC},
			},
		}
	}

	both := base("both")
	both.Spec.Image = "docker.io/kasmweb/chrome:1.16.0"
	both.Spec.Registry = "docker.io/kasmweb"
	if err := adminCli.Create(ctx, both); err == nil || !strings.Contains(err.Error(), "exactly one of image or registry") {
		t.Fatalf("image+registry must be rejected by the CEL rule, got %v", err)
	}

	neither := base("neither")
	if err := adminCli.Create(ctx, neither); err == nil || !strings.Contains(err.Error(), "exactly one of image or registry") {
		t.Fatalf("neither image nor registry must be rejected by the CEL rule, got %v", err)
	}

	imageOnly := base("image-only")
	imageOnly.Spec.Image = "docker.io/kasmweb/chrome:1.16.0"
	if err := adminCli.Create(ctx, imageOnly); err != nil {
		t.Fatalf("exact-image entry must be admitted: %v", err)
	}
	registryOnly := base("registry-only")
	registryOnly.Spec.Registry = "docker.io/kasmweb"
	if err := adminCli.Create(ctx, registryOnly); err != nil {
		t.Fatalf("registry entry must be admitted: %v", err)
	}
}

func TestWorkspaceImageEnumsAndDefaults(t *testing.T) {
	requireEnv(t)
	ns := newNS(t, "crd-enums")
	ctx := context.Background()

	img := &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-tagpolicy", Namespace: ns},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "Test",
			Image:       "docker.io/kasmweb/chrome:1.16.0",
			Protocols:   []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC},
			TagPolicy:   "sometimes",
		},
	}
	if err := adminCli.Create(ctx, img); err == nil || !strings.Contains(err.Error(), "Unsupported value") {
		t.Fatalf("invalid tagPolicy must fail the enum, got %v", err)
	}

	img.Name, img.Spec.TagPolicy = "bad-protocol", ""
	img.Spec.Protocols = []waasv1alpha1.Protocol{"telnet"}
	if err := adminCli.Create(ctx, img); err == nil || !strings.Contains(err.Error(), "Unsupported value") {
		t.Fatalf("invalid protocol must fail the enum, got %v", err)
	}

	img.Name = "no-protocols"
	img.Spec.Protocols = nil
	if err := adminCli.Create(ctx, img); err == nil {
		t.Fatal("protocols is required (MinItems=1); creation must fail")
	}

	img.Name = "bad-arch"
	img.Spec.Protocols = []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC}
	img.Spec.Architectures = []string{"mips"}
	if err := adminCli.Create(ctx, img); err == nil || !strings.Contains(err.Error(), "Unsupported value") {
		t.Fatalf("invalid architecture must fail the items enum, got %v", err)
	}

	// enabled defaults to true — observable only when the field is
	// OMITTED on the wire, which the typed client never does for a
	// plain bool; go through unstructured.
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "waas.xorhub.io/v1alpha1",
		"kind":       "WorkspaceImage",
		"metadata":   map[string]any{"name": "defaulted", "namespace": ns},
		"spec": map[string]any{
			"displayName": "Test",
			"image":       "docker.io/kasmweb/chrome:1.16.0",
			"protocols":   []any{"kasmvnc"},
		},
	}}
	if err := adminCli.Create(ctx, u); err != nil {
		t.Fatalf("creating without enabled: %v", err)
	}
	got := &waasv1alpha1.WorkspaceImage{}
	if err := adminCli.Get(ctx, types.NamespacedName{Namespace: ns, Name: "defaulted"}, got); err != nil {
		t.Fatal(err)
	}
	if !got.Spec.Enabled {
		t.Fatal("enabled must default to true (kill switch is an explicit opt-out)")
	}
}

func TestWorkspaceRequiredFields(t *testing.T) {
	requireEnv(t)
	ns := newNS(t, "crd-ws")
	ctx := context.Background()

	// Both identity fields carry MinLength=1: an empty string must be as
	// invalid as an absent field.
	noOwner := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "no-owner", Namespace: ns},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce"},
	}
	if err := adminCli.Create(ctx, noOwner); err == nil {
		t.Fatal("empty owner must be rejected by the schema")
	}
	noTemplate := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "no-template", Namespace: ns},
		Spec:       waasv1alpha1.WorkspaceSpec{Owner: "alice"},
	}
	if err := adminCli.Create(ctx, noTemplate); err == nil {
		t.Fatal("empty templateRef must be rejected by the schema")
	}
}

func TestWorkspaceTemplateEnums(t *testing.T) {
	requireEnv(t)
	ns := newNS(t, "crd-tpl")
	ctx := context.Background()

	tpl := &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-os", Namespace: ns},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			DisplayName: "Test", OS: "beos",
			Image: "ghcr.io/xorhub/waas/desktop-xfce:1.0.0",
		},
	}
	if err := adminCli.Create(ctx, tpl); err == nil || !strings.Contains(err.Error(), "Unsupported value") {
		t.Fatalf("invalid os must fail the enum, got %v", err)
	}

	tpl.Name, tpl.Spec.OS = "bad-workload", waasv1alpha1.OSLinux
	tpl.Spec.Workload = &waasv1alpha1.WorkspaceWorkload{Kind: "DaemonSet"}
	if err := adminCli.Create(ctx, tpl); err == nil || !strings.Contains(err.Error(), "Unsupported value") {
		t.Fatalf("invalid workload kind must fail the enum, got %v", err)
	}

	tpl.Name = "bad-override"
	tpl.Spec.Workload = nil
	tpl.Spec.Overrides = &waasv1alpha1.TemplateOverrides{
		AllowedFields: []waasv1alpha1.OverridableField{"everything"},
	}
	if err := adminCli.Create(ctx, tpl); err == nil || !strings.Contains(err.Error(), "Unsupported value") {
		t.Fatalf("invalid overridable field must fail the enum, got %v", err)
	}
}
