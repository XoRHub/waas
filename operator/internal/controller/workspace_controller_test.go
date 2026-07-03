package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("adding core scheme: %v", err)
	}
	if err := waasv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding waas scheme: %v", err)
	}
	return scheme
}

func newFixture(t *testing.T, objs ...client.Object) (*WorkspaceReconciler, client.Client) {
	t.Helper()
	scheme := newScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&waasv1alpha1.Workspace{}).
		Build()
	return &WorkspaceReconciler{Client: c}, c
}

func linuxTemplate() *waasv1alpha1.WorkspaceTemplate {
	return &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: "default"},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			DisplayName: "XFCE Desktop",
			OS:          waasv1alpha1.OSLinux,
			Image:       "ghcr.io/xorhub/waas/desktop-xfce:latest",
		},
	}
}

func workspace() *waasv1alpha1.Workspace {
	return &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "marc", Namespace: "default", Generation: 1},
		Spec: waasv1alpha1.WorkspaceSpec{
			TemplateRef: "xfce",
			Owner:       "8f4e1f1a-0000-4000-8000-000000000001",
		},
	}
}

func reconcile(t *testing.T, r *WorkspaceReconciler, ws *waasv1alpha1.Workspace) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: ws.Namespace, Name: ws.Name},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return res
}

func TestReconcileProvisionsLinuxWorkspace(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, linuxTemplate(), ws)
	ctx := context.Background()

	res := reconcile(t, r, ws)
	if res.RequeueAfter != requeueTransient {
		t.Fatalf("expected transient requeue, got %+v", res)
	}

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc-home"}, pvc); err != nil {
		t.Fatalf("expected home PVC: %v", err)
	}
	if len(pvc.OwnerReferences) != 0 {
		t.Fatalf("home PVC must not be owned by the workspace (state survives deletion)")
	}

	pod := &corev1.Pod{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, pod); err != nil {
		t.Fatalf("expected desktop pod: %v", err)
	}
	if pod.Spec.Containers[0].Ports[0].ContainerPort != 5901 {
		t.Fatalf("expected default VNC port 5901, got %d", pod.Spec.Containers[0].Ports[0].ContainerPort)
	}

	svc := &corev1.Service{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, svc); err != nil {
		t.Fatalf("expected workspace service: %v", err)
	}
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Fatalf("workspace service must be ClusterIP, got %s", svc.Spec.Type)
	}

	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatalf("fetching workspace: %v", err)
	}
	if got.Status.Phase != waasv1alpha1.PhaseProvisioning {
		t.Fatalf("expected Provisioning, got %s", got.Status.Phase)
	}
	if got.Status.Protocol != "vnc" || got.Status.Port != 5901 {
		t.Fatalf("unexpected connection info: %+v", got.Status)
	}
}

func TestReconcileReportsRunningWhenPodReady(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, linuxTemplate(), ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	pod := &corev1.Pod{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, pod); err != nil {
		t.Fatalf("fetching pod: %v", err)
	}
	pod.Status.Phase = corev1.PodRunning
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	if err := c.Status().Update(ctx, pod); err != nil {
		t.Fatalf("updating pod status: %v", err)
	}

	if res := reconcile(t, r, ws); res.RequeueAfter != 0 {
		t.Fatalf("running workspace should not requeue, got %+v", res)
	}

	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatalf("fetching workspace: %v", err)
	}
	if got.Status.Phase != waasv1alpha1.PhaseRunning {
		t.Fatalf("expected Running, got %s", got.Status.Phase)
	}
}

func TestReconcilePausedDeletesPodKeepsPVC(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, linuxTemplate(), ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatalf("fetching workspace: %v", err)
	}
	got.Spec.Paused = true
	if err := c.Update(ctx, got); err != nil {
		t.Fatalf("updating workspace: %v", err)
	}

	reconcile(t, r, got)

	pod := &corev1.Pod{}
	err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, pod)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected pod to be deleted while paused, got err=%v", err)
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc-home"}, pvc); err != nil {
		t.Fatalf("home PVC must survive pause: %v", err)
	}

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatalf("fetching workspace: %v", err)
	}
	if got.Status.Phase != waasv1alpha1.PhaseStopped {
		t.Fatalf("expected Stopped, got %s", got.Status.Phase)
	}
}

func TestReconcileWindowsWithoutKubeVirtFailsLoudly(t *testing.T) {
	tpl := linuxTemplate()
	tpl.Spec.OS = waasv1alpha1.OSWindows
	ws := workspace()
	r, c := newFixture(t, tpl, ws)
	r.KubeVirtAvailable = false

	reconcile(t, r, ws)

	got := &waasv1alpha1.Workspace{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatalf("fetching workspace: %v", err)
	}
	if got.Status.Phase != waasv1alpha1.PhaseFailed {
		t.Fatalf("expected Failed, got %s", got.Status.Phase)
	}
	if len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Reason != "KubeVirtUnavailable" {
		t.Fatalf("expected KubeVirtUnavailable condition, got %+v", got.Status.Conditions)
	}
}

func TestReconcileMissingTemplateStaysPending(t *testing.T) {
	ws := workspace()
	r, c := newFixture(t, ws)

	res := reconcile(t, r, ws)
	if res.RequeueAfter != requeueMissing {
		t.Fatalf("expected requeue while template is missing, got %+v", res)
	}

	got := &waasv1alpha1.Workspace{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatalf("fetching workspace: %v", err)
	}
	if got.Status.Phase != waasv1alpha1.PhasePending {
		t.Fatalf("expected Pending, got %s", got.Status.Phase)
	}
}
