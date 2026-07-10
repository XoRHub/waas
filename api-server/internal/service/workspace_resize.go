package service

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/xorhub/waas/api-server/internal/apierror"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// PodExecutor runs a fixed command inside a workspace pod — the narrow
// abstraction over pods/exec that the resize feature needs. Injected so
// dev mode (no cluster) and tests run without SPDY.
type PodExecutor interface {
	Exec(ctx context.Context, namespace, pod, container string, command []string) error
}

// WithPodExecutor wires the pods/exec implementation (same optional
// setter style as WithRemoteWorkspaces). Nil = the endpoint answers 503.
func (s *WorkspaceService) WithPodExecutor(exec PodExecutor) *WorkspaceService {
	s.exec = exec
	return s
}

// ResizeInput is the desired desktop geometry, in pixels.
type ResizeInput struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Resize bounds: wide enough for an 8K display, tight enough that a
// bogus client cannot ask Xvnc for an absurd framebuffer. Defense in
// depth — waas-resize re-validates its argument format in the pod.
const (
	minResizeDimension = 100
	maxResizeDimension = 7680
)

// Resize changes the RUNNING desktop's resolution by executing the
// image's waas-resize helper (RandR against Xvnc) inside the workspace
// pod. This is a WaaS-specific mechanism, NOT guacd's native resize:
// the xrdp-libvnc bridge cannot propagate an RDP resize to the
// underlying Xvnc and Guacamole's VNC client never sends one — RandR
// inside the pod is the only path that works (docs/session-resize.md).
//
// Deliberately narrow: a fixed argv (never a shell), two integers
// validated before anything is built, and the same authorization as
// every other session action (fetchByID: owner or admin).
func (s *WorkspaceService) Resize(ctx context.Context, actor Actor, id string, in ResizeInput) error {
	if in.Width < minResizeDimension || in.Width > maxResizeDimension ||
		in.Height < minResizeDimension || in.Height > maxResizeDimension {
		return apierror.BadRequest(fmt.Sprintf("width and height must be between %d and %d",
			minResizeDimension, maxResizeDimension))
	}
	ws, err := s.fetchByID(ctx, actor, id)
	if err != nil {
		// A remote-workspace id deserves an explicit answer, not a bare
		// 404: remote machines have no pod at all (model.go — no
		// template, no operator lifecycle, no compute).
		if apierror.IsNotFound(err) && s.remotes != nil {
			if rw, rerr := s.remotes.FindByID(ctx, id); rerr == nil && rw.OwnerID == actor.ID {
				return apierror.BadRequest("remote workspaces cannot be resized: they have no pod")
			}
		}
		return err
	}
	if ws.Status.Phase != waasv1alpha1.PhaseRunning {
		return apierror.Conflict(fmt.Sprintf("workspace is %s, not Running", ws.Status.Phase))
	}
	if s.exec == nil {
		return apierror.Unavailable("resize is not available on this deployment (no cluster exec)")
	}

	pod, err := s.workspacePod(ctx, ws)
	if err != nil {
		return err
	}
	command := []string{"waas-resize", fmt.Sprintf("%dx%d", in.Width, in.Height)}
	if err := s.exec.Exec(ctx, pod.Namespace, pod.Name, pod.Spec.Containers[0].Name, command); err != nil {
		return apierror.Unavailable(fmt.Sprintf("resize failed: %v", err))
	}
	s.audit.Record(ctx, actor, "workspace.resized", "workspace", id,
		fmt.Sprintf("name=%s mode=%dx%d", ws.Name, in.Width, in.Height))
	return nil
}

// workspacePod resolves THE pod of a workspace by the operator's
// ownership label in the workload namespace. First (and only) pod
// resolution in api-server — kept minimal: exactly one Running,
// non-terminating pod is expected per workspace.
func (s *WorkspaceService) workspacePod(ctx context.Context, ws *waasv1alpha1.Workspace) (*corev1.Pod, error) {
	pods := &corev1.PodList{}
	if err := s.kube.List(ctx, pods, client.InNamespace(ws.EffectiveTargetNamespace()),
		client.MatchingLabels{waasv1alpha1.LabelWorkspace: ws.Name}); err != nil {
		return nil, fmt.Errorf("listing pods of workspace %s: %w", ws.Name, err)
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil && len(p.Spec.Containers) > 0 {
			return p, nil
		}
	}
	return nil, apierror.Conflict("no running pod found for this workspace")
}
