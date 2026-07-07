package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/naming"
	"github.com/xorhub/waas/operator/pkg/params"
	"github.com/xorhub/waas/operator/pkg/policy"
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

	// remotes resolves remote-workspace sessions in ConnectionInfo; nil
	// in deployments without the feature (older tests, minimal wiring).
	remotes repository.RemoteWorkspaceRepository

	// defaultNamespacePattern is the operator-wide placement pattern
	// (WAAS_DEFAULT_NAMESPACE_PATTERN); empty = built-in. Must match the
	// operator/webhook value (one Helm values key feeds both).
	defaultNamespacePattern string

	issuer        string
	connectionTTL time.Duration
}

// WithDefaultNamespacePattern wires the global placement pattern (same
// optional-setter style as WithRemoteWorkspaces).
func (s *WorkspaceService) WithDefaultNamespacePattern(pattern string) *WorkspaceService {
	s.defaultNamespacePattern = pattern
	return s
}

func NewWorkspaceService(kube client.Client, namespace string, users repository.UserRepository,
	sessions repository.SessionRepository, audit *AuditService, signer *auth.Signer,
	issuer string, connectionTTL time.Duration) *WorkspaceService {
	return &WorkspaceService{
		kube: kube, namespace: namespace, users: users, sessions: sessions,
		audit: audit, signer: signer, issuer: issuer, connectionTTL: connectionTTL,
	}
}

// WithRemoteWorkspaces wires the remote registry into the connection
// resolver (kept out of the constructor to leave existing call sites
// untouched).
func (s *WorkspaceService) WithRemoteWorkspaces(remotes repository.RemoteWorkspaceRepository) *WorkspaceService {
	s.remotes = remotes
	return s
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
	// TargetNamespace overrides the template's placement pattern (needs
	// the "placement" override right; ownership is webhook-enforced).
	// Empty = the template pattern resolved for the owner.
	TargetNamespace string `json:"targetNamespace,omitempty"`
	// HomeVolumeName reattaches a RETAINED volume as this workspace's
	// home ("start from an existing volume"). The webhook enforces
	// ownership, namespace and retained state.
	HomeVolumeName string `json:"homeVolumeName,omitempty"`
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
	// Capabilities mirror what the token/policy actually enforce, so the
	// in-session overlay can show or grey out its toggles truthfully.
	Capabilities *model.SessionCapabilities `json:"capabilities,omitempty"`
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

	// Placement + workload naming are resolved HERE, once, and frozen into
	// the spec (the webhook enforces immutability and ownership; it
	// recomputes the same precedence chain — template pattern > global
	// pattern > built-in — so UI display and enforcement cannot diverge).
	targetNamespace := in.TargetNamespace
	if targetNamespace == "" {
		targetNamespace, err = s.resolveDefaultNamespace(tpl, owner.Username, in.DisplayName)
		if err != nil {
			return nil, apierror.BadRequest(fmt.Sprintf("template placement: %v", err))
		}
	}
	workloadName, err := s.resolveWorkloadName(ctx, name, in.DisplayName, targetNamespace)
	if err != nil {
		return nil, err
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
			TemplateRef:     in.TemplateRef,
			Owner:           owner.ID,
			DisplayName:     in.DisplayName,
			Overrides:       in.Overrides,
			TargetNamespace: targetNamespace,
			WorkloadName:    workloadName,
			HomeVolumeName:  in.HomeVolumeName,
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
	// Overrides get their own audit line: "who deviated from the template,
	// on what" must be answerable without diffing CRs. Values are omitted
	// on purpose (an env override may carry a credential).
	if summary := overridesSummary(in.Overrides); summary != "" {
		s.audit.Record(ctx, actor, "workspace.overrides_applied", "workspace", string(ws.UID),
			"name="+name+" "+summary)
	}
	m := workspaceToModel(ws, tpl)
	return &m, nil
}

// overridesSummary renders an audit-safe description of template
// deviations: field names and env var NAMES, never values.
func overridesSummary(ov *waasv1alpha1.WorkspaceOverrides) string {
	if ov == nil {
		return ""
	}
	var parts []string
	if len(ov.Env) > 0 {
		names := make([]string, 0, len(ov.Env))
		for _, e := range ov.Env {
			names = append(names, e.Name)
		}
		parts = append(parts, "env="+strings.Join(names, ","))
	}
	if ov.SecurityContext != nil {
		parts = append(parts, "securityContext")
	}
	if ov.PodSecurityContext != nil {
		parts = append(parts, "podSecurityContext")
	}
	if len(ov.Volumes) > 0 || len(ov.VolumeMounts) > 0 {
		parts = append(parts, fmt.Sprintf("volumes=%d mounts=%d", len(ov.Volumes), len(ov.VolumeMounts)))
	}
	if len(ov.NodeSelector) > 0 {
		parts = append(parts, "nodeSelector")
	}
	if len(ov.Tolerations) > 0 {
		parts = append(parts, "tolerations")
	}
	if ov.Protocol != "" {
		parts = append(parts, "protocol="+ov.Protocol)
	}
	if len(parts) == 0 {
		return ""
	}
	return "overrides: " + strings.Join(parts, " ")
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
// Delete removes a workspace. keepVolume carries the user's home-volume
// choice: true (the default) detaches it as a retained volume — still
// owned, still counted against the storage quota; false stamps the
// explicit opt-in annotation the operator's finalizer requires before
// deleting user state. No volume is ever deleted without that opt-in.
func (s *WorkspaceService) Delete(ctx context.Context, actor Actor, id string, keepVolume bool) error {
	ws, err := s.fetchByID(ctx, actor, id)
	if err != nil {
		return err
	}
	if !keepVolume {
		if ws.Annotations == nil {
			ws.Annotations = map[string]string{}
		}
		ws.Annotations[waasv1alpha1.AnnotationDeleteHome] = "true"
		if err := s.kube.Update(ctx, ws); err != nil {
			return fmt.Errorf("stamping volume choice on %s: %w", ws.Name, err)
		}
	}
	if err := s.kube.Delete(ctx, ws); err != nil {
		return fmt.Errorf("deleting workspace %s: %w", ws.Name, err)
	}
	// Close the workspace's open sessions NOW rather than leaving them
	// "active" on a dead target. Failure is logged, not returned: the CR
	// is already gone, and the session sweeper re-covers this on its next
	// pass (it also covers kubectl/ArgoCD deletions that never hit this
	// code path).
	if n, err := s.sessions.EndAllForWorkspace(ctx, string(ws.UID), time.Now().UTC()); err != nil {
		slog.Error("ending sessions of deleted workspace failed; the session sweeper will retry",
			"workspace", ws.Name, "error", err)
	} else if n > 0 {
		s.audit.Record(ctx, actor, "session.ended_with_workspace", "workspace", id,
			fmt.Sprintf("name=%s openSessions=%d", ws.Name, n))
	}
	s.audit.Record(ctx, actor, "workspace.deleted", "workspace", id,
		fmt.Sprintf("name=%s keepVolume=%t", ws.Name, keepVolume))
	return nil
}

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
		// The registry gates names AND values: locked parameters stay
		// locked (template userParams allow-list, admins bypass it) and
		// platform-owned parameters are rejected for everyone.
		isAdmin := actor.Role == string(auth.RoleAdmin)
		if violation := params.ValidateUserOverrides(protocol, in.Params, entry.UserParams, isAdmin); violation != nil {
			return nil, apierror.Forbidden(violation.Error())
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

	// Clipboard rights come from the CONNECTING user's policy and travel
	// inside the token: wwt enforces them without ever calling back.
	clipboard := s.clipboardGrant(ctx, actor)
	token, err := s.signer.Sign(auth.NewConnectionClaims(s.issuer, actor.ID, session.ID, string(ws.UID), clipboard, s.connectionTTL))
	if err != nil {
		return nil, fmt.Errorf("issuing connection token: %w", err)
	}
	s.audit.Record(ctx, actor, "session.started", "session", session.ID, "workspace="+ws.Name+" protocol="+protocol)

	return &ConnectResult{
		SessionID:       session.ID,
		ConnectionToken: token,
		Protocol:        protocol,
		Capabilities: &model.SessionCapabilities{
			ClipboardCopy:  clipboard.Copy,
			ClipboardPaste: clipboard.Paste,
		},
	}, nil
}

// clipboardGrant resolves the connecting user's clipboard rights from
// their WorkspacePolicy. Resolution failure (no user record, no matching
// policy) fails closed: session yes, clipboard no.
func (s *WorkspaceService) clipboardGrant(ctx context.Context, actor Actor) auth.ClipboardGrant {
	return resolveClipboardGrant(ctx, s.kube, s.namespace, s.users, actor)
}

// resolveClipboardGrant is the shared policy→clipboard resolution used by
// both workspace and remote-workspace connects.
func resolveClipboardGrant(ctx context.Context, kube client.Client, namespace string, users repository.UserRepository, actor Actor) auth.ClipboardGrant {
	user, err := users.FindByID(ctx, actor.ID)
	if err != nil {
		return auth.ClipboardGrant{}
	}
	policies := &waasv1alpha1.WorkspacePolicyList{}
	if err := kube.List(ctx, policies, client.InNamespace(namespace)); err != nil {
		return auth.ClipboardGrant{}
	}
	pol, _, denial := policy.Resolve(policies.Items, policy.Identity{Owner: user.ID, Username: user.Username, Groups: user.Groups})
	if denial != nil {
		return auth.ClipboardGrant{}
	}
	copyFrom, pasteTo := policy.ClipboardOf(pol)
	return auth.ClipboardGrant{Copy: copyFrom, Paste: pasteTo}
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

	// Remote-workspace sessions resolve against the remote registry, not
	// the cluster: the machine lives outside, guacd dials it directly.
	if session.Kind == model.SessionKindRemote {
		return s.remoteConnectionInfo(ctx, session)
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
		// Legacy path: literal credentials in template env. Prefer
		// credentialsSecretRef, which keeps secrets out of the CR.
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
		// overrides.
		entry := tpl.Spec.ProtocolNamed(info.Protocol)
		if entry != nil {
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
		// Credentials Secret (username/password/private-key/passphrase):
		// the platform-blessed source. Resolution failure is a hard error —
		// silently connecting with stale credentials would be worse.
		if entry != nil && entry.CredentialsSecretRef != "" {
			if err := s.applyCredentialsSecret(ctx, entry.CredentialsSecretRef, info); err != nil {
				return nil, err
			}
		}
		// Workspace env overrides win last: they change the password the
		// pod actually runs with, so the connection must follow.
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

// remoteConnectionInfo resolves a remote-workspace session: target from
// the registry row, credentials from its dedicated Secret, parameters =
// stored params merged with the session's vetted connect-time tweaks.
func (s *WorkspaceService) remoteConnectionInfo(ctx context.Context, session *model.Session) (*model.ConnectionInfo, error) {
	if s.remotes == nil {
		return nil, apierror.NotFound("remote workspaces are not configured on this server")
	}
	rw, err := s.remotes.FindByID(ctx, session.WorkspaceID)
	if errors.Is(err, repository.ErrRemoteWorkspaceNotFound) {
		return nil, apierror.NotFound("remote workspace not found")
	}
	if err != nil {
		return nil, err
	}
	// The session recorded which endpoint was chosen at connect time;
	// resolve port and stored params from that entry (default when the
	// session predates multi-protocol remotes).
	entry := rw.DefaultProtocol()
	if session.Protocol != "" {
		if chosen := rw.ProtocolNamed(session.Protocol); chosen != nil {
			entry = *chosen
		}
	}
	info := &model.ConnectionInfo{
		Protocol: entry.Name,
		Hostname: rw.Hostname,
		Port:     entry.Port,
		Params:   map[string]string{},
	}
	for k, v := range entry.Params {
		info.Params[k] = v
	}
	for k, v := range session.Params {
		info.Params[k] = v
	}
	if err := s.applyCredentialsSecret(ctx, rw.SecretName, info); err != nil {
		return nil, err
	}
	return info, nil
}

// applyCredentialsSecret loads a protocol's credentials Secret into the
// connection info. Key names follow the guacd vocabulary: username,
// password, private-key, passphrase.
func (s *WorkspaceService) applyCredentialsSecret(ctx context.Context, name string, info *model.ConnectionInfo) error {
	secret := &corev1.Secret{}
	if err := s.kube.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, secret); err != nil {
		return fmt.Errorf("resolving credentials secret %s: %w", name, err)
	}
	if v, ok := secret.Data["username"]; ok {
		info.Username = string(v)
	}
	if v, ok := secret.Data["password"]; ok {
		info.Password = string(v)
	}
	if info.Params == nil {
		info.Params = map[string]string{}
	}
	if v, ok := secret.Data["private-key"]; ok {
		info.Params["private-key"] = string(v)
	}
	if v, ok := secret.Data["passphrase"]; ok {
		info.Params["passphrase"] = string(v)
	}
	return nil
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
	phase := string(ws.Status.Phase)
	// A CR being deleted keeps its last phase until the finalizer/GC
	// finishes: surface the truth so the portal shows "Terminating"
	// instead of a live-looking card that vanishes seconds later.
	if !ws.DeletionTimestamp.IsZero() {
		phase = string(waasv1alpha1.PhaseTerminating)
	}
	m := model.Workspace{
		ID:          string(ws.UID),
		Name:        ws.Name,
		DisplayName: ws.Spec.DisplayName,
		TemplateRef: ws.Spec.TemplateRef,
		OwnerID:     ws.Spec.Owner,
		Phase:       phase,
		OS:          string(ws.Status.OS),
		Protocol:    ws.Status.Protocol,
		Paused:      ws.Spec.Paused,
		CreatedAt:   ws.CreationTimestamp.Time,

		Namespace:    ws.Spec.TargetNamespace,
		WorkloadName: ws.Spec.WorkloadName,
	}
	for _, p := range ws.Status.Protocols {
		m.Protocols = append(m.Protocols, model.WorkspaceProtocol{
			Name: p.Name, Port: p.Port, Default: p.Default,
		})
	}
	// Home volume: what the deletion dialog announces. The name is the
	// authoritative status one (adopted volumes included); the size comes
	// from the template (display-only — enforcement reads the PVC).
	if pvcName := ws.Status.PVCName; pvcName != "" {
		vol := &model.HomeVolumeInfo{Name: pvcName}
		if tpl != nil && tpl.Spec.HomeSize != nil {
			vol.Size = tpl.Spec.HomeSize.String()
		}
		m.HomeVolume = vol
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
		// Effective schedule: the workspace override wins over the
		// template's (the webhook vetted the override right).
		m.Schedule = tpl.Spec.Schedule
	}
	if ws.Spec.Overrides != nil && ws.Spec.Overrides.Schedule != nil {
		m.Schedule = ws.Spec.Overrides.Schedule
	}
	if nt := ws.Status.NextTransition; nt != nil {
		m.NextTransition = &model.ScheduledTransition{Time: nt.Time.Time, Up: nt.Up}
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

// resolveDefaultNamespace applies the placement precedence chain for one
// creation: template pattern > global pattern > built-in.
func (s *WorkspaceService) resolveDefaultNamespace(tpl *waasv1alpha1.WorkspaceTemplate, username, displayName string) (string, error) {
	pattern := naming.EffectivePattern(tpl.Spec.PlacementNamespacePattern(), s.defaultNamespacePattern)
	return naming.ResolveNamespace(pattern, naming.PatternValues{
		User:         username,
		Workspace:    displayName,
		TemplateName: tpl.Name,
		OS:           string(tpl.Spec.OS),
	})
}

// NamespacePreview resolves the namespace a workspace WOULD land in for
// the calling user — what the creation dialog and the template editor
// display. Display-only: creation re-resolves and the webhook re-checks,
// the UI never computes placement on its own.
func (s *WorkspaceService) NamespacePreview(ctx context.Context, actor Actor, templateRef, displayName string) (string, error) {
	owner, err := s.users.FindByID(ctx, actor.ID)
	if err != nil {
		return "", apierror.NotFound("user not found")
	}
	tpl := &waasv1alpha1.WorkspaceTemplate{}
	if err := s.kube.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: templateRef}, tpl); err != nil {
		if apierrors.IsNotFound(err) {
			return "", apierror.NotFound(fmt.Sprintf("template %q does not exist", templateRef))
		}
		return "", fmt.Errorf("fetching template %s: %w", templateRef, err)
	}
	return s.resolveDefaultNamespace(tpl, owner.Username, displayName)
}

// resolveWorkloadName derives the frozen workload name from the display
// name (fallback: the CR name), deterministically suffixed when the
// sanitized form is already taken in the target namespace. Two distinct
// display names that normalize identically ("Zoé" / "zoe") therefore get
// distinct Deployments.
func (s *WorkspaceService) resolveWorkloadName(ctx context.Context, crName, displayName, targetNamespace string) (string, error) {
	base := displayName
	if base == "" {
		base = crName
	}
	// Reserve room for the "-xxxxx" collision suffix.
	candidate := naming.SanitizeWithLimit(base, naming.MaxLabel-6)

	all := &waasv1alpha1.WorkspaceList{}
	if err := s.kube.List(ctx, all, client.InNamespace(s.namespace)); err != nil {
		return "", fmt.Errorf("listing workspaces: %w", err)
	}
	effectiveNS := targetNamespace
	if effectiveNS == "" {
		effectiveNS = s.namespace
	}
	taken := func(name string) bool {
		for i := range all.Items {
			sib := &all.Items[i]
			if sib.EffectiveTargetNamespace() == effectiveNS && sib.EffectiveWorkloadName() == name {
				return true
			}
		}
		// A PVC squatting "<name>-home" also takes the name: a retained
		// volume (possibly another user's, in a shared namespace) or the
		// terminating volume of a just-deleted same-named workspace —
		// reusing it would make the operator adopt or mount a volume this
		// workspace has no right to.
		pvc := &corev1.PersistentVolumeClaim{}
		err := s.kube.Get(ctx, client.ObjectKey{Namespace: effectiveNS, Name: name + "-home"}, pvc)
		return err == nil
	}
	if !taken(candidate) {
		return candidate, nil
	}
	suffixed := candidate + naming.Suffix(crName)
	if taken(suffixed) {
		return "", apierror.Conflict(fmt.Sprintf("workload name %q is already in use in namespace %q", suffixed, effectiveNS))
	}
	return suffixed, nil
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
