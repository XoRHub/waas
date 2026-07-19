package controller

// Failure detection: this file owns diagnosing WHY a not-ready workload
// is not ready. The ensure* functions in workload.go report convergence
// (a bare ready bool); when that says "not ready", Reconcile calls
// detectWorkloadFailure to distinguish "still coming up" (Provisioning)
// from a container error state the user must see (Failed): crash loops,
// image pull failures, config errors, evictions.

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// workloadFailure describes a container-level error state that blocks the
// desktop from ever becoming ready on its own.
type workloadFailure struct {
	Reason  string // CamelCase kubelet reason, doubles as condition/event reason
	Message string
}

// failureWaitingReasons are the kubelet waiting reasons that mean the
// container is stuck, not progressing. ContainerCreating, PodInitializing
// and in-progress pulls are deliberately absent: those are normal
// provisioning.
var failureWaitingReasons = map[string]bool{
	"CrashLoopBackOff":           true,
	"ImagePullBackOff":           true,
	"ErrImagePull":               true,
	"CreateContainerConfigError": true,
	"CreateContainerError":       true,
	"InvalidImageName":           true,
	"RunContainerError":          true,
}

// detectWorkloadFailure inspects the workspace's desktop pod(s) for error
// states. failure is nil while provisioning progresses normally; detail
// then carries non-fatal context worth surfacing in the Provisioning
// message (currently: the scheduler's Unschedulable explanation — the
// scheduler retries, autoscaling or preemption can resolve it, so it is
// not a failure).
func (r *WorkspaceReconciler) detectWorkloadFailure(ctx context.Context, ws *waasv1alpha1.Workspace) (failure *workloadFailure, detail string, err error) {
	pods := &corev1.PodList{}
	// Both labels: in a shared placement namespace the workspace name
	// alone could collide across CR namespaces.
	if err := r.List(ctx, pods,
		client.InNamespace(computeNamespace(ws)),
		client.MatchingLabels{labelWorkspace: ws.Name, labelWorkspaceNS: ws.Namespace}); err != nil {
		return nil, "", fmt.Errorf("listing pods of workspace %s: %w", ws.Name, err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		// An old pod draining during a Recreate rollout or manual reload
		// must not report its stale crash state.
		if pod.DeletionTimestamp != nil {
			continue
		}
		if f := podFailure(pod); f != nil {
			return f, "", nil
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == corev1.PodReasonUnschedulable {
				detail = cond.Message
			}
		}
	}
	return nil, detail, nil
}

// podFailure classifies one pod: a non-nil result means the pod is stuck
// in an error state; nil means it is running or provisioning normally.
func podFailure(pod *corev1.Pod) *workloadFailure {
	if pod.Status.Phase == corev1.PodFailed {
		reason := pod.Status.Reason // e.g. Evicted
		if reason == "" {
			reason = "PodFailed"
		}
		return &workloadFailure{
			Reason:  reason,
			Message: fmt.Sprintf("pod %q failed: %s", pod.Name, pod.Status.Message),
		}
	}
	for _, statuses := range [][]corev1.ContainerStatus{pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses} {
		for i := range statuses {
			if f := containerFailure(&statuses[i]); f != nil {
				return f
			}
		}
	}
	return nil
}

func containerFailure(cs *corev1.ContainerStatus) *workloadFailure {
	waiting := cs.State.Waiting
	if waiting == nil || !failureWaitingReasons[waiting.Reason] {
		return nil
	}
	msg := fmt.Sprintf("container %q: %s", cs.Name, waiting.Reason)
	if waiting.Message != "" {
		msg += ": " + waiting.Message
	}
	// The last termination tells the user WHY it crashes (OOMKilled, exit
	// code of the entrypoint) — the waiting state alone only says that it
	// does.
	if term := cs.LastTerminationState.Terminated; term != nil {
		msg += fmt.Sprintf(" (last exit code %d", term.ExitCode)
		if term.Reason != "" {
			msg += ", " + term.Reason
		}
		msg += fmt.Sprintf(", %d restarts)", cs.RestartCount)
	}
	return &workloadFailure{Reason: waiting.Reason, Message: msg}
}
