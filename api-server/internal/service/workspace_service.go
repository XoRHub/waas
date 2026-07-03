package service

import (
	"context"
	"errors"
	"fmt"
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
	out := make([]model.Workspace, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, workspaceToModel(&list.Items[i]))
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

	owned := &waasv1alpha1.WorkspaceList{}
	if err := s.kube.List(ctx, owned, client.InNamespace(s.namespace), client.MatchingLabels{ownerLabel: owner.ID}); err != nil {
		return nil, fmt.Errorf("counting workspaces of %s: %w", owner.Username, err)
	}
	if len(owned.Items) >= owner.MaxWorkspaces {
		return nil, apierror.Conflict(fmt.Sprintf("workspace quota reached (%d)", owner.MaxWorkspaces))
	}

	name := in.Name
	if name == "" {
		name = generateWorkspaceName(owner.Username)
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return nil, apierror.BadRequest("name must be a valid DNS-1123 subdomain")
	}

	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.namespace,
			Labels:    map[string]string{ownerLabel: owner.ID},
		},
		Spec: waasv1alpha1.WorkspaceSpec{
			TemplateRef: in.TemplateRef,
			Owner:       owner.ID,
			DisplayName: in.DisplayName,
		},
	}
	if err := s.kube.Create(ctx, ws); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil, apierror.Conflict(fmt.Sprintf("workspace %q already exists", name))
		}
		return nil, fmt.Errorf("creating workspace %s: %w", name, err)
	}
	s.audit.Record(ctx, actor, "workspace.created", "workspace", string(ws.UID), "name="+name)
	m := workspaceToModel(ws)
	return &m, nil
}

// Get returns one workspace by ID, enforcing ownership for non-admins.
func (s *WorkspaceService) Get(ctx context.Context, actor Actor, id string) (*model.Workspace, error) {
	ws, err := s.fetchByID(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	m := workspaceToModel(ws)
	return &m, nil
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
		return nil, fmt.Errorf("updating workspace %s: %w", ws.Name, err)
	}
	action := "workspace.resumed"
	if paused {
		action = "workspace.paused"
	}
	s.audit.Record(ctx, actor, action, "workspace", id, "name="+ws.Name)
	m := workspaceToModel(ws)
	return &m, nil
}

// Connect opens a desktop session: it records the session and issues the
// short-lived connection token the WebSocket proxy will validate before
// dialing guacd.
func (s *WorkspaceService) Connect(ctx context.Context, actor Actor, id string) (*ConnectResult, error) {
	ws, err := s.fetchByID(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if ws.Status.Phase != waasv1alpha1.PhaseRunning {
		return nil, apierror.Conflict(fmt.Sprintf("workspace is %s, not Running", ws.Status.Phase))
	}

	session := &model.Session{
		ID:            uuid.NewString(),
		UserID:        actor.ID,
		WorkspaceID:   string(ws.UID),
		WorkspaceName: ws.Name,
		Protocol:      ws.Status.Protocol,
		ClientIP:      actor.ClientIP,
		StartedAt:     time.Now().UTC(),
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("recording session: %w", err)
	}

	token, err := s.signer.Sign(auth.NewConnectionClaims(s.issuer, actor.ID, session.ID, string(ws.UID), s.connectionTTL))
	if err != nil {
		return nil, fmt.Errorf("issuing connection token: %w", err)
	}
	s.audit.Record(ctx, actor, "session.started", "session", session.ID, "workspace="+ws.Name)

	return &ConnectResult{SessionID: session.ID, ConnectionToken: token, Protocol: ws.Status.Protocol}, nil
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

func workspaceToModel(ws *waasv1alpha1.Workspace) model.Workspace {
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
