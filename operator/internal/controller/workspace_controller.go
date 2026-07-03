package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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
}

// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspacetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create
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

	pvcName, err := r.ensureHomePVC(ctx, ws, tpl)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}

	if ws.Spec.Paused {
		if err := r.teardownCompute(ctx, ws, tpl); err != nil {
			return ctrl.Result{}, fmt.Errorf("pausing workspace %s: %w", ws.Name, err)
		}
		if err := r.patchStatus(ctx, ws, func(st *waasv1alpha1.WorkspaceStatus) {
			st.Phase = waasv1alpha1.PhaseStopped
			st.OS = tpl.Spec.OS
			st.PVCName = pvcName
			st.Address, st.Port, st.Protocol = "", 0, ""
			setCondition(st, metav1.ConditionFalse, "Paused", "workspace is paused; home volume retained")
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	var ready bool
	if tpl.Spec.OS == waasv1alpha1.OSWindows {
		ready, err = r.ensureVirtualMachine(ctx, ws, tpl, pvcName)
	} else {
		ready, err = r.ensurePod(ctx, ws, tpl, pvcName)
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}
	if err := r.ensureService(ctx, ws, tpl); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
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
		st.Port = tpl.Spec.DesktopPort()
		st.Protocol = tpl.Spec.Protocol()
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
	return ctrl.Result{}, nil
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

// ensurePod creates the linux desktop pod if missing and reports readiness.
func (r *WorkspaceReconciler) ensurePod(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, pvcName string) (bool, error) {
	name := computeName(ws)
	existing := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Namespace: ws.Namespace, Name: name}, existing)
	if err == nil {
		return podReady(existing), nil
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("fetching pod %s: %w", name, err)
	}

	resources := tpl.Spec.Resources
	if ws.Spec.Resources != nil {
		resources = *ws.Spec.Resources
	}
	port := tpl.Spec.DesktopPort()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ws.Namespace,
			Labels:    workspaceLabels(ws),
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyAlways,
			Containers: []corev1.Container{{
				Name:      "desktop",
				Image:     tpl.Spec.Image,
				Env:       tpl.Spec.Env,
				Resources: resources,
				Ports:     []corev1.ContainerPort{{Name: "desktop", ContainerPort: port}},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "home",
					MountPath: homeMountPath,
				}},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)},
					},
					InitialDelaySeconds: 5,
					PeriodSeconds:       5,
				},
				LivenessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)},
					},
					InitialDelaySeconds: 30,
					PeriodSeconds:       10,
				},
			}},
			Volumes: []corev1.Volume{{
				Name: "home",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName},
				},
			}},
		},
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
func (r *WorkspaceReconciler) ensureVirtualMachine(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, pvcName string) (bool, error) {
	name := computeName(ws)
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(kubevirt.VirtualMachineGVK)
	err := r.Get(ctx, types.NamespacedName{Namespace: ws.Namespace, Name: name}, existing)
	if err == nil {
		ready, _, _ := unstructured.NestedBool(existing.Object, "status", "ready")
		return ready, nil
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
			"runStrategy": "Always",
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

	port := tpl.Spec.DesktopPort()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ws.Namespace,
			Labels:    workspaceLabels(ws),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{labelWorkspace: ws.Name},
			Ports: []corev1.ServicePort{{
				Name:       "desktop",
				Port:       port,
				TargetPort: intstr.FromInt32(port),
			}},
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

// teardownCompute removes the pod or VM of a paused workspace, keeping PVC
// and service (the service simply has no endpoints while paused).
func (r *WorkspaceReconciler) teardownCompute(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) error {
	name := computeName(ws)
	if tpl.Spec.OS == waasv1alpha1.OSWindows {
		vm := &unstructured.Unstructured{}
		vm.SetGroupVersionKind(kubevirt.VirtualMachineGVK)
		vm.SetNamespace(ws.Namespace)
		vm.SetName(name)
		if err := r.Delete(ctx, vm); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting virtualmachine %s: %w", name, err)
		}
		return nil
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ws.Namespace, Name: name}}
	if err := r.Delete(ctx, pod); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting pod %s: %w", name, err)
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
		Owns(&corev1.Service{}).
		Named("workspace").
		Complete(r)
}
