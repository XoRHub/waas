package controller

import (
	"context"
	"slices"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/kasmcfg"
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

	deny, err := applyPolicy(admin, kasmPolicy{})
	if err != nil {
		t.Fatalf("applyPolicy(deny): %v", err)
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
	mixed, err := applyPolicy(admin, kasmPolicy{copyAllowed: true})
	if err != nil {
		t.Fatalf("applyPolicy(mixed): %v", err)
	}
	copyE, pasteE, override = clipboardEnabled(t, mixed)
	if !copyE || pasteE || override {
		t.Fatalf("expected copy=true paste=false override=false, got %v/%v/%v", copyE, pasteE, override)
	}

	// Empty admin config still yields a complete enforcement document.
	empty, err := applyPolicy("", kasmPolicy{copyAllowed: true, pasteAllowed: true})
	if err != nil {
		t.Fatalf("applyPolicy(empty): %v", err)
	}
	copyE, pasteE, override = clipboardEnabled(t, empty)
	if !copyE || !pasteE || override {
		t.Fatalf("allowing policy over empty config: got copy=%v paste=%v override=%v", copyE, pasteE, override)
	}
}

// The watermark text is stamped only when the policy sets one; the admin's
// other watermark sub-keys survive next to it.
func TestKasmWatermarkStamped(t *testing.T) {
	admin := "data_loss_prevention:\n  watermark:\n    text:\n      font_size: 24\n"
	without, err := applyPolicy(admin, kasmPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parseYAML(t, without)["data_loss_prevention"].(map[string]any)["watermark"].(map[string]any)["text"].(map[string]any)["template"]; ok {
		t.Fatal("no policy watermark must leave the template key absent")
	}
	with, err := applyPolicy(admin, kasmPolicy{watermark: "WaaS alice %H:%M"})
	if err != nil {
		t.Fatal(err)
	}
	text := parseYAML(t, with)["data_loss_prevention"].(map[string]any)["watermark"].(map[string]any)["text"].(map[string]any)
	if text["template"] != "WaaS alice %H:%M" {
		t.Fatalf("watermark text not stamped, got %v", text)
	}
	if got := nestedFloat(t, with, "data_loss_prevention", "watermark", "text", "font_size"); got != 24 {
		t.Fatalf("admin watermark sub-key must survive, got %v", got)
	}
}

// The owner's policy decides printing and the watermark text, with the
// owner's username substituted; no policy fails closed on all of it.
func TestKasmPolicyOfFollowsOwner(t *testing.T) {
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"
	ws.Annotations = map[string]string{waasv1alpha1.AnnotationUsername: "o'reilly"}
	pol := &waasv1alpha1.WorkspacePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "default"},
		Spec: waasv1alpha1.WorkspacePolicySpec{
			FileTransfer: &waasv1alpha1.FileTransferPolicy{Download: true},
			Watermark:    "{user} %H:%M",
		},
	}
	r, _ := newFixture(t, kasmTemplate(), ws, pol)
	got := r.kasmPolicyOf(context.Background(), ws)
	want := kasmPolicy{copyAllowed: true, pasteAllowed: true, printing: true, watermark: "o_reilly %H:%M"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	r, _ = newFixture(t, kasmTemplate(), ws)
	if got := r.kasmPolicyOf(context.Background(), ws); got != (kasmPolicy{}) {
		t.Fatalf("no policy must fail closed, got %+v", got)
	}
}

// The printer toggle follows the policy; the other side services stay off.
func TestKasmPrinterFollowsPolicy(t *testing.T) {
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"
	for _, printing := range []bool{false, true} {
		env := map[string]string{}
		for _, e := range desktopEnv(ws, kasmTemplate(), &waasv1alpha1.WorkspaceOverrides{}, printing, false) {
			env[e.Name] = e.Value
		}
		for _, name := range kasmcfg.SideServiceEnv {
			want := "0"
			if name == kasmcfg.PrinterEnv && printing {
				want = "1"
			}
			if env[name] != want {
				t.Errorf("printing=%v: %s = %q, want %q", printing, name, env[name], want)
			}
		}
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
		if v.Name == kasmConfigVolume || v.Name == kasmVncDirVolume || v.Name == kasmShmVolume {
			t.Fatalf("non-kasmvnc workspace must not mount kasmvnc volumes, found %q", v.Name)
		}
	}
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		if slices.Contains(kasmcfg.SideServiceEnv, e.Name) {
			t.Fatalf("non-kasmvnc workspace must not get kasmweb side-service env, found %q", e.Name)
		}
	}
}

func kasmDeployment(t *testing.T, tpl *waasv1alpha1.WorkspaceTemplate, ws *waasv1alpha1.Workspace) *appsv1.Deployment {
	t.Helper()
	r, c := newFixture(t, tpl, ws)
	reconcile(t, r, ws)
	dep := &appsv1.Deployment{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	return dep
}

// The kasmweb side services WaaS does not proxy are forced off, over
// whatever the template or the workspace override says.
func TestKasmSideServicesForcedOff(t *testing.T) {
	tpl := kasmTemplate()
	tpl.Spec.Env = []corev1.EnvVar{{Name: "KASM_SVC_AUDIO", Value: "1"}, {Name: "START_PULSEAUDIO", Value: "1"}}
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"
	ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{Env: []corev1.EnvVar{{Name: "KASM_SVC_PRINTER", Value: "1"}}}

	env := map[string]string{}
	for _, e := range kasmDeployment(t, tpl, ws).Spec.Template.Spec.Containers[0].Env {
		if _, dup := env[e.Name]; dup {
			t.Fatalf("env %q set twice", e.Name)
		}
		env[e.Name] = e.Value
	}
	for _, name := range kasmcfg.SideServiceEnv {
		if env[name] != "0" {
			t.Errorf("%s must be forced to 0, got %q", name, env[name])
		}
	}
	if env["START_PULSEAUDIO"] != "1" {
		t.Error("unmanaged env must survive")
	}
}

// A legacy template stored before the webhook/api-server guard existed
// could list a managed name twice; mergeEnv only replaces the first
// entry of a name, so without dropping every existing entry first the
// stamp would be shadowed by the duplicate's later "1".
func TestKasmSideServicesForcedOffDropsDuplicateTemplateEntries(t *testing.T) {
	tpl := kasmTemplate()
	tpl.Spec.Env = []corev1.EnvVar{
		{Name: "KASM_SVC_AUDIO", Value: "1"},
		{Name: "KASM_SVC_AUDIO", Value: "1"},
	}
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"

	env := map[string]string{}
	for _, e := range kasmDeployment(t, tpl, ws).Spec.Template.Spec.Containers[0].Env {
		if _, dup := env[e.Name]; dup {
			t.Fatalf("env %q set twice", e.Name)
		}
		env[e.Name] = e.Value
	}
	if env["KASM_SVC_AUDIO"] != "0" {
		t.Errorf("duplicated template entry must not survive the stamp, got %q", env["KASM_SVC_AUDIO"])
	}
}

// kasmvnc pods get a Memory /dev/shm unless the admin already mounts one.
func TestKasmShmMount(t *testing.T) {
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"
	spec := kasmDeployment(t, kasmTemplate(), ws).Spec.Template.Spec
	var shm *corev1.Volume
	for i := range spec.Volumes {
		if spec.Volumes[i].Name == kasmShmVolume {
			shm = &spec.Volumes[i]
		}
	}
	if shm == nil || shm.EmptyDir == nil || shm.EmptyDir.Medium != corev1.StorageMediumMemory ||
		shm.EmptyDir.SizeLimit == nil || shm.EmptyDir.SizeLimit.String() != kasmShmSize {
		t.Fatalf("expected a %s Memory emptyDir for /dev/shm, got %+v", kasmShmSize, shm)
	}
	if !slices.ContainsFunc(spec.Containers[0].VolumeMounts, func(m corev1.VolumeMount) bool {
		return m.Name == kasmShmVolume && m.MountPath == "/dev/shm"
	}) {
		t.Fatalf("expected the /dev/shm mount, got %+v", spec.Containers[0].VolumeMounts)
	}

	// Admin-provided /dev/shm: theirs is kept, ours is not added.
	tpl := kasmTemplate()
	tpl.Spec.Workload = &waasv1alpha1.WorkspaceWorkload{
		Volumes:      []corev1.Volume{{Name: "dshm", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}}}},
		VolumeMounts: []corev1.VolumeMount{{Name: "dshm", MountPath: "/dev/shm/"}},
	}
	spec = kasmDeployment(t, tpl, ws).Spec.Template.Spec
	for _, v := range spec.Volumes {
		if v.Name == kasmShmVolume {
			t.Fatal("an admin /dev/shm mount must not get a duplicate")
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
