package controller

// The namespace janitor reclaims operator-created namespaces whose
// cleanup policy (frozen in the waas.xorhub.io/cleanup label at creation)
// is DeleteWhenEmpty, once nothing waas-managed lives in them anymore.
//
// It exists because emptiness cannot be decided synchronously in the
// workspace finalizer: content deletion is asynchronous — the home PVC
// stays Terminating under kubernetes.io/pvc-protection until the desktop
// pod is gone, pods have grace periods — so a check made while the
// finalizer runs reliably sees leftovers and would have to give up
// (that was exactly the orphaned-namespace bug). The janitor is instead
// re-triggered by the DELETION EVENTS of the managed content: whenever
// the last waas object of a namespace actually disappears — including a
// retained volume removed weeks later through the volumes API — the
// namespace is re-evaluated and reclaimed.
//
// It is an internal mechanism of the operator binary: one more reconciler
// on the shared manager, not a separate service or deployment.

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// NamespaceJanitor reconciles operator-created namespaces against their
// frozen cleanup policy. It only ever DELETES namespaces; creation and
// bootstrap stay with the workspace reconciler.
type NamespaceJanitor struct {
	client.Client
	// KubeVirtAvailable gates the VirtualMachine WATCH only (watching an
	// unregistered kind fails at startup). The emptiness check always
	// probes VMs and tolerates the kind being absent.
	KubeVirtAvailable bool
	Recorder          record.EventRecorder
}

// Reconcile re-evaluates one namespace. Idempotent and conservative:
// any doubt (unlabeled namespace, remaining workspace, remaining managed
// object) keeps the namespace; only a provably empty DeleteWhenEmpty
// namespace is deleted.
func (j *NamespaceJanitor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ns := &corev1.Namespace{}
	if err := j.Get(ctx, types.NamespacedName{Name: req.Name}, ns); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !ns.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	// Never touch namespaces the operator did not create, and never guess
	// a policy: a managed namespace without the cleanup label predates the
	// label and is treated as Retain (the audit script surfaces those for
	// a human decision — auto-deleting them could destroy admin-tuned
	// namespaces).
	if ns.Labels[labelManagedBy] != managerName ||
		ns.Labels[waasv1alpha1.LabelCleanup] != string(waasv1alpha1.CleanupDeleteWhenEmpty) {
		return ctrl.Result{}, nil
	}

	empty, err := j.isEmpty(ctx, ns.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("evaluating namespace %s: %w", ns.Name, err)
	}
	if !empty {
		// No requeue: the deletion event of whatever is still inside
		// re-triggers this reconcile.
		return ctrl.Result{}, nil
	}
	if err := j.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("deleting empty namespace %s: %w", ns.Name, err)
	}
	if j.Recorder != nil {
		j.Recorder.Event(ns, corev1.EventTypeNormal, "NamespaceReclaimed",
			fmt.Sprintf("namespace %q is empty and its cleanup policy is DeleteWhenEmpty; deleting it (quota and network policy cascade with it)", ns.Name))
	}
	return ctrl.Result{}, nil
}

// isEmpty reports whether nothing waas-managed lives in the namespace:
// no Workspace (live OR terminating — its finalizer still needs the
// namespace) targets it, and none of the managed content types has an
// instance left. Retained home PVCs keep the managed-by label on
// purpose, so a namespace holding user state is never reclaimed.
func (j *NamespaceJanitor) isEmpty(ctx context.Context, name string) (bool, error) {
	workspaces := &waasv1alpha1.WorkspaceList{}
	if err := j.List(ctx, workspaces); err != nil {
		return false, fmt.Errorf("listing workspaces: %w", err)
	}
	for i := range workspaces.Items {
		if workspaces.Items[i].EffectiveTargetNamespace() == name {
			return false, nil
		}
	}
	for _, gvk := range waasv1alpha1.WorkspaceContentGVKs() {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gvk)
		// Secrets need the full list: the shared pull-secret copies are
		// managed-by us but are NOT workspace content — alone, they must
		// never pin a DeleteWhenEmpty namespace (the namespace cascade
		// reclaims them).
		opts := []client.ListOption{client.InNamespace(name), client.MatchingLabels{labelManagedBy: managerName}}
		if gvk.Kind != "Secret" {
			opts = append(opts, client.Limit(1))
		}
		err := j.List(ctx, list, opts...)
		if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			continue // optional kind (KubeVirt) not installed
		}
		if err != nil {
			return false, fmt.Errorf("listing %s in %s: %w", gvk.Kind, name, err)
		}
		for i := range list.Items {
			if gvk.Kind == "Secret" && list.Items[i].GetLabels()[waasv1alpha1.LabelPullSecret] == "true" {
				continue
			}
			return false, nil
		}
	}
	return true, nil
}

// SetupWithManager wires the janitor: it watches the managed namespaces
// themselves (startup pass included) and maps every managed content
// object and Workspace back to the namespace it may be holding open —
// the DELETE events of those are what re-evaluates a namespace once its
// content is actually gone.
func (j *NamespaceJanitor) SetupWithManager(mgr ctrl.Manager) error {
	mapToNamespace := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []ctrl.Request {
		if obj.GetLabels()[labelManagedBy] != managerName || obj.GetNamespace() == "" {
			return nil
		}
		return []ctrl.Request{{NamespacedName: types.NamespacedName{Name: obj.GetNamespace()}}}
	})
	mapWorkspace := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []ctrl.Request {
		ws, ok := obj.(*waasv1alpha1.Workspace)
		if !ok {
			return nil
		}
		target := ws.EffectiveTargetNamespace()
		if target == ws.Namespace {
			return nil // not placed: its namespace is the platform's
		}
		return []ctrl.Request{{NamespacedName: types.NamespacedName{Name: target}}}
	})
	managedNS := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetLabels()[labelManagedBy] == managerName
	})

	b := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}, builder.WithPredicates(managedNS)).
		Watches(&corev1.Pod{}, mapToNamespace).
		Watches(&appsv1.Deployment{}, mapToNamespace).
		Watches(&appsv1.StatefulSet{}, mapToNamespace).
		Watches(&corev1.Service{}, mapToNamespace).
		Watches(&corev1.PersistentVolumeClaim{}, mapToNamespace).
		Watches(&waasv1alpha1.Workspace{}, mapWorkspace).
		Named("namespace-janitor")
	if j.KubeVirtAvailable {
		vm := &unstructured.Unstructured{}
		vm.SetGroupVersionKind(waasv1alpha1.VirtualMachineGVK)
		b = b.Watches(vm, mapToNamespace)
	}
	return b.Complete(j)
}
