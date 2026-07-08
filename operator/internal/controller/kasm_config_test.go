package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestKasmConfigMaterializedAndRolledOnChange(t *testing.T) {
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
	if cm.Data[kasmConfigKey] != tpl.Spec.KasmVNCConfig {
		t.Fatalf("ConfigMap must carry the template content verbatim, got %q", cm.Data[kasmConfigKey])
	}

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

	// Content change: ConfigMap converges and the hash moves (rollout).
	tpl.Spec.KasmVNCConfig = "desktop:\n  resolution:\n    width: 2560\n"
	if err := c.Update(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, cm); err != nil {
		t.Fatal(err)
	}
	if cm.Data[kasmConfigKey] != tpl.Spec.KasmVNCConfig {
		t.Fatal("ConfigMap must follow the template content")
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	if dep.Spec.Template.Annotations[annotationKasmConfigHash] == hash {
		t.Fatal("hash must change with the content — that is the rollout signal")
	}

	// Field cleared: the ConfigMap goes away, the mount disappears.
	tpl.Spec.KasmVNCConfig = ""
	if err := c.Update(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, cm); err == nil {
		t.Fatal("cleared field must delete the ConfigMap")
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == kasmConfigVolume {
			t.Fatal("cleared field must drop the volume")
		}
	}
}
