package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// TestMapTemplateToWorkspaces pins the watch wiring that makes drift
// VISIBLE (docs/adr/0001): editing a template must enqueue exactly the
// workspaces stamped from it — a running workspace has no timed
// requeue, so this mapping is the only thing standing between a
// template edit and a TemplateDrifted badge that never shows up.
func TestMapTemplateToWorkspaces(t *testing.T) {
	tpl := linuxTemplate()
	onXfce := workspace()
	onOther := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "eve", Namespace: "default"},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "chrome", Owner: "u2"},
	}
	elsewhere := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "far", Namespace: "other"},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: "u3"},
	}
	r, _ := newFixture(t, tpl, onXfce, onOther, elsewhere)

	reqs := r.mapTemplateToWorkspaces(context.Background(), tpl)
	if len(reqs) != 1 {
		t.Fatalf("expected exactly the one workspace stamped from %q, got %+v", tpl.Name, reqs)
	}
	if reqs[0].Name != "marc" || reqs[0].Namespace != "default" {
		t.Fatalf("expected default/marc, got %+v", reqs[0])
	}
}
