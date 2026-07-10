package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// nestedBool reads a nested boolean out of a kasmvnc.yaml document, failing
// the test when the path is absent or the leaf is not a bool.
func nestedBool(t *testing.T, content string, keys ...string) bool {
	t.Helper()
	root := map[string]any{}
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		t.Fatalf("parsing effective kasmvnc config: %v\n%s", err, content)
	}
	var cur any = root
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v missing at %q in:\n%s", keys, k, content)
		}
		cur, ok = m[k]
		if !ok {
			t.Fatalf("path %v missing key %q in:\n%s", keys, k, content)
		}
	}
	b, ok := cur.(bool)
	if !ok {
		t.Fatalf("path %v is not a bool (%T) in:\n%s", keys, cur, content)
	}
	return b
}

func clipboardEnabled(t *testing.T, content string) (copyEnabled, pasteEnabled, clientOverride bool) {
	t.Helper()
	return nestedBool(t, content, "data_loss_prevention", "clipboard", "server_to_client", "enabled"),
		nestedBool(t, content, "data_loss_prevention", "clipboard", "client_to_server", "enabled"),
		nestedBool(t, content, "runtime_configuration", "allow_client_to_override_kasm_server_settings")
}

// TestKasmClipboardPolicyEnforcement is the unit-level contract of the
// merge: the policy decision lands on the DLP directives regardless of
// what the admin's opaque config said, the client override is always
// forced off, and unrelated admin directives survive.
func TestKasmClipboardPolicyEnforcement(t *testing.T) {
	// Admin config that (naively) tries to enable clipboard and let the
	// client override the server — a denying policy must win over both.
	admin := "" +
		"desktop:\n  resolution:\n    width: 1024\n" +
		"data_loss_prevention:\n  clipboard:\n" +
		"    server_to_client:\n      enabled: true\n" +
		"    client_to_server:\n      enabled: true\n" +
		"runtime_configuration:\n  allow_client_to_override_kasm_server_settings: true\n"

	deny, err := applyClipboardPolicy(admin, false, false)
	if err != nil {
		t.Fatalf("applyClipboardPolicy(deny): %v", err)
	}
	copyE, pasteE, override := clipboardEnabled(t, deny)
	if copyE || pasteE {
		t.Fatalf("denying policy must disable clipboard, got copy=%v paste=%v", copyE, pasteE)
	}
	if override {
		t.Fatal("client override must be forced off so the DLP directives take effect")
	}
	// Unrelated admin directive preserved.
	if got := nestedFloat(t, deny, "desktop", "resolution", "width"); got != 1024 {
		t.Fatalf("admin directive must survive the merge, got width=%v", got)
	}

	// Asymmetric grant: copy allowed, paste denied.
	mixed, err := applyClipboardPolicy(admin, true, false)
	if err != nil {
		t.Fatalf("applyClipboardPolicy(mixed): %v", err)
	}
	copyE, pasteE, override = clipboardEnabled(t, mixed)
	if !copyE || pasteE || override {
		t.Fatalf("expected copy=true paste=false override=false, got %v/%v/%v", copyE, pasteE, override)
	}

	// Empty admin config still yields a complete enforcement document.
	empty, err := applyClipboardPolicy("", true, true)
	if err != nil {
		t.Fatalf("applyClipboardPolicy(empty): %v", err)
	}
	copyE, pasteE, override = clipboardEnabled(t, empty)
	if !copyE || !pasteE || override {
		t.Fatalf("allowing policy over empty config: got copy=%v paste=%v override=%v", copyE, pasteE, override)
	}
}

func nestedFloat(t *testing.T, content string, keys ...string) float64 {
	t.Helper()
	root := map[string]any{}
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		t.Fatalf("parsing: %v", err)
	}
	var cur any = root
	for _, k := range keys {
		cur = cur.(map[string]any)[k]
	}
	f, ok := cur.(float64)
	if !ok {
		t.Fatalf("path %v not a number (%T)", keys, cur)
	}
	return f
}

// TestKasmConfigDeniesClipboardWithoutPolicy verifies the fail-closed
// reconcile path: no matching policy → clipboard off in the materialized
// ConfigMap, and the pod carries the config hash (rollout trigger).
func TestKasmConfigDeniesClipboardWithoutPolicy(t *testing.T) {
	tpl := kasmTemplate()
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, cm); err != nil {
		t.Fatalf("expected the kasmvnc ConfigMap even without admin config: %v", err)
	}
	copyE, pasteE, override := clipboardEnabled(t, cm.Data[kasmConfigKey])
	if copyE || pasteE || override {
		t.Fatalf("no policy must fail closed: copy=%v paste=%v override=%v", copyE, pasteE, override)
	}

	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	if dep.Spec.Template.Annotations[annotationKasmConfigHash] == "" {
		t.Fatal("expected the config hash on the pod template")
	}
}

// TestKasmConfigHashMatchesConfigMap guards the duplication between the
// two independent call sites that both compute the effective content:
// ensureKasmConfig writes it into the ConfigMap, buildPodTemplate hashes
// it onto the pod. If they ever diverge (different merge order, key
// sorting, layers), the pod would carry a hash that doesn't match the
// mounted file and the rollout signal would be wrong. They must agree for
// the same inputs, with and without an admin override.
func TestKasmConfigHashMatchesConfigMap(t *testing.T) {
	for _, admin := range []string{"", "desktop:\n  resolution:\n    width: 1600\n"} {
		tpl := kasmTemplate()
		tpl.Spec.KasmVNCConfig = admin
		ws := workspace()
		ws.Spec.TemplateRef = "kasm-firefox"
		r, c := newFixture(t, tpl, ws)
		ctx := context.Background()

		reconcile(t, r, ws)

		cm := &corev1.ConfigMap{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, cm); err != nil {
			t.Fatalf("admin=%q: %v", admin, err)
		}
		dep := &appsv1.Deployment{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
			t.Fatalf("admin=%q: %v", admin, err)
		}
		want := kasmConfigHash(cm.Data[kasmConfigKey])
		if got := dep.Spec.Template.Annotations[annotationKasmConfigHash]; got != want {
			t.Fatalf("admin=%q: pod hash %q != hash of mounted config %q — call sites diverged", admin, got, want)
		}
	}
}

// TestKasmConfigAbsentForNonKasmWorkspace: a guacd template must not get a
// kasmvnc.yaml ConfigMap or mount.
func TestKasmConfigAbsentForNonKasmWorkspace(t *testing.T) {
	ws := workspace() // default template is the guacd xfce one
	r, c := newFixture(t, linuxTemplate(), ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, cm); err == nil {
		t.Fatal("a non-kasmvnc workspace must not get a kasmvnc ConfigMap")
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == kasmConfigVolume || v.Name == kasmVncDirVolume {
			t.Fatalf("non-kasmvnc workspace must not mount kasmvnc volumes, found %q", v.Name)
		}
	}
}

// TestKasmVncDirNeverAutoCreatedOnHome is the non-regression test for the
// self.pem boot loop: the subPath parent <home>/.vnc must be an
// operator-managed emptyDir, never a directory the kubelet auto-creates on
// the home PVC (that auto-creation lands root:root 0755, and the non-root
// desktop user can no longer write self.pem there — reproduced live on
// local-path, where fsGroup would not have applied either).
func TestKasmVncDirNeverAutoCreatedOnHome(t *testing.T) {
	tpl := kasmTemplate()
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	var dir *corev1.Volume
	for i := range dep.Spec.Template.Spec.Volumes {
		if dep.Spec.Template.Spec.Volumes[i].Name == kasmVncDirVolume {
			dir = &dep.Spec.Template.Spec.Volumes[i]
		}
	}
	if dir == nil || dir.EmptyDir == nil {
		t.Fatalf("expected an emptyDir volume %q for .vnc, got %+v", kasmVncDirVolume, dir)
	}

	mounts := dep.Spec.Template.Spec.Containers[0].VolumeMounts
	dirIdx, fileIdx := -1, -1
	for i, m := range mounts {
		switch m.Name {
		case kasmVncDirVolume:
			dirIdx = i
			if m.MountPath != "/home/kasm-user/.vnc" {
				t.Fatalf(".vnc dir must mount at <home>/.vnc, got %q", m.MountPath)
			}
			if m.ReadOnly {
				t.Fatal(".vnc dir must stay writable for KasmVNC runtime artifacts (self.pem)")
			}
		case kasmConfigVolume:
			fileIdx = i
		}
	}
	if dirIdx < 0 || fileIdx < 0 {
		t.Fatalf("expected both the .vnc dir and the config file mounts, got %+v", mounts)
	}
	// Nested mounts: the directory must precede the file bind-mounted
	// inside it, so the subPath parent is always the emptyDir — the
	// kubelet never creates .vnc on the home volume.
	if dirIdx > fileIdx {
		t.Fatalf(".vnc dir mount (idx %d) must precede the config file mount (idx %d)", dirIdx, fileIdx)
	}
}

func TestKasmConfigBoundaryConvergence(t *testing.T) {
	tpl := kasmTemplate()
	tpl.Spec.KasmVNCConfig = "desktop:\n  resolution:\n    width: 1024\n"
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, cm); err != nil {
		t.Fatalf("expected the kasmvnc ConfigMap: %v", err)
	}
	// Effective content = admin directive + policy clipboard enforcement.
	if got := nestedFloat(t, cm.Data[kasmConfigKey], "desktop", "resolution", "width"); got != 1024 {
		t.Fatalf("ConfigMap must carry the admin directive, got width=%v", got)
	}
	clipboardEnabled(t, cm.Data[kasmConfigKey]) // asserts the DLP block exists

	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	container := dep.Spec.Template.Spec.Containers[0]
	var mount *corev1.VolumeMount
	for i := range container.VolumeMounts {
		if container.VolumeMounts[i].Name == kasmConfigVolume {
			mount = &container.VolumeMounts[i]
		}
	}
	if mount == nil {
		t.Fatal("expected the kasmvnc config mount")
	}
	if mount.MountPath != "/home/kasm-user/.vnc/kasmvnc.yaml" || mount.SubPath != kasmConfigKey || !mount.ReadOnly {
		t.Fatalf("single-file read-only subPath mount expected, got %+v", mount)
	}
	hash := dep.Spec.Template.Annotations[annotationKasmConfigHash]
	if hash == "" {
		t.Fatal("expected the config hash on the pod template (rollout trigger)")
	}

	// Content change MID-SESSION (docs/adr/0001): the ConfigMap converges
	// but the running workload does NOT roll — the workspace reports
	// TemplateDrifted instead.
	tpl.Spec.KasmVNCConfig = "desktop:\n  resolution:\n    width: 2560\n"
	if err := c.Update(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	// Make the workload report running so the boundary rule applies.
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	one := int32(1)
	dep.Spec.Replicas = &one
	dep.Status.ReadyReplicas = 1
	if err := c.Status().Update(ctx, dep); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, cm); err != nil {
		t.Fatal(err)
	}
	if got := nestedFloat(t, cm.Data[kasmConfigKey], "desktop", "resolution", "width"); got != 2560 {
		t.Fatalf("ConfigMap must follow the admin directive, got width=%v", got)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	if dep.Spec.Template.Annotations[annotationKasmConfigHash] != hash {
		t.Fatal("a running workload must NOT roll on a config edit (boundary convergence)")
	}
	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	if cond := findCondition(got, waasv1alpha1.ConditionTemplateDrifted); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected TemplateDrifted=True while running, got %+v", cond)
	}

	// Pause = a scale-up boundary on the way: the template converges
	// while no session can be killed, and drift clears.
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, ws); err != nil {
		t.Fatal(err)
	}
	ws.Spec.Paused = true
	if err := c.Update(ctx, ws); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	if dep.Spec.Template.Annotations[annotationKasmConfigHash] == hash {
		t.Fatal("pausing must converge the pod template (no session to kill)")
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	if cond := findCondition(got, waasv1alpha1.ConditionTemplateDrifted); cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("expected TemplateDrifted=False after convergence, got %+v", cond)
	}

	// Admin field cleared while down: the ConfigMap STAYS (clipboard
	// enforcement is policy-driven, not admin-driven) but drops the admin
	// directive; the mount stays too.
	tpl.Spec.KasmVNCConfig = ""
	if err := c.Update(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, cm); err != nil {
		t.Fatal("clearing the admin field must NOT delete the enforcement ConfigMap")
	}
	if _, ok := parseYAML(t, cm.Data[kasmConfigKey])["desktop"]; ok {
		t.Fatal("cleared admin field must drop the admin directive")
	}
	clipboardEnabled(t, cm.Data[kasmConfigKey]) // enforcement still present
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == kasmConfigVolume {
			found = true
		}
	}
	if !found {
		t.Fatal("kasmvnc workspace must keep the config mount for clipboard enforcement")
	}
}

func parseYAML(t *testing.T, content string) map[string]any {
	t.Helper()
	root := map[string]any{}
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		t.Fatalf("parsing: %v", err)
	}
	return root
}
