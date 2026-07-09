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

// TestMapCatalogToWorkspaces pins the catalog watch: which templates an
// entry governs is a catalog-global question (best registry-prefix), so
// an edit re-enqueues every workspace of the namespace — and only them.
func TestMapCatalogToWorkspaces(t *testing.T) {
	img := &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: "default"},
		Spec:       waasv1alpha1.WorkspaceImageSpec{Image: "ghcr.io/xorhub/waas/desktop-xfce:latest"},
	}
	onXfce := workspace()
	onOther := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "eve", Namespace: "default"},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "chrome", Owner: "u2"},
	}
	elsewhere := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "far", Namespace: "other"},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: "u3"},
	}
	r, _ := newFixture(t, linuxTemplate(), img, onXfce, onOther, elsewhere)

	reqs := r.mapCatalogToWorkspaces(context.Background(), img)
	if len(reqs) != 2 {
		t.Fatalf("expected the namespace's whole fleet (2), got %+v", reqs)
	}
	for _, req := range reqs {
		if req.Namespace != "default" {
			t.Fatalf("catalog edits must stay namespace-scoped, got %+v", req)
		}
	}
}
