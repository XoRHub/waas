package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/shared/auth"
)

const ownerLabel = "waas.xorhub.io/owner"

// WorkspaceService is the business logic around Workspace CRs: quota, RBAC
// scoping, lifecycle actions and desktop connections. It only ever creates
// CRs — the operator turns them into pods/VMs.
type WorkspaceService struct {
	kube      client.Client
	namespace string
	users     repository.UserRepository
	sessions  repository.SessionRepository
	audit     *AuditService
	signer    *auth.Signer

	issuer        string
	connectionTTL time.Duration
}

func NewWorkspaceService(kube client.Client, namespace string, users repository.UserRepository,
	sessions repository.SessionRepository, audit *AuditService, signer *auth.Signer,
	issuer string, connectionTTL time.Duration) *WorkspaceService {
	return &WorkspaceService{
		kube: kube, namespace: namespace, users: users, sessions: sessions,
		audit: audit, signer: signer, issuer: issuer, connectionTTL: connectionTTL,
	}
}

// CreateWorkspaceInput is the user-facing creation payload.
type CreateWorkspaceInput struct {
	Name        string `json:"name"`
	TemplateRef string `json:"templateRef"`
	DisplayName string `json:"displayName"`
	// OwnerID lets admins create workspaces for other users; ignored for
	// non-admin callers.
	OwnerID string `json:"ownerId"`
	// Resources is the user-chosen sizing ("cpu"/"memory" quantities).
	// Bounds are enforced by the admission webhook, not here.
	Resources map[string]string `json:"resources"`
	// Overrides are template deviations (env, security contexts, volumes,
	// protocol...). Passed verbatim to the CR: the admission webhook is
	// the single judge of what this creator may override.
	Overrides *waasv1alpha1.WorkspaceOverrides `json:"overrides,omitempty"`
}

// ConnectInput is the optional connect-time payload: a protocol choice and
// guacd parameter overrides among the template's user-tunable names.
type ConnectInput struct {
	Protocol string            `json:"protocol,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
}

// ConnectResult carries what the frontend needs to open the desktop stream.
type ConnectResult struct {
	SessionID       string `json:"sessionId"`
	ConnectionToken string `json:"connectionToken"`
	Protocol        string `json:"protocol"`
}

// List returns the caller's workspaces, or every workspace for admins.
func (s *WorkspaceService) List(ctx context.Context, actor Actor) ([]model.Workspace, error) {
	opts := []client.ListOption{client.InNamespace(s.namespace)}
	if actor.Role != string(auth.RoleAdmin) {
		opts = append(opts, client.MatchingLabels{ownerLabel: actor.ID})
	}
	list := &waasv1alpha1.WorkspaceList{}
	if err := s.kube.List(ctx, list, opts...); err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}
	// One template list feeds the protocol/userParams enrichment of every
	// workspace row (best-effort: nil template just means no enrichment).
	templates := map[string]*waasv1alpha1.WorkspaceTemplate{}
	tplList := &waasv1alpha1.WorkspaceTemplateList{}
	if err := s.kube.List(ctx, tplList, client.InNamespace(s.namespace)); err == nil {
		for i := range tplList.Items {
			templates[tplList.Items[i].Name] = &tplList.Items[i]
		}
	}
	out := make([]model.Workspace, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, workspaceToModel(&list.Items[i], templates[list.Items[i].Spec.TemplateRef]))
	}
	return out, nil
}

// Create stamps a new Workspace CR after checking quota.
func (s *WorkspaceService) Create(ctx context.Context, actor Actor, in CreateWorkspaceInput) (*model.Workspace, error) {
	ownerID := actor.ID
	if in.OwnerID != "" && actor.Role == string(auth.RoleAdmin) {
		ownerID = in.OwnerID
	}
	owner, err := s.users.FindByID(ctx, ownerID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, apierror.NotFound("owner user not found")
		}
		return nil, fmt.Errorf("looking up owner %s: %w", ownerID, err)
	}
	if in.TemplateRef == "" {
		return nil, apierror.BadRequest("templateRef is required")
	}

	// The API path requires the template to exist up front (kubectl/GitOps
	// users get the more permissive eventually-consistent behavior instead).
	tpl := &waasv1alpha1.WorkspaceTemplate{}
	if err := s.kube.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: in.TemplateRef}, tpl); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, apierror.BadRequest(fmt.Sprintf("template %q does not exist", in.TemplateRef))
		}
		return nil, fmt.Errorf("fetching template %s: %w", in.TemplateRef, err)
	}

	name := in.Name
	if name == "" {
		name = generateWorkspaceName(owner.Username)
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return nil, apierror.BadRequest("name must be a valid DNS-1123 subdomain")
	}

	// Quota and catalog rules are enforced by the admission webhook (the
	// single enforcement point shared with kubectl), not re-implemented
	// here. The identity annotations below feed its policy resolution:
	// the webhook only accepts them because this service's SA is a
	// configured trusted writer, and freezes them afterwards.
	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.namespace,
			Labels:    map[string]string{ownerLabel: owner.ID},
			Annotations: map[string]string{
				waasv1alpha1.AnnotationUsername: owner.Username,
				waasv1alpha1.AnnotationGroups:   strings.Join(owner.Groups, ","),
				// The creator's platform role: it lets the webhook grant
				// admins full override rights on any template.
				waasv1alpha1.AnnotationRole: actor.Role,
			},
		},
		Spec: waasv1alpha1.WorkspaceSpec{
			TemplateRef: in.TemplateRef,
			Owner:       owner.ID,
			DisplayName: in.DisplayName,
			Overrides:   in.Overrides,
		},
	}
	rr, err := requirementsFrom(in.Resources)
	if err != nil {
		return nil, err
	}
	ws.Spec.Resources = rr
	if err := s.kube.Create(ctx, ws); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil, apierror.Conflict(fmt.Sprintf("workspace %q already exists", name))
		}
		if denial, ok := policyDenial(err); ok {
			s.audit.Record(ctx, actor, "workspace.denied", "workspace", name, denial)
			return nil, apierror.Forbidden(denial)
		}
		return nil, fmt.Errorf("creating workspace %s: %w", name, err)
	}
	s.audit.Record(ctx, actor, "workspace.created", "workspace", string(ws.UID), "name="+name)
	m := workspaceToModel(ws, tpl)
	return &m, nil
}

// Get returns one workspace by ID, enforcing ownership for non-admins.
func (s *WorkspaceService) Get(ctx context.Context, actor Actor, id string) (*model.Workspace, error) {
	ws, err := s.fetchByID(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	m := workspaceToModel(ws, s.templateOf(ctx, ws))
	return &m, nil
}

// templateOf resolves a workspace's template, best-effort (nil when gone).
func (s *WorkspaceService) templateOf(ctx context.Context, ws *waasv1alpha1.Workspace) *waasv1alpha1.WorkspaceTemplate {
	tpl := &waasv1alpha1.WorkspaceTemplate{}
	if err := s.kube.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: ws.Spec.TemplateRef}, tpl); err != nil {
		return nil
	}
	return tpl
}

// Delete removes the Workspace CR. The operator tears down compute; the home
// volume is intentionally retained.
func (s *WorkspaceService) Delete(ctx context.Context, actor Actor, id string) error {
	ws, err := s.fetchByID(ctx, actor, id)
	if err != nil {
		return err
	}
	if err := s.kube.Delete(ctx, ws); err != nil {
		return fmt.Errorf("deleting workspace %s: %w", ws.Name, err)
	}
	s.audit.Record(ctx, actor, "workspace.deleted", "workspace", id, "name="+ws.Name)
	return nil
}

// SetPaused pauses or resumes a workspace.
func (s *WorkspaceService) SetPaused(ctx context.Context, actor Actor, id string, paused bool) (*model.Workspace, error) {
	ws, err := s.fetchByID(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	ws.Spec.Paused = paused
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

// Connect opens a desktop session: it records the session and issues the
// short-lived connection token the WebSocket proxy will validate before
// dialing guacd. The caller may pick any protocol the template declares
// and override the guacd parameters the template allow-lists.
func (s *WorkspaceService) Connect(ctx context.Context, actor Actor, id string, in ConnectInput) (*ConnectResult, error) {
	ws, err := s.fetchByID(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if ws.Status.Phase != waasv1alpha1.PhaseRunning {
		return nil, apierror.Conflict(fmt.Sprintf("workspace is %s, not Running", ws.Status.Phase))
	}

	protocol := ws.Status.Protocol
	if in.Protocol != "" {
		protocol = in.Protocol
	}
	if len(in.Params) > 0 || in.Protocol != "" {
		tpl := &waasv1alpha1.WorkspaceTemplate{}
		if err := s.kube.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: ws.Spec.TemplateRef}, tpl); err != nil {
			return nil, fmt.Errorf("fetching template %s: %w", ws.Spec.TemplateRef, err)
		}
		entry := tpl.Spec.ProtocolNamed(protocol)
		if entry == nil {
			return nil, apierror.BadRequest(fmt.Sprintf("protocol %q is not offered by this workspace", protocol))
		}
		// Locked parameters stay locked: only allow-listed names may be
		// overridden by non-admin users.
		if actor.Role != string(auth.RoleAdmin) {
			for name := range in.Params {
				if !slices.Contains(entry.UserParams, name) {
					return nil, apierror.Forbidden(fmt.Sprintf("parameter %q is not user-configurable for protocol %q", name, protocol))
				}
			}
		}
	}

	session := &model.Session{
		ID:            uuid.NewString(),
		UserID:        actor.ID,
		WorkspaceID:   string(ws.UID),
		WorkspaceName: ws.Name,
		Protocol:      protocol,
		ClientIP:      actor.ClientIP,
		StartedAt:     time.Now().UTC(),
		Params:        in.Params,
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("recording session: %w", err)
	}

	token, err := s.signer.Sign(auth.NewConnectionClaims(s.issuer, actor.ID, session.ID, string(ws.UID), s.connectionTTL))
	if err != nil {
		return nil, fmt.Errorf("issuing connection token: %w", err)
	}
	s.audit.Record(ctx, actor, "session.started", "session", session.ID, "workspace="+ws.Name+" protocol="+protocol)

	return &ConnectResult{SessionID: session.ID, ConnectionToken: token, Protocol: protocol}, nil
}

// EndSession closes a session record (called by the proxy on disconnect via
// the internal API, or by the frontend).
func (s *WorkspaceService) EndSession(ctx context.Context, sessionID string) error {
	if err := s.sessions.End(ctx, sessionID, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}

// ConnectionInfo resolves a session into the guacd connection parameters.
// Internal endpoint only: this is where desktop credentials would surface,
// so it must never be reachable from outside the cluster.
func (s *WorkspaceService) ConnectionInfo(ctx context.Context, sessionID string) (*model.ConnectionInfo, error) {
	session, err := s.sessions.FindByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, repository.ErrSessionNotFound) {
			return nil, apierror.NotFound("session not found")
		}
		return nil, fmt.Errorf("finding session %s: %w", sessionID, err)
	}
	if session.EndedAt != nil {
		return nil, apierror.Conflict("session already ended")
	}

	ws, err := s.findByUID(ctx, session.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if ws.Status.Phase != waasv1alpha1.PhaseRunning || ws.Status.Address == "" {
		return nil, apierror.Conflict("workspace is not running")
	}

	info := &model.ConnectionInfo{
		Protocol: ws.Status.Protocol,
		Hostname: ws.Status.Address,
		Port:     ws.Status.Port,
	}
	// The session may target any protocol the workspace serves, not just
	// the default one recorded in status.
	if session.Protocol != "" && session.Protocol != info.Protocol {
		for _, p := range ws.Status.Protocols {
			if p.Name == session.Protocol {
				info.Protocol, info.Port = p.Name, p.Port
				break
			}
		}
	}
	// Desktop credentials stay server-side: resolved from the template and
	// handed to guacd by the proxy, never exposed to the browser.
	tpl := &waasv1alpha1.WorkspaceTemplate{}
	if err := s.kube.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: ws.Spec.TemplateRef}, tpl); err == nil {
		for _, env := range tpl.Spec.Env {
			switch env.Name {
			case "VNC_PW", "VNC_PASSWORD":
				info.Password = env.Value
			case "RDP_USERNAME":
				info.Username = env.Value
			case "RDP_PASSWORD":
				info.Password = env.Value
			}
		}
		// Template params first (locked), then the session's vetted user
		// overrides. Env-var overrides from the workspace spec win over
		// template env for credentials.
		if entry := tpl.Spec.ProtocolNamed(info.Protocol); entry != nil {
			info.Params = map[string]string{}
			for k, v := range entry.Params {
				info.Params[k] = v
			}
			for k, v := range session.Params {
				info.Params[k] = v
			}
		} else if len(session.Params) > 0 {
			info.Params = session.Params
		}
		if ws.Spec.Overrides != nil {
			for _, env := range ws.Spec.Overrides.Env {
				switch env.Name {
				case "VNC_PW", "VNC_PASSWORD", "RDP_PASSWORD":
					info.Password = env.Value
				case "RDP_USERNAME":
					info.Username = env.Value
				}
			}
		}
	}
	return info, nil
}

func (s *WorkspaceService) fetchByID(ctx context.Context, actor Actor, id string) (*waasv1alpha1.Workspace, error) {
	ws, err := s.findByUID(ctx, id)
	if err != nil {
		return nil, err
	}
	if actor.Role != string(auth.RoleAdmin) && ws.Spec.Owner != actor.ID {
		// 404, not 403: don't leak the existence of other users' workspaces.
		return nil, apierror.NotFound("workspace not found")
	}
	return ws, nil
}

func (s *WorkspaceService) findByUID(ctx context.Context, uid string) (*waasv1alpha1.Workspace, error) {
	list := &waasv1alpha1.WorkspaceList{}
	if err := s.kube.List(ctx, list, client.InNamespace(s.namespace)); err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}
	for i := range list.Items {
		if string(list.Items[i].UID) == uid {
			return &list.Items[i], nil
		}
	}
	return nil, apierror.NotFound("workspace not found")
}

func workspaceToModel(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) model.Workspace {
	m := model.Workspace{
		ID:          string(ws.UID),
		Name:        ws.Name,
		DisplayName: ws.Spec.DisplayName,
		TemplateRef: ws.Spec.TemplateRef,
		OwnerID:     ws.Spec.Owner,
		Phase:       string(ws.Status.Phase),
		OS:          string(ws.Status.OS),
		Protocol:    ws.Status.Protocol,
		Paused:      ws.Spec.Paused,
		CreatedAt:   ws.CreationTimestamp.Time,
	}
	for _, p := range ws.Status.Protocols {
		m.Protocols = append(m.Protocols, model.WorkspaceProtocol{
			Name: p.Name, Port: p.Port, Default: p.Default,
		})
	}
	if tpl != nil {
		if len(m.Protocols) == 0 {
			// Not provisioned yet: surface the template's declared
			// protocols so the UI can already offer the choice.
			def := tpl.Spec.DefaultProtocol()
			for _, p := range tpl.Spec.EffectiveProtocols() {
				m.Protocols = append(m.Protocols, model.WorkspaceProtocol{
					Name: p.Name, Port: p.Port, Default: p.Name == def.Name,
				})
			}
		}
		for i := range m.Protocols {
			if entry := tpl.Spec.ProtocolNamed(m.Protocols[i].Name); entry != nil {
				m.Protocols[i].UserParams = entry.UserParams
			}
		}
	}
	if m.Phase == "" {
		m.Phase = string(waasv1alpha1.PhasePending)
	}
	for _, cond := range ws.Status.Conditions {
		if cond.Type == waasv1alpha1.ConditionReady && cond.Status != metav1.ConditionTrue {
			m.Message = cond.Message
		}
	}
	return m
}

// policyDenial extracts the governance webhook's message from a
// Forbidden admission error, so the portal shows "denied by policy X:
// quota reached (3/3)" instead of a raw Kubernetes error dump.
func policyDenial(err error) (string, bool) {
	if !apierrors.IsForbidden(err) {
		return "", false
	}
	msg := err.Error()
	// The webhook formats denials as `[Reason] message`; keep that tail.
	if idx := strings.Index(msg, "["); idx >= 0 {
		msg = msg[idx:]
	}
	return msg, true
}

func generateWorkspaceName(username string) string {
	sanitized := strings.ToLower(username)
	sanitized = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, sanitized)
	sanitized = strings.Trim(sanitized, "-")
	if sanitized == "" {
		sanitized = "user"
	}
	if len(sanitized) > 40 {
		sanitized = sanitized[:40]
	}
	return fmt.Sprintf("%s-%s", sanitized, uuid.NewString()[:8])
}
