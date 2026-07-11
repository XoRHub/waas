package controller

import (
	"context"
	"fmt"
	"net"
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
	"github.com/xorhub/waas/operator/internal/metrics"
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
	// Probe checks that a TCP endpoint accepts connections (the
	// ConnectionReady condition); nil means a short net.Dial. Injectable
	// for tests.
	Probe func(addr string) error
	// PlatformNamespace is where guacd/wwt run (usually the Helm release
	// namespace, which may differ from the namespace holding the CRs).
	// The bootstrap NetworkPolicy of placed namespaces lets it in; empty
	// = assume everything runs beside the CRs.
	PlatformNamespace string
	// DefaultNamespacePattern is the operator-wide placement pattern
	// (WAAS_DEFAULT_NAMESPACE_PATTERN), applied when a template declares
	// none; empty = the built-in naming.BuiltinNamespacePattern. Only
	// affects NEW workspaces: existing ones carry their frozen
	// spec.targetNamespace.
	DefaultNamespacePattern string
}

func (r *WorkspaceReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *WorkspaceReconciler) probe(addr string) error {
	if r.Probe != nil {
		return r.Probe(addr)
	}
	conn, err := net.DialTimeout("tcp", addr, 750*time.Millisecond)
	if err == nil {
		conn.Close()
	}
	return err
}

// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspaces,verbs=get;list;watch;update;delete
// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspacetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;create;update;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;create;update;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=resourcequotas,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update

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
		// PVC has no owner reference either way: the finalizer applies the
		// user's deletion-time choice — delete it with the workspace
		// (explicit opt-in annotation) or detach it as a retained volume.
		if controllerutil.ContainsFinalizer(ws, finalizerTeardown) {
			if err := r.finalizeHomeVolume(ctx, ws); err != nil {
				r.reportTeardownFailure(ctx, ws, "finalizing home volume", err)
				return ctrl.Result{}, fmt.Errorf("finalizing home volume of %s: %w", ws.Name, err)
			}
			if err := r.teardownPlacement(ctx, ws); err != nil {
				r.reportTeardownFailure(ctx, ws, "tearing down placed objects", err)
				return ctrl.Result{}, fmt.Errorf("tearing down workspace %s: %w", ws.Name, err)
			}
			controllerutil.RemoveFinalizer(ws, finalizerTeardown)
			if err := r.Update(ctx, ws); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
		return ctrl.Result{}, nil
	}
	// Every workspace carries the teardown finalizer before any compute
	// exists: placed ones because cross-namespace objects would leak, and
	// ALL of them because the home volume's fate (delete vs retain) is
	// decided at deletion time and must go through the finalizer.
	if !controllerutil.ContainsFinalizer(ws, finalizerTeardown) {
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

	// Generated credentials (KasmVNC and vnc/rdp) must exist before the
	// workload: its VNC_PW secretKeyRef references the pod-namespace copy.
	if err := r.ensureKasmCredentials(ctx, ws, tpl); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}
	if err := r.ensureDesktopCredentials(ctx, ws, tpl); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}

	// User-level KasmVNC config: the ConfigMap must exist before the
	// workload that mounts it.
	if err := r.ensureKasmConfig(ctx, ws, tpl); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}

	// Private-registry pull credentials, fail-closed: a workspace whose
	// catalog entry references an unresolvable secret must say so instead
	// of crash-looping in ImagePullBackOff.
	if denial, err := r.ensurePullSecret(ctx, ws, tpl); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	} else if denial != nil {
		r.recordEvent(ws, corev1.EventTypeWarning, string(denial.Reason), denial.Message)
		if err := r.setUnready(ctx, ws, waasv1alpha1.PhaseFailed, string(denial.Reason), denial.Message); err != nil {
			return ctrl.Result{}, err
		}
		// The secret may arrive via GitOps; retry on the slow loop.
		return ctrl.Result{RequeueAfter: requeueMissing}, nil
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

	var ready, drifted, reloaded bool
	if tpl.Spec.OS == waasv1alpha1.OSWindows {
		// Windows/KubeVirt path: drift detection deliberately out of
		// scope (unstructured VM spec, path still untested end to end).
		ready, err = r.ensureVirtualMachine(ctx, ws, tpl, pvcName, !down)
	} else {
		ready, drifted, reloaded, err = r.ensureWorkload(ctx, ws, tpl, pvcName, down)
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
	}
	// The reload request is ONE-SHOT: consumed when applied, and cleared
	// just as well when there is nothing to apply (no drift, workspace
	// down — the boundary converges anyway, drift out of scope). A stale
	// request must never fire at some later resume.
	if reloadRequested(ws) && (reloaded || down || !drifted) {
		if reloaded {
			r.recordEvent(ws, corev1.EventTypeNormal, "WorkloadReloaded",
				"manual reload: the desktop restarts now on the up-to-date configuration")
		}
		if err := r.clearReloadRequest(ctx, ws); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling workspace %s: %w", ws.Name, err)
		}
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
		// Lifecycle milestone on the TRANSITION only — reconciles are
		// frequent, the event stream must not be.
		if ws.Status.Phase != phase {
			r.recordEvent(ws, corev1.EventTypeNormal, string(phase), condMsg)
		}
		if err := r.patchStatus(ctx, ws, func(st *waasv1alpha1.WorkspaceStatus) {
			st.Phase = phase
			st.OS = tpl.Spec.OS
			st.PVCName = pvcName
			st.Address, st.Port, st.Protocol = "", 0, ""
			st.Protocols = nil
			st.NextTransition = nextTransition
			setCondition(st, metav1.ConditionFalse, condReason, condMsg)
			setDriftCondition(st, drifted)
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
	// Drift milestone on the TRANSITION only: the events panel and the
	// card badge tell the user their workspace will restart with updates
	// at its next resume (docs/adr/0001).
	if drifted && !hasDriftCondition(ws) {
		r.recordEvent(ws, corev1.EventTypeNormal, "TemplateDrifted",
			fmt.Sprintf("desired configuration changed (template %q or workspace overrides): the workspace picks the new shape up at its next resume, or on manual reload", tpl.Name))
	}

	// Lifecycle milestones on transitions: with the events panel these
	// two lines make the CR tell its own story (provisioning started,
	// desktop up), between the admission events and the pods' own.
	if ws.Status.Phase != phase {
		switch phase {
		case waasv1alpha1.PhaseProvisioning:
			r.recordEvent(ws, corev1.EventTypeNormal, "Provisioning",
				fmt.Sprintf("provisioning desktop workload %s/%s", computeNamespace(ws), computeName(ws)))
		case waasv1alpha1.PhaseRunning:
			r.recordEvent(ws, corev1.EventTypeNormal, "Ready",
				fmt.Sprintf("desktop is up (%s protocol on port %d)", effectiveProtocol(ws, tpl).Name, effectiveProtocol(ws, tpl).Port))
		}
	}
	// ConnectionReady: pod readiness proves the container runs, not that
	// the desktop server LISTENS. Probe the service endpoint once the
	// workload reports ready; while it lags, requeue on a short loop.
	connReady := false
	var connReason, connMsg string
	def := effectiveProtocol(ws, tpl)
	switch {
	case !ready:
		connReason, connMsg = "DesktopDown", "workload is not ready"
	default:
		addr := fmt.Sprintf("%s.%s.svc.cluster.local:%d", computeName(ws), computeNamespace(ws), def.Port)
		if err := r.probe(addr); err != nil {
			connReason, connMsg = "DesktopNotListening", fmt.Sprintf("%s did not accept a TCP connection: %v", addr, err)
		} else {
			connReady, connReason, connMsg = true, "DesktopListening", fmt.Sprintf("desktop accepts connections on port %d (%s)", def.Port, def.Name)
		}
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
		connStatus := metav1.ConditionFalse
		if connReady {
			connStatus = metav1.ConditionTrue
		}
		setTypedCondition(st, waasv1alpha1.ConditionConnectionReady, connStatus, connReason, connMsg)
		setDriftCondition(st, drifted)
	}); err != nil {
		return ctrl.Result{}, err
	}

	if !ready {
		// Transient state: poll until the pod/VM reports ready.
		return ctrl.Result{RequeueAfter: earliestRequeue(requeueTransient, scheduleRequeue)}, nil
	}
	// Wake up at whichever comes first: the next schedule edge or the TTL.
	// Ready but the desktop server not accepting yet: bounded retry loop
	// until ConnectionReady turns true (rare beyond a few seconds).
	if ready && !connReady {
		return ctrl.Result{RequeueAfter: earliestRequeue(10*time.Second, earliestRequeue(scheduleRequeue, lifetimeRequeue))}, nil
	}
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

// clearReloadRequest consumes the one-shot reload annotation. Fresh
// fetch: the CR may have been updated earlier in this reconcile.
func (r *WorkspaceReconciler) clearReloadRequest(ctx context.Context, ws *waasv1alpha1.Workspace) error {
	fresh := &waasv1alpha1.Workspace{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(ws), fresh); err != nil {
		return client.IgnoreNotFound(err)
	}
	if _, ok := fresh.Annotations[waasv1alpha1.AnnotationReloadRequestedAt]; !ok {
		return nil
	}
	delete(fresh.Annotations, waasv1alpha1.AnnotationReloadRequestedAt)
	if err := r.Update(ctx, fresh); err != nil {
		return fmt.Errorf("clearing reload annotation on %s: %w", ws.Name, err)
	}
	return nil
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

// homePVCName is the workspace's effective home volume: an adopted
// retained volume (spec.homeVolumeName, webhook-vetted) or the derived
// "<workloadName>-home".
func homePVCName(ws *waasv1alpha1.Workspace) string {
	if ws.Spec.HomeVolumeName != "" {
		return ws.Spec.HomeVolumeName
	}
	return computeName(ws) + "-home"
}

// finalizeHomeVolume applies the deletion-time choice to the home PVC:
// the explicit delete-home annotation deletes it with the workspace;
// otherwise it is DETACHED — marked retained, stamped with provenance,
// still owned by the user and still counted against their storage quota.
func (r *WorkspaceReconciler) finalizeHomeVolume(ctx context.Context, ws *waasv1alpha1.Workspace) error {
	key := types.NamespacedName{Namespace: computeNamespace(ws), Name: homePVCName(ws)}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Get(ctx, key, pvc); err != nil {
		return client.IgnoreNotFound(err)
	}
	// Ownership guard: only touch a PVC that still belongs to THIS
	// workspace. If the name was reused (delete + recreate racing through
	// the finalizer), the volume under that name is the NEW workspace's
	// home — deleting or relabeling it here would destroy someone else's
	// live data.
	if pvc.Labels[labelWorkspace] != ws.Name {
		return nil
	}
	if ws.Annotations[waasv1alpha1.AnnotationDeleteHome] == "true" {
		if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting home pvc %s: %w", key.Name, err)
		}
		r.recordEvent(ws, corev1.EventTypeNormal, "HomeVolumeDeleted",
			fmt.Sprintf("home volume %q deleted with the workspace (explicit user choice)", key.Name))
		return nil
	}
	if pvc.Labels == nil {
		pvc.Labels = map[string]string{}
	}
	pvc.Labels[waasv1alpha1.LabelRetained] = "true"
	// The per-workspace labels point at a CR about to vanish; provenance
	// moves to annotations, ownership (owner + managed-by) stays.
	delete(pvc.Labels, labelWorkspace)
	delete(pvc.Labels, labelWorkspaceNS)
	if pvc.Annotations == nil {
		pvc.Annotations = map[string]string{}
	}
	origin := ws.Spec.DisplayName
	if origin == "" {
		origin = ws.Name
	}
	pvc.Annotations[waasv1alpha1.AnnotationOriginWorkspace] = origin
	pvc.Annotations[waasv1alpha1.AnnotationRetainedAt] = r.now().UTC().Format(time.RFC3339)
	if err := r.Update(ctx, pvc); err != nil {
		return fmt.Errorf("detaching home pvc %s: %w", key.Name, err)
	}
	r.recordEvent(ws, corev1.EventTypeNormal, "HomeVolumeRetained",
		fmt.Sprintf("home volume %q retained (still counts against the owner's storage quota)", key.Name))
	return nil
}

// reportTeardownFailure makes a failing finalizer VISIBLE: Kubernetes
// Event (Warning) plus a Ready condition on the CR, so a workspace stuck
// in Terminating explains itself in kubectl describe and on the portal
// instead of retrying silently forever. The finalizer is never removed
// automatically — that would trade a visible stuck deletion for a silent
// leak; docs/workspace-deletion.md documents the manual unblock.
func (r *WorkspaceReconciler) reportTeardownFailure(ctx context.Context, ws *waasv1alpha1.Workspace, stage string, err error) {
	msg := fmt.Sprintf("%s failed: %v — deletion is retried with backoff; see docs/workspace-deletion.md to unblock", stage, err)
	metrics.TeardownFailures.Inc()
	r.recordEvent(ws, corev1.EventTypeWarning, "TeardownFailed", msg)
	// Best-effort: the CR is going away, a lost status write must not
	// mask the original teardown error.
	_ = r.patchStatus(ctx, ws, func(st *waasv1alpha1.WorkspaceStatus) {
		st.Phase = waasv1alpha1.PhaseTerminating
		setCondition(st, metav1.ConditionFalse, "TeardownFailed", msg)
	})
}

// ensureHomePVC creates the user-state volume if missing, or ADOPTS the
// retained volume named by spec.homeVolumeName (relabeled live again).
// Idempotent, and the PVC is deliberately not owned by the Workspace so
// state survives deletion.
func (r *WorkspaceReconciler) ensureHomePVC(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) (string, error) {
	name := homePVCName(ws)
	existing := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Namespace: computeNamespace(ws), Name: name}, existing)
	if err == nil {
		// A terminating PVC (previous same-named workspace deleted WITH
		// its volume, pvc-protection still draining) must never be
		// mounted: wait for the name to free up, then create fresh.
		if !existing.DeletionTimestamp.IsZero() {
			return "", fmt.Errorf("home pvc %s is terminating; waiting for the name to be released", name)
		}
		// Never adopt someone else's volume: in a SHARED namespace two
		// users' display names can collide on the same derived PVC name
		// (the api-server suffixes new names, but defense in depth).
		if owner := existing.Labels[waasv1alpha1.LabelOwner]; owner != "" && owner != ws.Spec.Owner {
			return "", fmt.Errorf("home pvc %s belongs to another owner; refusing to adopt", name)
		}
		// Adoption: a retained volume becoming a live home again sheds its
		// retained marker and regains the per-workspace labels.
		if existing.Labels[waasv1alpha1.LabelRetained] == "true" || existing.Labels[labelWorkspace] != ws.Name {
			if existing.Labels == nil {
				existing.Labels = map[string]string{}
			}
			for k, v := range workspaceLabels(ws) {
				existing.Labels[k] = v
			}
			delete(existing.Labels, waasv1alpha1.LabelRetained)
			delete(existing.Annotations, waasv1alpha1.AnnotationRetainedAt)
			if err := r.Update(ctx, existing); err != nil {
				return "", fmt.Errorf("adopting pvc %s: %w", name, err)
			}
		}
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
func (r *WorkspaceReconciler) ensurePod(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, pvcName string, paused bool) (bool, bool, bool, error) {
	name := computeName(ws)
	existing := &corev1.Pod{}
	err := r.Get(ctx, computeKey(ws), existing)
	if err == nil {
		// A bare Pod has no replica count: "scale to 0" means delete it
		// (state lives on the home PVC, so resume recreates it cleanly).
		if paused {
			if delErr := r.Delete(ctx, existing); delErr != nil && !apierrors.IsNotFound(delErr) {
				return false, false, false, fmt.Errorf("pausing pod %s: %w", name, delErr)
			}
			return false, false, false, nil
		}
		// A bare Pod converges by recreation only (docs/adr/0001): a
		// template edit while it runs is REPORTED as drift and applies
		// at the next pause/resume, which rebuilds the pod from scratch.
		desired := r.buildPodTemplate(ctx, ws, tpl, pvcName)
		drifted := existing.Annotations[annotationPodTemplateHash] != desired.Annotations[annotationPodTemplateHash]
		// Manual reload: recreation IS a bare Pod's only boundary — delete
		// now, the workload watch requeues and the next reconcile rebuilds
		// the pod from the up-to-date template.
		if drifted && reloadRequested(ws) {
			if delErr := r.Delete(ctx, existing); delErr != nil && !apierrors.IsNotFound(delErr) {
				return false, false, false, fmt.Errorf("reloading pod %s: %w", name, delErr)
			}
			return false, false, true, nil
		}
		return podReady(existing), drifted, false, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, false, false, fmt.Errorf("fetching pod %s: %w", name, err)
	}
	// Not found + paused: nothing to run, stay down.
	if paused {
		return false, false, false, nil
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
		return false, false, false, fmt.Errorf("setting owner on pod %s: %w", name, err)
	}
	if err := r.Create(ctx, pod); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, false, false, fmt.Errorf("creating pod %s: %w", name, err)
	}
	return false, false, false, nil
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
	ports := desiredServicePorts(tpl)
	existing := &corev1.Service{}
	err := r.Get(ctx, computeKey(ws), existing)
	if err == nil {
		// Ports converge on every reconcile: a template gaining a
		// protocol or the audio port after the Service was created must
		// reach existing workspaces without recreating them. Deliberately
		// scoped to Spec.Ports — the rest of the Service (and the wider
		// create-only podTemplate gap, see docs/studies/audit-2026-07.md)
		// stays as-is.
		if servicePortsEqual(existing.Spec.Ports, ports) {
			return nil
		}
		existing.Spec.Ports = ports
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("updating ports of service %s: %w", name, err)
		}
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("fetching service %s: %w", name, err)
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

// desiredServicePorts is one ServicePort per declared protocol, plus the
// PulseAudio port when a protocol exposes it — the Service mirrors the
// container ports 1:1.
func desiredServicePorts(tpl *waasv1alpha1.WorkspaceTemplate) []corev1.ServicePort {
	protocols := tpl.Spec.EffectiveProtocols()
	ports := make([]corev1.ServicePort, 0, len(protocols)+1)
	for _, p := range protocols {
		ports = append(ports, corev1.ServicePort{
			Name:       p.Name,
			Port:       p.Port,
			TargetPort: intstr.FromInt32(p.Port),
		})
	}
	if tpl.Spec.AudioPortExposed() {
		ports = append(ports, corev1.ServicePort{
			Name:       audioPortName,
			Port:       waasv1alpha1.PulseAudioPort,
			TargetPort: intstr.FromInt32(waasv1alpha1.PulseAudioPort),
		})
	}
	return ports
}

// servicePortsEqual compares the facets desiredServicePorts sets, mindful
// that the API server defaults Protocol to TCP on live objects — a naive
// DeepEqual against our TCP-implicit desired list would update forever.
func servicePortsEqual(live, desired []corev1.ServicePort) bool {
	if len(live) != len(desired) {
		return false
	}
	for i := range desired {
		if live[i].Name != desired[i].Name ||
			live[i].Port != desired[i].Port ||
			live[i].TargetPort != desired[i].TargetPort ||
			(live[i].Protocol != "" && live[i].Protocol != corev1.ProtocolTCP) {
			return false
		}
	}
	return true
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
	// Conditions follow the Kubernetes convention: ObservedGeneration is
	// the generation the condition was last evaluated against — every
	// reconcile evaluates them, so stamp them all.
	for i := range fresh.Status.Conditions {
		fresh.Status.Conditions[i].ObservedGeneration = fresh.Generation
	}
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
	setTypedCondition(st, waasv1alpha1.ConditionReady, status, reason, message)
}

// setDriftCondition reports docs/adr/0001 drift: True = a pending
// configuration change (template edit or workspace override update)
// awaits the next scale-up boundary or a manual reload. The condition
// type and reason are kept for API compatibility even though the cause
// may be the workspace's own overrides.
func setDriftCondition(st *waasv1alpha1.WorkspaceStatus, drifted bool) {
	if drifted {
		setTypedCondition(st, waasv1alpha1.ConditionTemplateDrifted, metav1.ConditionTrue,
			"TemplateChanged", "the desired configuration (template or overrides) changed since this workspace started; the new shape applies at the next resume or on manual reload (the desktop will restart with updates)")
		return
	}
	setTypedCondition(st, waasv1alpha1.ConditionTemplateDrifted, metav1.ConditionFalse,
		"InSync", "the workload matches its desired configuration")
}

// hasDriftCondition reads the PREVIOUS reconcile's verdict (event
// emission on transitions only).
func hasDriftCondition(ws *waasv1alpha1.Workspace) bool {
	for i := range ws.Status.Conditions {
		c := &ws.Status.Conditions[i]
		if c.Type == waasv1alpha1.ConditionTemplateDrifted {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

func setTypedCondition(st *waasv1alpha1.WorkspaceStatus, condType string, status metav1.ConditionStatus, reason, message string) {
	cond := metav1.Condition{
		Type:               condType,
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

// SetupWithManager wires the controller. The Workspace predicate fires on
// spec changes (generation) OR annotation changes — a manual resume of a
// cron-Stopped workspace only touches the manual-state-at annotation
// (spec.paused is already false), and filtering it out would leave the
// workspace down until the next scheduled edge. Status-only updates stay
// filtered. Workloads are mapped back to their Workspace through the
// platform labels rather than Owns(): placed workloads live in another
// namespace and cannot carry an owner reference (the labels cover the
// legacy same-namespace objects too). Templates are watched too: drift
// (docs/adr/0001) must be DETECTED when the template is edited — a
// running workspace has no timed requeue, so without this watch the
// TemplateDrifted condition (and the portal badge) would wait for some
// unrelated event instead of appearing right away.
func (r *WorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mapFn := handler.EnqueueRequestsFromMapFunc(r.mapObjectToWorkspace)
	wsPredicate := predicate.Or[client.Object](
		predicate.GenerationChangedPredicate{},
		predicate.AnnotationChangedPredicate{},
	)
	return ctrl.NewControllerManagedBy(mgr).
		For(&waasv1alpha1.Workspace{}, builder.WithPredicates(wsPredicate)).
		Watches(&corev1.Pod{}, mapFn).
		Watches(&appsv1.Deployment{}, mapFn).
		Watches(&appsv1.StatefulSet{}, mapFn).
		Watches(&corev1.Service{}, mapFn).
		// Spec edits only (generation): status/metadata churn on a
		// template must not re-reconcile its whole fleet.
		Watches(&waasv1alpha1.WorkspaceTemplate{},
			handler.EnqueueRequestsFromMapFunc(r.mapTemplateToWorkspaces),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Catalog entries feed the pod template too (arch affinity, pull
		// secret): an edit must re-evaluate drift as well. Which templates
		// an entry governs is a catalog-GLOBAL question (exact match, else
		// best registry-prefix across all entries — adding or removing one
		// can change another entry's matches), so any catalog spec change
		// re-enqueues the namespace's whole fleet: catalog edits are rare
		// admin operations and an in-sync reconcile is a cheap no-op.
		Watches(&waasv1alpha1.WorkspaceImage{},
			handler.EnqueueRequestsFromMapFunc(r.mapCatalogToWorkspaces),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("workspace").
		Complete(r)
}

// mapTemplateToWorkspaces enqueues every workspace stamped from the
// edited template (same namespace, spec.templateRef match): each one
// re-evaluates its drift fingerprint against the new shape. Also covers
// the GitOps ordering case — a template applied AFTER its workspaces
// unblocks them immediately instead of on the retry loop.
func (r *WorkspaceReconciler) mapTemplateToWorkspaces(ctx context.Context, obj client.Object) []ctrl.Request {
	list := &waasv1alpha1.WorkspaceList{}
	if err := r.List(ctx, list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var reqs []ctrl.Request
	for i := range list.Items {
		if list.Items[i].Spec.TemplateRef == obj.GetName() {
			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
	}
	return reqs
}

// mapCatalogToWorkspaces enqueues the namespace's whole fleet on a
// catalog edit — see the Watches comment: best-prefix resolution makes
// the affected set non-local to the edited entry, and over-enqueueing
// only costs no-op reconciles on rare admin operations.
func (r *WorkspaceReconciler) mapCatalogToWorkspaces(ctx context.Context, obj client.Object) []ctrl.Request {
	list := &waasv1alpha1.WorkspaceList{}
	if err := r.List(ctx, list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	reqs := make([]ctrl.Request, 0, len(list.Items))
	for i := range list.Items {
		reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return reqs
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
