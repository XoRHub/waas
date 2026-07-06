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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/internal/kubevirt"
	"github.com/xorhub/waas/operator/pkg/schedule"
)

const (
	labelManagedBy   = waasv1alpha1.LabelManagedBy
	labelWorkspace   = waasv1alpha1.LabelWorkspace
	labelWorkspaceNS = waasv1alpha1.LabelWorkspaceNamespace
	labelOwner       = waasv1alpha1.LabelOwner
	managerName      = waasv1alpha1.ManagerName

	requeueTransient = 5 * time.Second
	requeueMissing   = 30 * time.Second

	defaultHomeSize = "10Gi"
	homeMountPath   = "/home/user"

	// finalizerTeardown marks workspaces whose workloads live outside the
	// CR's namespace: owner references are illegal across namespaces, so
	// the operator deletes those objects explicitly at workspace deletion.
	finalizerTeardown = "waas.xorhub.io/teardown"
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
	// Now is the reconciler's clock; nil means time.Now. Injectable so
	// schedule transitions are testable at a chosen instant.
	Now func() time.Time
}

func (r *WorkspaceReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspaces,verbs=get;list;watch;update;delete
// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspacetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=resourcequotas,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create

// Reconcile drives a Workspace toward its desired state.
func (r *WorkspaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ws := &waasv1alpha1.Workspace{}
	if err := r.Get(ctx, req.NamespacedName, ws); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !ws.DeletionTimestamp.IsZero() {
		// Same-namespace compute (pod/VM/service) is garbage-collected
		// through owner references. Cross-namespace workloads cannot carry
		// one: the teardown finalizer deletes them explicitly. The home
		// PVC is deliberately skipped in BOTH paths: user state must
		// survive workspace deletion and recreation.
		if controllerutil.ContainsFinalizer(ws, finalizerTeardown) {
			if err := r.teardownPlacement(ctx, ws); err != nil {
				return ctrl.Result{}, fmt.Errorf("tearing down workspace %s: %w", ws.Name, err)
			}
			controllerutil.RemoveFinalizer(ws, finalizerTeardown)
			if err := r.Update(ctx, ws); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
		return ctrl.Result{}, nil
	}
	// Placed workspaces need the teardown finalizer before any compute
	// exists, or a fast create+delete could leak cross-namespace objects.
	if computeNamespace(ws) != ws.Namespace && !controllerutil.ContainsFinalizer(ws, finalizerTeardown) {
		controllerutil.AddFinalizer(ws, finalizerTeardown)
		if err := r.Update(ctx, ws); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer to workspace %s: %w", ws.Name, err)
		}
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

	// Placement first: the target namespace (and its bootstrap: Pod
	// Security labels, ResourceQuota, default NetworkPolicy) must exist
	// before anything lands in it.
	if err := r.ensureNamespace(ctx, ws, tpl); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}

	pvcName, err := r.ensureHomePVC(ctx, ws, tpl)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}

	// Effective down-state = manual pause OR a scheduled downtime window,
	// resolved by conflict rule B (see pkg/schedule). Down = scale to 0,
	// NOT delete: the workload object and config are kept so resume is a
	// fast scale back to 1. The home volume is retained either way.
	sched := effectiveSchedule(ws, tpl)
	now := r.now()
	decision := sched.Resolve(now, ws.Spec.Paused, manualStateAt(ws))
	down := decision.Down
	nextTransition := transitionStatus(decision.NextEdge)
	// Requeue when the next scheduled edge fires so the transition is
	// applied on time.
	var scheduleRequeue time.Duration
	if decision.NextEdge != nil {
		if d := decision.NextEdge.Time.Sub(now); d > 0 {
			scheduleRequeue = d
		}
	}

	var ready bool
	if tpl.Spec.OS == waasv1alpha1.OSWindows {
		ready, err = r.ensureVirtualMachine(ctx, ws, tpl, pvcName, !down)
	} else {
		ready, err = r.ensureWorkload(ctx, ws, tpl, pvcName, down)
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}
	// The Service is kept across pause so the in-cluster DNS name stays
	// stable and resume needs no endpoint churn.
	if err := r.ensureService(ctx, ws, tpl); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}

	if down {
		phase := waasv1alpha1.PhaseStopped
		condReason, condMsg := "ScheduledDowntime", "workspace is in a scheduled downtime window (scaled to 0); resumes at the next uptime edge"
		if decision.Manual {
			phase = waasv1alpha1.PhasePaused
			condReason, condMsg = "Paused", "workspace is paused (scaled to 0); workload object and home volume retained"
		}
		if err := r.patchStatus(ctx, ws, func(st *waasv1alpha1.WorkspaceStatus) {
			st.Phase = phase
			st.OS = tpl.Spec.OS
			st.PVCName = pvcName
			st.Address, st.Port, st.Protocol = "", 0, ""
			st.Protocols = nil
			st.NextTransition = nextTransition
			setCondition(st, metav1.ConditionFalse, condReason, condMsg)
		}); err != nil {
			return ctrl.Result{}, err
		}
		// Requeue at the next schedule edge, or on the TTL loop.
		return ctrl.Result{RequeueAfter: earliestRequeue(scheduleRequeue, lifetimeRequeue)}, nil
	}

	phase := waasv1alpha1.PhaseProvisioning
	if ready {
		phase = waasv1alpha1.PhaseRunning
	}
	if err := r.patchStatus(ctx, ws, func(st *waasv1alpha1.WorkspaceStatus) {
		st.Phase = phase
		st.OS = tpl.Spec.OS
		st.PVCName = pvcName
		st.Address = fmt.Sprintf("%s.%s.svc.cluster.local", computeName(ws), computeNamespace(ws))
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
		st.NextTransition = nextTransition
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
		return ctrl.Result{RequeueAfter: earliestRequeue(requeueTransient, scheduleRequeue)}, nil
	}
	// Wake up at whichever comes first: the next schedule edge or the TTL.
	if rq := earliestRequeue(scheduleRequeue, lifetimeRequeue); rq > 0 {
		return ctrl.Result{RequeueAfter: rq}, nil
	}
	return ctrl.Result{}, nil
}

// effectiveSchedule resolves the workspace's schedule: its override when
// present (the webhook vets the "schedule" override right), else the
// template's.
func effectiveSchedule(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) schedule.Spec {
	src := tpl.Spec.Schedule
	if ws.Spec.Overrides != nil && ws.Spec.Overrides.Schedule != nil {
		src = ws.Spec.Overrides.Schedule
	}
	if src == nil {
		return schedule.Spec{}
	}
	return schedule.Spec{Timezone: src.Timezone, Uptime: src.Uptime, Downtime: src.Downtime}
}

// manualStateAt reads the api-server's manual pause/resume timestamp.
func manualStateAt(ws *waasv1alpha1.Workspace) *time.Time {
	v := ws.Annotations[waasv1alpha1.AnnotationManualStateAt]
	if v == "" {
		return nil
	}
	ts, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil
	}
	return &ts
}

func transitionStatus(e *schedule.Edge) *waasv1alpha1.ScheduledTransition {
	if e == nil {
		return nil
	}
	return &waasv1alpha1.ScheduledTransition{Time: metav1.NewTime(e.Time), Up: e.Up}
}

// earliestRequeue returns the smallest strictly-positive duration, or 0
// when none is positive (meaning "no timed requeue").
func earliestRequeue(durs ...time.Duration) time.Duration {
	var best time.Duration
	for _, d := range durs {
		if d > 0 && (best == 0 || d < best) {
			best = d
		}
	}
	return best
}

// computeExists reports whether the workspace already has its workload
// (linux) or VM (windows) — i.e. whether capacity was already granted.
func (r *WorkspaceReconciler) computeExists(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) (bool, error) {
	name := computeKey(ws)
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
	err := r.Get(ctx, types.NamespacedName{Namespace: computeNamespace(ws), Name: name}, existing)
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
			Namespace: computeNamespace(ws),
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
	err := r.Get(ctx, computeKey(ws), existing)
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
			Name:        name,
			Namespace:   computeNamespace(ws),
			Labels:      template.Labels,
			Annotations: template.Annotations,
		},
		Spec: template.Spec,
	}
	if err := r.setOwnerIfLocal(ws, pod); err != nil {
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
	err := r.Get(ctx, computeKey(ws), existing)
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
			"namespace": computeNamespace(ws),
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
	if err := r.setOwnerIfLocal(ws, vm); err != nil {
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
	err := r.Get(ctx, computeKey(ws), existing)
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
			Namespace: computeNamespace(ws),
			Labels:    workspaceLabels(ws),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{labelWorkspace: ws.Name},
			Ports:    ports,
		},
	}
	if err := r.setOwnerIfLocal(ws, svc); err != nil {
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
	return ws.EffectiveWorkloadName()
}

// computeNamespace is where the workloads live; the CR itself always
// stays in the platform namespace.
func computeNamespace(ws *waasv1alpha1.Workspace) string {
	return ws.EffectiveTargetNamespace()
}

func computeKey(ws *waasv1alpha1.Workspace) types.NamespacedName {
	return types.NamespacedName{Namespace: computeNamespace(ws), Name: computeName(ws)}
}

func workspaceLabels(ws *waasv1alpha1.Workspace) map[string]string {
	return map[string]string{
		labelManagedBy:   managerName,
		labelWorkspace:   ws.Name,
		labelWorkspaceNS: ws.Namespace,
		labelOwner:       ws.Spec.Owner,
	}
}

// setOwnerIfLocal puts the usual controller reference on obj — unless the
// workload is placed in another namespace, where owner references are
// illegal; the teardown finalizer covers deletion there instead.
func (r *WorkspaceReconciler) setOwnerIfLocal(ws *waasv1alpha1.Workspace, obj client.Object) error {
	if obj.GetNamespace() != ws.Namespace {
		return nil
	}
	return controllerutil.SetControllerReference(ws, obj, r.Scheme())
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
// status-only updates from re-triggering reconciliation. Workloads are
// mapped back to their Workspace through the platform labels rather than
// Owns(): placed workloads live in another namespace and cannot carry an
// owner reference (the labels cover the legacy same-namespace objects too).
func (r *WorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mapFn := handler.EnqueueRequestsFromMapFunc(r.mapObjectToWorkspace)
	return ctrl.NewControllerManagedBy(mgr).
		For(&waasv1alpha1.Workspace{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&corev1.Pod{}, mapFn).
		Watches(&appsv1.Deployment{}, mapFn).
		Watches(&appsv1.StatefulSet{}, mapFn).
		Watches(&corev1.Service{}, mapFn).
		Named("workspace").
		Complete(r)
}

// mapObjectToWorkspace resolves the Workspace behind a watched workload:
// the CR name comes from the workspace label, the CR namespace from the
// workspace-namespace label (falling back to the object's own namespace
// for legacy objects, which always sit beside their CR).
func (r *WorkspaceReconciler) mapObjectToWorkspace(_ context.Context, obj client.Object) []ctrl.Request {
	labels := obj.GetLabels()
	if labels[labelManagedBy] != managerName || labels[labelWorkspace] == "" {
		return nil
	}
	ns := labels[labelWorkspaceNS]
	if ns == "" {
		ns = obj.GetNamespace()
	}
	return []ctrl.Request{{NamespacedName: types.NamespacedName{Namespace: ns, Name: labels[labelWorkspace]}}}
}
