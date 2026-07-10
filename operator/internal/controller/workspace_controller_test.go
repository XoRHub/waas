package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
		WithStatusSubresource(&waasv1alpha1.Workspace{}, &appsv1.Deployment{}, &appsv1.StatefulSet{}).
		Build()
	// Probe succeeds by default: unit tests exercise reconcile logic, not
	// real TCP reachability (covered by its own test with a failing stub).
	return &WorkspaceReconciler{Client: c, Probe: func(string) error { return nil }}, c
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

	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatalf("expected desktop deployment: %v", err)
	}
	if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("desktop deployment must use Recreate (RWO home volume), got %s", dep.Spec.Strategy.Type)
	}
	if dep.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort != 5901 {
		t.Fatalf("expected default VNC port 5901, got %d", dep.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort)
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

	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatalf("fetching deployment: %v", err)
	}
	dep.Status.ReadyReplicas = 1
	if err := c.Status().Update(ctx, dep); err != nil {
		t.Fatalf("updating deployment status: %v", err)
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

// Pause scales the Deployment to 0 (not delete): the object and its spec
// survive so resume is a fast scale back to 1, and the home PVC is kept.
func TestReconcilePausedScalesToZeroKeepsWorkloadAndPVC(t *testing.T) {
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

	// Deployment must still exist, scaled to 0.
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatalf("deployment must survive pause (scale-to-0), got err=%v", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
		t.Fatalf("paused deployment must be scaled to 0, got replicas=%v", dep.Spec.Replicas)
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc-home"}, pvc); err != nil {
		t.Fatalf("home PVC must survive pause: %v", err)
	}

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatalf("fetching workspace: %v", err)
	}
	if got.Status.Phase != waasv1alpha1.PhasePaused {
		t.Fatalf("expected Paused, got %s", got.Status.Phase)
	}
	if got.Status.Address != "" || got.Status.Port != 0 {
		t.Fatalf("paused workspace must clear reachability, got %+v", got.Status)
	}

	// Resume: scale back to 1, object reused (not recreated).
	got.Spec.Paused = false
	if err := c.Update(ctx, got); err != nil {
		t.Fatalf("resuming workspace: %v", err)
	}
	reconcile(t, r, got)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatalf("fetching deployment after resume: %v", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
		t.Fatalf("resumed deployment must be scaled to 1, got replicas=%v", dep.Spec.Replicas)
	}
}

func TestReconcileLegacyPodWorkloadKind(t *testing.T) {
	tpl := linuxTemplate()
	tpl.Spec.Workload = &waasv1alpha1.WorkspaceWorkload{Kind: waasv1alpha1.WorkloadPod}
	ws := workspace()
	r, c := newFixture(t, tpl, ws)

	reconcile(t, r, ws)

	pod := &corev1.Pod{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "ws-marc"}, pod); err != nil {
		t.Fatalf("workload kind Pod must create a bare pod: %v", err)
	}
}

func TestReconcileMultiProtocolServiceAndStatus(t *testing.T) {
	tpl := linuxTemplate()
	tpl.Spec.Workload = &waasv1alpha1.WorkspaceWorkload{
		SecurityContext: &corev1.SecurityContext{RunAsUser: ptrInt64(1000)},
		NodeSelector:    map[string]string{"zone": "a"},
	}
	tpl.Spec.Env = []corev1.EnvVar{{Name: "VNC_PW", Value: "tpl"}, {Name: "KEEP", Value: "yes"}}
	tpl.Spec.Protocols = []waasv1alpha1.WorkspaceProtocol{
		{Name: "vnc", Port: 5901},
		{Name: "ssh", Port: 2222, Default: true},
	}
	ws := workspace()
	ws.Spec.Overrides = &waasv1alpha1.WorkspaceOverrides{
		Env:      []corev1.EnvVar{{Name: "VNC_PW", Value: "override"}},
		Protocol: "vnc",
	}
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	reconcile(t, r, ws)

	svc := &corev1.Service{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, svc); err != nil {
		t.Fatal(err)
	}
	if len(svc.Spec.Ports) != 2 {
		t.Fatalf("expected one service port per protocol, got %+v", svc.Spec.Ports)
	}

	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	podSpec := dep.Spec.Template.Spec
	if podSpec.Containers[0].SecurityContext == nil || *podSpec.Containers[0].SecurityContext.RunAsUser != 1000 {
		t.Fatalf("template security context must reach the container, got %+v", podSpec.Containers[0].SecurityContext)
	}
	if podSpec.NodeSelector["zone"] != "a" {
		t.Fatalf("template nodeSelector must reach the pod, got %+v", podSpec.NodeSelector)
	}
	env := map[string]string{}
	for _, e := range podSpec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	if env["VNC_PW"] != "override" || env["KEEP"] != "yes" {
		t.Fatalf("override env must win by name and keep the rest, got %v", env)
	}

	got := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, got); err != nil {
		t.Fatal(err)
	}
	// The workspace picked vnc over the template's ssh default.
	if got.Status.Protocol != "vnc" || got.Status.Port != 5901 {
		t.Fatalf("expected vnc/5901 default, got %s/%d", got.Status.Protocol, got.Status.Port)
	}
	if len(got.Status.Protocols) != 2 {
		t.Fatalf("expected both protocols in status, got %+v", got.Status.Protocols)
	}
}

func ptrInt64(v int64) *int64 { return &v }

// The audio port rides along the protocol ports (pod + Service) when a
// vnc entry exposes it, and an EXISTING Service converges to the new port
// list — the create-only Service would otherwise strand every workspace
// provisioned before the template enabled audio.
func TestReconcileAudioPortOnPodAndService(t *testing.T) {
	tpl := linuxTemplate()
	tpl.Spec.Protocols = []waasv1alpha1.WorkspaceProtocol{
		{Name: "vnc", Port: 5901, Default: true, Params: map[string]string{"enable-audio": "true"}},
	}
	ws := workspace()
	r, c := newFixture(t, tpl, ws)
	ctx := context.Background()

	// First reconcile: no audio exposure — one port, like before.
	reconcile(t, r, ws)
	svc := &corev1.Service{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, svc); err != nil {
		t.Fatal(err)
	}
	if len(svc.Spec.Ports) != 1 {
		t.Fatalf("expected the single vnc port, got %+v", svc.Spec.Ports)
	}

	// The template turns audio exposure on AFTER the Service exists.
	got := &waasv1alpha1.WorkspaceTemplate{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "xfce"}, got); err != nil {
		t.Fatal(err)
	}
	got.Spec.Protocols[0].ExposeAudioPort = true
	if err := c.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, ws)

	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, svc); err != nil {
		t.Fatal(err)
	}
	if len(svc.Spec.Ports) != 2 {
		t.Fatalf("existing service must converge to the audio port, got %+v", svc.Spec.Ports)
	}
	audio := svc.Spec.Ports[1]
	if audio.Name != audioPortName || audio.Port != waasv1alpha1.PulseAudioPort || audio.TargetPort.IntValue() != int(waasv1alpha1.PulseAudioPort) {
		t.Fatalf("unexpected audio service port: %+v", audio)
	}

	// The pod template gains the container port too — at the next
	// scale-up boundary (docs/adr/0001: template edits never land
	// mid-session), so pause then resume.
	fresh := &waasv1alpha1.Workspace{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, fresh); err != nil {
		t.Fatal(err)
	}
	fresh.Spec.Paused = true
	if err := c.Update(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, fresh)
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "marc"}, fresh); err != nil {
		t.Fatal(err)
	}
	fresh.Spec.Paused = false
	if err := c.Update(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, fresh)

	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	ports := dep.Spec.Template.Spec.Containers[0].Ports
	if len(ports) != 2 || ports[1].Name != audioPortName || ports[1].ContainerPort != waasv1alpha1.PulseAudioPort {
		t.Fatalf("expected vnc + audio container ports, got %+v", ports)
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
