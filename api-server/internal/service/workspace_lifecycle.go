package service

// Lifecycle: this file owns the state-changing actions on an existing
// workspace — pause/resume, runtime override updates and the one-shot
// reload — everything that edits the CR after creation without
// deleting it.

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// SetPaused pauses or resumes a workspace.
func (s *WorkspaceService) SetPaused(ctx context.Context, actor Actor, id string, paused bool) (*model.Workspace, error) {
	ws, err := s.fetchByID(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	ws.Spec.Paused = paused
	// Stamp the manual-action time so the schedule evaluator can apply
	// conflict rule B (a manual pause/resume wins until the next opposite
	// scheduled edge). Both pause and resume record it.
	if ws.Annotations == nil {
		ws.Annotations = map[string]string{}
	}
	ws.Annotations[waasv1alpha1.AnnotationManualStateAt] = time.Now().UTC().Format(time.RFC3339)
	if err := s.kube.Update(ctx, ws); err != nil {
		// Resuming re-acquires compute: the webhook may deny it if the
		// image was disabled or the quota shrank in the meantime.
		if denial, ok := policyDenial(err); ok {
			s.audit.Record(ctx, actor, "workspace.denied", "workspace", id, denial)
			return nil, apierror.Forbidden(denial)
		}
		return nil, fmt.Errorf("updating workspace %s: %w", ws.Name, err)
	}
	action := "workspace.resumed"
	if paused {
		action = "workspace.paused"
	}
	s.audit.Record(ctx, actor, action, "workspace", id, "name="+ws.Name)
	m := workspaceToModel(ws, s.templateOf(ctx, ws))
	return &m, nil
}

// UpdateOverridesInput is the runtime-reconfiguration payload of
// PATCH /workspaces/{id}/overrides. Pointer fields distinguish "leave
// alone" (absent) from "replace with this value" (present): a provided
// field REPLACES the stored override wholesale — an empty list/map
// clears it — consistent with the "presence = override" semantics of
// the spec (see operator/pkg/policy/overrides.go).
type UpdateOverridesInput struct {
	Env          *[]corev1.EnvVar     `json:"env,omitempty"`
	NodeSelector *map[string]string   `json:"nodeSelector,omitempty"`
	Tolerations  *[]corev1.Toleration `json:"tolerations,omitempty"`
	// Resources is the user-chosen sizing ({"cpu","memory"} quantities);
	// an empty map reverts to the template sizing.
	Resources *map[string]string `json:"resources,omitempty"`
	// Labels/Annotations are the workload-metadata override (the
	// "metadata" right): merged under the template's workload metadata by
	// the operator, reserved keys rejected by the webhook.
	Labels      *map[string]string `json:"labels,omitempty"`
	Annotations *map[string]string `json:"annotations,omitempty"`
	// Schedule replaces the schedule override; a zero struct clears it
	// (back to the template's schedule).
	Schedule *waasv1alpha1.WorkspaceSchedule `json:"schedule,omitempty"`
}

// UpdateOverrides reconfigures an instantiated workspace's runtime
// deviations (env, node placement, sizing). The admission webhook
// re-runs CheckOverrides on the update — the UI mirrors the rights but
// this service never judges them itself (defense in depth: the
// webhook's "[Reason] message" denial comes back as a 403). The change
// reaches the live desktop at the next scale-up boundary or on manual
// reload (docs/adr/0001); the drift badge flags it meanwhile.
func (s *WorkspaceService) UpdateOverrides(ctx context.Context, actor Actor, id string, in UpdateOverridesInput) (*model.Workspace, error) {
	if in.Env == nil && in.NodeSelector == nil && in.Tolerations == nil && in.Resources == nil &&
		in.Labels == nil && in.Annotations == nil && in.Schedule == nil {
		return nil, apierror.BadRequest("no override field provided (env, nodeSelector, tolerations, resources, labels, annotations, schedule)")
	}
	ws, err := s.fetchByID(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	ov := ws.Spec.Overrides
	if ov == nil {
		ov = &waasv1alpha1.WorkspaceOverrides{}
	}
	// Empty collections normalize to nil: "cleared" and "never set" must
	// be the same state on the CR (presence = override).
	if in.Env != nil {
		ov.Env = *in.Env
		if len(ov.Env) == 0 {
			ov.Env = nil
		}
	}
	if in.NodeSelector != nil {
		ov.NodeSelector = *in.NodeSelector
		if len(ov.NodeSelector) == 0 {
			ov.NodeSelector = nil
		}
	}
	if in.Tolerations != nil {
		ov.Tolerations = *in.Tolerations
		if len(ov.Tolerations) == 0 {
			ov.Tolerations = nil
		}
	}
	if in.Labels != nil {
		ov.Labels = *in.Labels
		if len(ov.Labels) == 0 {
			ov.Labels = nil
		}
	}
	if in.Annotations != nil {
		ov.Annotations = *in.Annotations
		if len(ov.Annotations) == 0 {
			ov.Annotations = nil
		}
	}
	if in.Schedule != nil {
		ov.Schedule = in.Schedule
		// A zero struct clears the override, like the empty collections
		// above: back to the template's schedule.
		if reflect.DeepEqual(*in.Schedule, waasv1alpha1.WorkspaceSchedule{}) {
			ov.Schedule = nil
		}
	}
	// An all-empty overrides block means "no deviation": store nil, as a
	// creation without overrides would.
	if reflect.DeepEqual(*ov, waasv1alpha1.WorkspaceOverrides{}) {
		ov = nil
	}
	ws.Spec.Overrides = ov
	if in.Resources != nil {
		rr, err := requirementsFrom(*in.Resources)
		if err != nil {
			return nil, err
		}
		ws.Spec.Resources = rr
	}
	if err := s.kube.Update(ctx, ws); err != nil {
		if denial, ok := policyDenial(err); ok {
			s.audit.Record(ctx, actor, "workspace.denied", "workspace", id, denial)
			return nil, apierror.Forbidden(denial)
		}
		return nil, fmt.Errorf("updating workspace %s: %w", ws.Name, err)
	}
	// Same audit contract as workspace.overrides_applied at creation:
	// field names and env var NAMES, never values (an env override may
	// carry a credential).
	s.audit.Record(ctx, actor, "workspace.overrides_updated", "workspace", id,
		"name="+ws.Name+" "+updateOverridesSummary(in))
	m := workspaceToModel(ws, s.templateOf(ctx, ws))
	return &m, nil
}

// updateOverridesSummary renders the audit-safe description of one
// overrides update: the replaced fields and env var names, no values.
func updateOverridesSummary(in UpdateOverridesInput) string {
	var parts []string
	if in.Env != nil {
		names := make([]string, 0, len(*in.Env))
		for _, e := range *in.Env {
			names = append(names, e.Name)
		}
		parts = append(parts, "env="+strings.Join(names, ","))
	}
	if in.NodeSelector != nil {
		parts = append(parts, "nodeSelector")
	}
	if in.Tolerations != nil {
		parts = append(parts, "tolerations")
	}
	if in.Resources != nil {
		parts = append(parts, "resources")
	}
	if in.Labels != nil {
		parts = append(parts, "labels")
	}
	if in.Annotations != nil {
		parts = append(parts, "annotations")
	}
	if in.Schedule != nil {
		parts = append(parts, "schedule")
	}
	return "updated: " + strings.Join(parts, " ")
}

// Reload asks the operator for ONE immediate convergence boundary: the
// desktop restarts now on its up-to-date configuration (a pending
// template edit or override change). Implemented as a dedicated
// one-shot annotation — NOT a pause/resume: spec.paused and the
// manual-state-at annotation stay untouched, so a reload can never
// disturb the schedule conflict resolution (rule B) or the pause
// intent (docs/workspace-lifecycle.md).
func (s *WorkspaceService) Reload(ctx context.Context, actor Actor, id string) (*model.Workspace, error) {
	ws, err := s.fetchByID(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if ws.Status.Phase != waasv1alpha1.PhaseRunning {
		return nil, apierror.Conflict(fmt.Sprintf(
			"workspace is %s, not Running; pending changes apply when it next starts", ws.Status.Phase))
	}
	if ws.Annotations == nil {
		ws.Annotations = map[string]string{}
	}
	ws.Annotations[waasv1alpha1.AnnotationReloadRequestedAt] = time.Now().UTC().Format(time.RFC3339)
	if err := s.kube.Update(ctx, ws); err != nil {
		return nil, fmt.Errorf("updating workspace %s: %w", ws.Name, err)
	}
	s.audit.Record(ctx, actor, "workspace.reloaded", "workspace", id, "name="+ws.Name)
	m := workspaceToModel(ws, s.templateOf(ctx, ws))
	return &m, nil
}
