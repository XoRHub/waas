package controller

// Volume retention tests: at deletion the home PVC is DETACHED by
// default (retained volume: still owned, still counted in the storage
// quota) and only deleted on the explicit opt-in annotation; a retained
// volume can be adopted back as the home of a new workspace.

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func TestDeletionRetainsHomeVolumeByDefault(t *testing.T) {
	ws := workspace()
	ws.Spec.DisplayName = "Poste CAD"
	r, c := newFixture(t, linuxTemplate(), ws)
	ctx := context.Background()

	reconcile(t, r, ws) // provisions PVC + finalizer
	if err := c.Delete(ctx, &waasv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "marc"}}); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws) // finalizer path

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, &waasv1alpha1.Workspace{}); !apierrors.IsNotFound(err) {
		t.Fatalf("workspace must be gone, got err=%v", err)
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc-home"}, pvc); err != nil {
		t.Fatalf("home volume must survive by default: %v", err)
	}
	if pvc.Labels[waasv1alpha1.LabelRetained] != "true" {
		t.Fatalf("retained volume must carry the retained label, got %v", pvc.Labels)
	}
	if pvc.Labels[waasv1alpha1.LabelOwner] != ws.Spec.Owner {
		t.Fatalf("retained volume must keep its owner, got %v", pvc.Labels)
	}
	if pvc.Labels[labelWorkspace] != "" {
		t.Fatalf("retained volume must not point at a dead workspace, got %v", pvc.Labels)
	}
	if pvc.Annotations[waasv1alpha1.AnnotationOriginWorkspace] != "Poste CAD" {
		t.Fatalf("provenance annotation missing, got %v", pvc.Annotations)
	}
	if pvc.Annotations[waasv1alpha1.AnnotationRetainedAt] == "" {
		t.Fatalf("retained-at annotation missing, got %v", pvc.Annotations)
	}
}

func TestDeletionDeletesHomeVolumeOnExplicitChoice(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, linuxTemplate(), ws)
	ctx := context.Background()

	reconcile(t, r, ws)
	// The api-server stamps the opt-in annotation right before deleting.
	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations == nil {
		got.Annotations = map[string]string{}
	}
	got.Annotations[waasv1alpha1.AnnotationDeleteHome] = "true"
	if err := c.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, got); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc-home"}, &corev1.PersistentVolumeClaim{}); !apierrors.IsNotFound(err) {
		t.Fatalf("explicitly-deleted home volume must be gone, got err=%v", err)
	}
}

func TestAdoptedRetainedVolumeIsRelabeledLive(t *testing.T) {
	retained := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name:      "old-home",
		Namespace: "default",
		Labels: map[string]string{
			labelManagedBy:             managerName,
			waasv1alpha1.LabelOwner:    "8f4e1f1a-0000-4000-8000-000000000001",
			waasv1alpha1.LabelRetained: "true",
		},
		Annotations: map[string]string{
			waasv1alpha1.AnnotationOriginWorkspace: "ancien poste",
			waasv1alpha1.AnnotationRetainedAt:      "2026-07-01T00:00:00Z",
		},
	}}
	ws := workspace()
	ws.Spec.HomeVolumeName = "old-home"
	r, c := newFixture(t, linuxTemplate(), ws, retained)
	ctx := context.Background()

	reconcile(t, r, ws)

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "old-home"}, pvc); err != nil {
		t.Fatal(err)
	}
	if pvc.Labels[waasv1alpha1.LabelRetained] != "" {
		t.Fatalf("adopted volume must shed the retained marker, got %v", pvc.Labels)
	}
	if pvc.Labels[labelWorkspace] != "marc" {
		t.Fatalf("adopted volume must belong to its new workspace, got %v", pvc.Labels)
	}
	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.PVCName != "old-home" {
		t.Fatalf("status must advertise the adopted volume, got %q", got.Status.PVCName)
	}
}

// ---- spec.homeVolume: template-driven PVC metadata (backup enrollment) ----

func homeVolumeTemplate(labels, annotations map[string]string) *waasv1alpha1.WorkspaceTemplate {
	tpl := linuxTemplate()
	tpl.Spec.HomeVolume = &waasv1alpha1.WorkspaceHomeVolume{Labels: labels, Annotations: annotations}
	return tpl
}

func getHomePVC(t *testing.T, c client.Client, name string) *corev1.PersistentVolumeClaim {
	t.Helper()
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: name}, pvc); err != nil {
		t.Fatalf("fetching pvc %s: %v", name, err)
	}
	return pvc
}

func TestHomeVolumeMetaStampedAtCreation(t *testing.T) {
	tpl := homeVolumeTemplate(
		map[string]string{
			"recurring-job.longhorn.io/source": "enabled",
			// Denylist-filtered (2nd line of defense behind the webhook):
			// a platform key in the template must never override ours.
			labelWorkspace: "spoofed",
		},
		map[string]string{"backup.example.com/tier": "gold"},
	)
	ws := workspace()
	r, c := newFixture(t, tpl, ws)

	reconcile(t, r, ws)

	pvc := getHomePVC(t, c, "ws-marc-home")
	if pvc.Labels["recurring-job.longhorn.io/source"] != "enabled" {
		t.Fatalf("template label missing on the PVC: %v", pvc.Labels)
	}
	if pvc.Labels[labelWorkspace] != "marc" {
		t.Fatalf("platform label must win over a colliding template key: %v", pvc.Labels)
	}
	if pvc.Annotations["backup.example.com/tier"] != "gold" {
		t.Fatalf("template annotation missing: %v", pvc.Annotations)
	}
	want := `{"labels":["recurring-job.longhorn.io/source"],"annotations":["backup.example.com/tier"]}`
	if got := pvc.Annotations[waasv1alpha1.AnnotationTemplateMeta]; got != want {
		t.Fatalf("ledger = %q, want %q", got, want)
	}
}

func TestHomeVolumeMetaConvergesOnTemplateEdit(t *testing.T) {
	tpl := homeVolumeTemplate(map[string]string{
		"recurring-job.longhorn.io/source":             "enabled",
		"recurring-job-group.longhorn.io/backup-daily": "enabled",
	}, nil)
	ws := workspace()
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	// An admin stamps a key BY HAND (outside the ledger): every
	// convergence pass below must leave it alone.
	pvc := getHomePVC(t, c, "ws-marc-home")
	pvc.Labels["ops.example.com/hand-set"] = "keep"
	if err := c.Update(ctx, pvc); err != nil {
		t.Fatal(err)
	}

	// Template edit: one key changes value, one is REMOVED, one appears.
	fresh := &waasv1alpha1.WorkspaceTemplate{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "xfce"}, fresh); err != nil {
		t.Fatal(err)
	}
	fresh.Spec.HomeVolume = &waasv1alpha1.WorkspaceHomeVolume{Labels: map[string]string{
		"recurring-job.longhorn.io/source":              "disabled",
		"recurring-job-group.longhorn.io/backup-weekly": "enabled",
	}}
	if err := c.Update(ctx, fresh); err != nil {
		t.Fatal(err)
	}

	reconcile(t, r, ws)

	pvc = getHomePVC(t, c, "ws-marc-home")
	if pvc.Labels["recurring-job.longhorn.io/source"] != "disabled" {
		t.Fatalf("value change must propagate: %v", pvc.Labels)
	}
	if _, ok := pvc.Labels["recurring-job-group.longhorn.io/backup-daily"]; ok {
		t.Fatalf("removed template key must leave the PVC: %v", pvc.Labels)
	}
	if pvc.Labels["recurring-job-group.longhorn.io/backup-weekly"] != "enabled" {
		t.Fatalf("new template key must land: %v", pvc.Labels)
	}
	if pvc.Labels["ops.example.com/hand-set"] != "keep" {
		t.Fatalf("admin key outside the ledger must survive: %v", pvc.Labels)
	}
	want := `{"labels":["recurring-job-group.longhorn.io/backup-weekly","recurring-job.longhorn.io/source"]}`
	if got := pvc.Annotations[waasv1alpha1.AnnotationTemplateMeta]; got != want {
		t.Fatalf("ledger = %q, want %q", got, want)
	}
}

func TestHomeVolumeMetaCrossTemplateAdoption(t *testing.T) {
	// A retained volume still carrying template A's keys and ledger,
	// re-adopted by a workspace of template B: A's keys are removed
	// (they are in the ledger), B's are stamped.
	retained := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name:      "old-home",
		Namespace: "default",
		Labels: map[string]string{
			labelManagedBy:                     managerName,
			waasv1alpha1.LabelOwner:            "8f4e1f1a-0000-4000-8000-000000000001",
			waasv1alpha1.LabelRetained:         "true",
			"recurring-job.longhorn.io/source": "enabled",
		},
		Annotations: map[string]string{
			waasv1alpha1.AnnotationTemplateMeta: `{"labels":["recurring-job.longhorn.io/source"]}`,
		},
	}}
	tplB := homeVolumeTemplate(map[string]string{"backup.example.com/plan": "weekly"}, nil)
	ws := workspace()
	ws.Spec.HomeVolumeName = "old-home"
	r, c := newFixture(t, tplB, ws, retained)

	reconcile(t, r, ws)

	pvc := getHomePVC(t, c, "old-home")
	if _, ok := pvc.Labels["recurring-job.longhorn.io/source"]; ok {
		t.Fatalf("template A's ledgered key must be removed on adoption by B: %v", pvc.Labels)
	}
	if pvc.Labels["backup.example.com/plan"] != "weekly" {
		t.Fatalf("template B's key must land: %v", pvc.Labels)
	}
	if pvc.Labels[labelWorkspace] != "marc" || pvc.Labels[waasv1alpha1.LabelRetained] != "" {
		t.Fatalf("adoption relabel must still apply: %v", pvc.Labels)
	}
	want := `{"labels":["backup.example.com/plan"]}`
	if got := pvc.Annotations[waasv1alpha1.AnnotationTemplateMeta]; got != want {
		t.Fatalf("ledger = %q, want %q", got, want)
	}
}

func TestHomeVolumeMetaSurvivesRetention(t *testing.T) {
	// A detached volume still holds the user's data — exactly the one to
	// keep backing up: finalizeHomeVolume must strip neither the
	// template metadata nor the ledger.
	tpl := homeVolumeTemplate(map[string]string{"recurring-job.longhorn.io/source": "enabled"}, nil)
	ws := workspace()
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)
	if err := c.Delete(ctx, &waasv1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "marc"}}); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)

	pvc := getHomePVC(t, c, "ws-marc-home")
	if pvc.Labels[waasv1alpha1.LabelRetained] != "true" {
		t.Fatalf("volume must be retained: %v", pvc.Labels)
	}
	if pvc.Labels["recurring-job.longhorn.io/source"] != "enabled" {
		t.Fatalf("retained volume must keep its template metadata: %v", pvc.Labels)
	}
	if pvc.Annotations[waasv1alpha1.AnnotationTemplateMeta] == "" {
		t.Fatalf("retained volume must keep the ledger: %v", pvc.Annotations)
	}
}

func TestHomeVolumeMetaRepairsCorruptedLedger(t *testing.T) {
	tpl := homeVolumeTemplate(map[string]string{"recurring-job.longhorn.io/source": "enabled"}, nil)
	ws := workspace()
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)
	pvc := getHomePVC(t, c, "ws-marc-home")
	pvc.Annotations[waasv1alpha1.AnnotationTemplateMeta] = "{not json"
	if err := c.Update(ctx, pvc); err != nil {
		t.Fatal(err)
	}

	reconcile(t, r, ws)

	pvc = getHomePVC(t, c, "ws-marc-home")
	want := `{"labels":["recurring-job.longhorn.io/source"]}`
	if got := pvc.Annotations[waasv1alpha1.AnnotationTemplateMeta]; got != want {
		t.Fatalf("corrupted ledger must be rewritten, got %q", got)
	}
}

func TestHomeVolumeMetaNoChurnWithoutTemplateBlock(t *testing.T) {
	// No homeVolume block, no ledger: the steady state must not Update
	// the PVC on every reconcile.
	ws := workspace()
	r, c := newFixture(t, linuxTemplate(), ws)

	reconcile(t, r, ws)
	before := getHomePVC(t, c, "ws-marc-home").ResourceVersion
	reconcile(t, r, ws)
	after := getHomePVC(t, c, "ws-marc-home").ResourceVersion
	if before != after {
		t.Fatalf("PVC must not churn (resourceVersion %s -> %s)", before, after)
	}
}
