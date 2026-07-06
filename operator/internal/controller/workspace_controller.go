package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/internal/kubevirt"
)

const (
	labelManagedBy = "app.kubernetes.io/managed-by"
	labelWorkspace = "waas.xorhub.io/workspace"
	labelOwner     = "waas.xorhub.io/owner"
	managerName    = "waas-operator"

	requeueTransient = 5 * time.Second
	requeueMissing   = 30 * time.Second

	defaultHomeSize = "10Gi"
	homeMountPath   = "/home/user"
)

// WorkspaceReconciler reconciles a Workspace object into a pod + service +
// home PVC (linux) or a KubeVirt VirtualMachine (windows). It only ever talks
// to the Kubernetes API — never to the platform database.
type WorkspaceReconciler struct {
	client.Client
	// KubeVirtAvailable is detected once at startup. Windows workspaces are
	// normally rejected by the validating webhook when false; the reconciler
	// re-checks so a bypassed webhook still fails loudly instead of silently.
	KubeVirtAvailable bool
	// Recorder emits the audit trail (policy applied/denied, TTL
	// deletions) as Kubernetes Events on the Workspace.
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspaces,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspacetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines,verbs=get;list;watch;create;delete

// Reconcile drives a Workspace toward its desired state.
func (r *WorkspaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ws := &waasv1alpha1.Workspace{}
	if err := r.Get(ctx, req.NamespacedName, ws); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !ws.DeletionTimestamp.IsZero() {
		// Compute (pod/VM/service) is garbage-collected through owner
		// references. The home PVC carries no owner reference on purpose:
		// user state must survive workspace deletion and recreation.
		return ctrl.Result{}, nil
	}

	tpl := &waasv1alpha1.WorkspaceTemplate{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: ws.Namespace, Name: ws.Spec.TemplateRef}, tpl); err != nil {
		if apierrors.IsNotFound(err) {
			// GitOps may apply the Workspace before its template: keep
			// retrying instead of failing terminally.
			if err := r.setUnready(ctx, ws, waasv1alpha1.PhasePending, "TemplateNotFound",
				fmt.Sprintf("workspace template %q not found", ws.Spec.TemplateRef)); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: requeueMissing}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching template %s: %w", ws.Spec.TemplateRef, err)
	}

	if tpl.Spec.OS == waasv1alpha1.OSWindows && !r.KubeVirtAvailable {
		if err := r.setUnready(ctx, ws, waasv1alpha1.PhaseFailed, "KubeVirtUnavailable",
			"windows workspaces require KubeVirt, which is not installed in this cluster"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Lifecycle TTL first: an expired workspace is deleted whatever its
	// state (paused included — its storage is exactly what TTLs reclaim).
	deleted, lifetimeRequeue, err := r.enforceLifetime(ctx, ws)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}
	if deleted {
		return ctrl.Result{}, nil
	}

	// Governance re-check, second line behind the admission webhook (see
	// workspace_governance.go). Only gates the creation of NEW compute:
	// running pods are never torn down by policy (grandfathering).
	if !ws.Spec.Paused {
		hasCompute, err := r.computeExists(ctx, ws, tpl)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
		}
		if !hasCompute {
			pol, denial := r.evaluateGovernance(ctx, ws, tpl)
			if denial != nil {
				r.recordEvent(ws, corev1.EventTypeWarning, string(denial.Reason), denial.Message)
				if err := r.setUnready(ctx, ws, waasv1alpha1.PhaseFailed, string(denial.Reason), denial.Message); err != nil {
					return ctrl.Result{}, err
				}
				// Policies and catalog can change; retry on a slow loop.
				return ctrl.Result{RequeueAfter: requeueMissing}, nil
			}
			if pol != nil {
				r.recordEvent(ws, corev1.EventTypeNormal, "PolicyApplied",
					fmt.Sprintf("workspace admitted under policy %q for owner %s", pol.Name, ws.Spec.Owner))
			}
		}
	}

	pvcName, err := r.ensureHomePVC(ctx, ws, tpl)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}

	// Pause = scale to 0, NOT delete: the workload object, its spec and
	// config are kept so resume is a fast scale back to 1 with no
	// reconstruction. The home volume is retained either way.
	paused := ws.Spec.Paused

	var ready bool
	if tpl.Spec.OS == waasv1alpha1.OSWindows {
		ready, err = r.ensureVirtualMachine(ctx, ws, tpl, pvcName, !paused)
	} else {
		ready, err = r.ensureWorkload(ctx, ws, tpl, pvcName, paused)
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}
	// The Service is kept across pause so the in-cluster DNS name stays
	// stable and resume needs no endpoint churn.
	if err := r.ensureService(ctx, ws, tpl); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}

	if paused {
		if err := r.patchStatus(ctx, ws, func(st *waasv1alpha1.WorkspaceStatus) {
			st.Phase = waasv1alpha1.PhasePaused
			st.OS = tpl.Spec.OS
			st.PVCName = pvcName
			st.Address, st.Port, st.Protocol = "", 0, ""
			st.Protocols = nil
			setCondition(st, metav1.ConditionFalse, "Paused", "workspace is paused (scaled to 0); workload object and home volume retained")
		}); err != nil {
			return ctrl.Result{}, err
		}
		// Paused workspaces still age toward their TTL.
		return ctrl.Result{RequeueAfter: lifetimeRequeue}, nil
	}

	phase := waasv1alpha1.PhaseProvisioning
	if ready {
		phase = waasv1alpha1.PhaseRunning
	}
	if err := r.patchStatus(ctx, ws, func(st *waasv1alpha1.WorkspaceStatus) {
		st.Phase = phase
		st.OS = tpl.Spec.OS
		st.PVCName = pvcName
		st.Address = fmt.Sprintf("%s.%s.svc.cluster.local", computeName(ws), ws.Namespace)
		def := effectiveProtocol(ws, tpl)
		st.Port = def.Port
		st.Protocol = def.Name
		protocols := tpl.Spec.EffectiveProtocols()
		st.Protocols = make([]waasv1alpha1.WorkspaceProtocolStatus, 0, len(protocols))
		for _, p := range protocols {
			st.Protocols = append(st.Protocols, waasv1alpha1.WorkspaceProtocolStatus{
				Name: p.Name, Port: p.Port, Default: p.Name == def.Name,
			})
		}
		if ready {
			setCondition(st, metav1.ConditionTrue, "WorkspaceReady", "desktop is up and reachable")
		} else {
			setCondition(st, metav1.ConditionFalse, "Provisioning", "waiting for desktop to become ready")
		}
	}); err != nil {
		return ctrl.Result{}, err
	}

	if !ready {
		// Transient state: poll until the pod/VM reports ready.
		return ctrl.Result{RequeueAfter: requeueTransient}, nil
	}
	if lifetimeRequeue > 0 {
		// Wake up exactly when the TTL expires.
		return ctrl.Result{RequeueAfter: lifetimeRequeue}, nil
	}
	return ctrl.Result{}, nil
}

// computeExists reports whether the workspace already has its workload
// (linux) or VM (windows) — i.e. whether capacity was already granted.
func (r *WorkspaceReconciler) computeExists(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) (bool, error) {
	name := types.NamespacedName{Namespace: ws.Namespace, Name: computeName(ws)}
	if tpl.Spec.OS == waasv1alpha1.OSWindows {
		vm := &unstructured.Unstructured{}
		vm.SetGroupVersionKind(kubevirt.VirtualMachineGVK)
		err := r.Get(ctx, name, vm)
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return err == nil, err
	}
	// Any linux workload kind counts: capacity granted under a previous
	// template workload kind must stay grandfathered.
	for _, obj := range []client.Object{&appsv1.Deployment{}, &appsv1.StatefulSet{}, &corev1.Pod{}} {
		err := r.Get(ctx, name, obj)
		if err == nil {
			return true, nil
		}
		if !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	return false, nil
}

// ensureHomePVC creates the user-state volume if missing. Idempotent, and the
// PVC is deliberately not owned by the Workspace so state survives deletion.
func (r *WorkspaceReconciler) ensureHomePVC(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) (string, error) {
	name := computeName(ws) + "-home"
	existing := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Namespace: ws.Namespace, Name: name}, existing)
	if err == nil {
		return name, nil
	}
	if !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("fetching pvc %s: %w", name, err)
	}

	size := resource.MustParse(defaultHomeSize)
	if tpl.Spec.HomeSize != nil {
		size = *tpl.Spec.HomeSize
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ws.Namespace,
			Labels:    workspaceLabels(ws),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: tpl.Spec.StorageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: size},
			},
		},
	}
	if err := r.Create(ctx, pvc); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("creating pvc %s: %w", name, err)
	}
	return name, nil
}

// ensurePod creates a bare desktop pod (workload kind "Pod") if missing and
// reports readiness. Deployment/StatefulSet variants live in workload.go.
func (r *WorkspaceReconciler) ensurePod(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, pvcName string, paused bool) (bool, error) {
	name := computeName(ws)
	existing := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Namespace: ws.Namespace, Name: name}, existing)
	if err == nil {
		// A bare Pod has no replica count: "scale to 0" means delete it
		// (state lives on the home PVC, so resume recreates it cleanly).
		if paused {
			if delErr := r.Delete(ctx, existing); delErr != nil && !apierrors.IsNotFound(delErr) {
				return false, fmt.Errorf("pausing pod %s: %w", name, delErr)
			}
			return false, nil
		}
		return podReady(existing), nil
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("fetching pod %s: %w", name, err)
	}
	// Not found + paused: nothing to run, stay down.
	if paused {
		return false, nil
	}

	template := r.buildPodTemplate(ctx, ws, tpl, pvcName)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ws.Namespace,
			Labels:    workspaceLabels(ws),
		},
		Spec: template.Spec,
	}
	if err := controllerutil.SetControllerReference(ws, pod, r.Scheme()); err != nil {
		return false, fmt.Errorf("setting owner on pod %s: %w", name, err)
	}
	if err := r.Create(ctx, pod); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, fmt.Errorf("creating pod %s: %w", name, err)
	}
	return false, nil
}

// ensureVirtualMachine creates the KubeVirt VM for a windows workspace and
// reports readiness. Managed as unstructured so the operator has no compile
// -time dependency on KubeVirt (it is optional at runtime).
func (r *WorkspaceReconciler) ensureVirtualMachine(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, pvcName string, running bool) (bool, error) {
	name := computeName(ws)
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(kubevirt.VirtualMachineGVK)
	err := r.Get(ctx, types.NamespacedName{Namespace: ws.Namespace, Name: name}, existing)
	if err == nil {
		// Pause/resume a VM by toggling spec.running (keeps the VM object
		// and its disks; KubeVirt stops/starts the virt-launcher pod).
		cur, _, _ := unstructured.NestedBool(existing.Object, "spec", "running")
		if cur != running {
			if err := unstructured.SetNestedField(existing.Object, running, "spec", "running"); err != nil {
				return false, fmt.Errorf("setting running on virtualmachine %s: %w", name, err)
			}
			if err := r.Update(ctx, existing); err != nil {
				return false, fmt.Errorf("scaling virtualmachine %s (running=%t): %w", name, running, err)
			}
		}
		ready, _, _ := unstructured.NestedBool(existing.Object, "status", "ready")
		return ready && running, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("fetching virtualmachine %s: %w", name, err)
	}

	resources := tpl.Spec.Resources
	if ws.Spec.Resources != nil {
		resources = *ws.Spec.Resources
	}
	requests := map[string]any{}
	for res, qty := range resources.Requests {
		requests[string(res)] = qty.String()
	}
	if len(requests) == 0 {
		requests = map[string]any{"memory": "4Gi", "cpu": "2"}
	}

	labels := map[string]any{}
	for k, v := range workspaceLabels(ws) {
		labels[k] = v
	}
	vm := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": kubevirt.VirtualMachineGVK.GroupVersion().String(),
		"kind":       kubevirt.VirtualMachineGVK.Kind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": ws.Namespace,
			"labels":    labels,
		},
		"spec": map[string]any{
			// running toggles start/stop (pause = false); keeps the VM
			// object and disks. Mutually exclusive with runStrategy.
			"running": running,
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"domain": map[string]any{
						"devices": map[string]any{
							"disks": []any{
								map[string]any{"name": "os", "disk": map[string]any{"bus": "virtio"}},
								map[string]any{"name": "home", "disk": map[string]any{"bus": "virtio"}},
							},
							"interfaces": []any{
								map[string]any{"name": "default", "masquerade": map[string]any{}},
							},
						},
						"resources": map[string]any{"requests": requests},
					},
					"networks": []any{
						map[string]any{"name": "default", "pod": map[string]any{}},
					},
					"volumes": []any{
						map[string]any{"name": "os", "containerDisk": map[string]any{"image": tpl.Spec.Image}},
						map[string]any{"name": "home", "persistentVolumeClaim": map[string]any{"claimName": pvcName}},
					},
				},
			},
		},
	}}
	if err := controllerutil.SetControllerReference(ws, vm, r.Scheme()); err != nil {
		return false, fmt.Errorf("setting owner on virtualmachine %s: %w", name, err)
	}
	if err := r.Create(ctx, vm); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, fmt.Errorf("creating virtualmachine %s: %w", name, err)
	}
	return false, nil
}

// ensureService gives the workspace a stable in-cluster DNS name that guacd
// can dial regardless of pod restarts. For windows, KubeVirt stamps the
// workspace labels from the VM template onto the virt-launcher pod, so the
// same selector matches both cases.
func (r *WorkspaceReconciler) ensureService(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) error {
	name := computeName(ws)
	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Namespace: ws.Namespace, Name: name}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("fetching service %s: %w", name, err)
	}

	protocols := tpl.Spec.EffectiveProtocols()
	ports := make([]corev1.ServicePort, 0, len(protocols))
	for _, p := range protocols {
		ports = append(ports, corev1.ServicePort{
			Name:       p.Name,
			Port:       p.Port,
			TargetPort: intstr.FromInt32(p.Port),
		})
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ws.Namespace,
			Labels:    workspaceLabels(ws),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{labelWorkspace: ws.Name},
			Ports:    ports,
		},
	}
	if err := controllerutil.SetControllerReference(ws, svc, r.Scheme()); err != nil {
		return fmt.Errorf("setting owner on service %s: %w", name, err)
	}
	if err := r.Create(ctx, svc); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating service %s: %w", name, err)
	}
	return nil
}


// patchStatus re-fetches the workspace fresh before writing status, avoiding
// resource-version conflicts, and only ever uses the status subresource.
func (r *WorkspaceReconciler) patchStatus(ctx context.Context, ws *waasv1alpha1.Workspace, mutate func(*waasv1alpha1.WorkspaceStatus)) error {
	fresh := &waasv1alpha1.Workspace{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(ws), fresh); err != nil {
		return client.IgnoreNotFound(err)
	}
	mutate(&fresh.Status)
	fresh.Status.ObservedGeneration = fresh.Generation
	if err := r.Status().Update(ctx, fresh); err != nil {
		return fmt.Errorf("updating status of workspace %s: %w", ws.Name, err)
	}
	return nil
}

func (r *WorkspaceReconciler) setUnready(ctx context.Context, ws *waasv1alpha1.Workspace, phase waasv1alpha1.WorkspacePhase, reason, message string) error {
	return r.patchStatus(ctx, ws, func(st *waasv1alpha1.WorkspaceStatus) {
		st.Phase = phase
		setCondition(st, metav1.ConditionFalse, reason, message)
	})
}

func setCondition(st *waasv1alpha1.WorkspaceStatus, status metav1.ConditionStatus, reason, message string) {
	cond := metav1.Condition{
		Type:               waasv1alpha1.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
	for i, existing := range st.Conditions {
		if existing.Type == cond.Type {
			if existing.Status == cond.Status && existing.Reason == cond.Reason {
				return
			}
			st.Conditions[i] = cond
			return
		}
	}
	st.Conditions = append(st.Conditions, cond)
}

func computeName(ws *waasv1alpha1.Workspace) string {
	return "ws-" + ws.Name
}

func workspaceLabels(ws *waasv1alpha1.Workspace) map[string]string {
	return map[string]string{
		labelManagedBy: managerName,
		labelWorkspace: ws.Name,
		labelOwner:     ws.Spec.Owner,
	}
}

func podReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// SetupWithManager wires the controller. GenerationChangedPredicate keeps
// status-only updates from re-triggering reconciliation; owned pods and
// services still wake the controller on state changes.
func (r *WorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&waasv1alpha1.Workspace{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.Pod{}).
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Named("workspace").
		Complete(r)
}
