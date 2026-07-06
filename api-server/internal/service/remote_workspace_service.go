package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/params"
	"github.com/xorhub/waas/operator/pkg/policy"
	"github.com/xorhub/waas/shared/auth"
)

// RemoteWorkspaceService manages user-registered OUT-OF-CLUSTER machines
// reachable through guacd. It is deliberately independent from
// WorkspaceService: no template, no operator, no quota — the only shared
// pieces are the session store, the connection-token flow and the guacd
// parameter registry.
//
// Access is policy-gated (WorkspacePolicy.spec.remoteWorkspaces, fail
// closed) and credentials live in one Kubernetes Secret per entry —
// never in the database, never in an API response.
type RemoteWorkspaceService struct {
	kube      client.Client
	namespace string
	users     repository.UserRepository
	remotes   repository.RemoteWorkspaceRepository
	sessions  repository.SessionRepository
	audit     *AuditService
	signer    *auth.Signer
	// wol emits Wake-on-LAN packets via an external relay; nil = feature
	// disabled (no relay configured).
	wol WoLSender

	issuer        string
	connectionTTL time.Duration
}

func NewRemoteWorkspaceService(kube client.Client, namespace string, users repository.UserRepository,
	remotes repository.RemoteWorkspaceRepository, sessions repository.SessionRepository,
	audit *AuditService, signer *auth.Signer, issuer string, connectionTTL time.Duration) *RemoteWorkspaceService {
	return &RemoteWorkspaceService{
		kube: kube, namespace: namespace, users: users, remotes: remotes, sessions: sessions,
		audit: audit, signer: signer, issuer: issuer, connectionTTL: connectionTTL,
	}
}

// WithWoL wires the Wake-on-LAN relay (kept out of the constructor to
// leave existing call sites untouched).
func (s *RemoteWorkspaceService) WithWoL(wol WoLSender) *RemoteWorkspaceService {
	s.wol = wol
	return s
}

// RemoteCredentialsInput carries the secret material for one remote
// machine, write-only. Pointer semantics per key: nil = keep the stored
// value, empty string = delete it, anything else = replace it.
type RemoteCredentialsInput struct {
	Username   *string `json:"username,omitempty"`
	Password   *string `json:"password,omitempty"`
	PrivateKey *string `json:"privateKey,omitempty"`
	Passphrase *string `json:"passphrase,omitempty"`
}

// secretKeys maps input fields onto the guacd credential vocabulary used
// by the connection resolver (same keys as template credential Secrets).
func (c *RemoteCredentialsInput) secretKeys() map[string]*string {
	if c == nil {
		return nil
	}
	return map[string]*string{
		"username":    c.Username,
		"password":    c.Password,
		"private-key": c.PrivateKey,
		"passphrase":  c.Passphrase,
	}
}

// RemoteWorkspaceInput is the create/update payload.
type RemoteWorkspaceInput struct {
	Name        string                  `json:"name"`
	Hostname    string                  `json:"hostname"`
	Port        int32                   `json:"port"`
	Protocol    string                  `json:"protocol"`
	MACAddress  string                  `json:"macAddress,omitempty"`
	Params      map[string]string       `json:"params,omitempty"`
	Credentials *RemoteCredentialsInput `json:"credentials,omitempty"`
}

// WoLSender emits a Wake-on-LAN magic packet for a MAC address. The
// packet must originate on the target's L2 network, which a pod cannot
// reach — so this is delegated to an external relay (see HTTPWoLRelay).
type WoLSender interface {
	Wake(ctx context.Context, mac string) error
}

// requireFeature enforces the policy gate. Fail closed: no user record,
// no resolvable policy or a policy without remoteWorkspaces all deny.
// Platform admins always pass.
func (s *RemoteWorkspaceService) requireFeature(ctx context.Context, actor Actor) error {
	if actor.Role == string(auth.RoleAdmin) {
		return nil
	}
	user, err := s.users.FindByID(ctx, actor.ID)
	if err != nil {
		return apierror.Forbidden("remote workspaces are not enabled for your account")
	}
	policies := &waasv1alpha1.WorkspacePolicyList{}
	if err := s.kube.List(ctx, policies, client.InNamespace(s.namespace)); err != nil {
		return fmt.Errorf("listing workspace policies: %w", err)
	}
	pol, _, denial := policy.Resolve(policies.Items, identityFor(user))
	if denial != nil || !policy.RemoteWorkspacesAllowed(pol) {
		return apierror.Forbidden("remote workspaces are not enabled for your policy")
	}
	return nil
}

func validateRemoteInput(in RemoteWorkspaceInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return apierror.BadRequest("name is required")
	}
	host := strings.TrimSpace(in.Hostname)
	if host == "" || strings.ContainsAny(host, " \t/@") {
		return apierror.BadRequest("hostname must be a bare host or IP (no scheme, no path, no credentials)")
	}
	if in.Port < 1 || in.Port > 65535 {
		return apierror.BadRequest("port must be between 1 and 65535")
	}
	known := false
	for _, p := range params.Protocols() {
		if p == in.Protocol {
			known = true
			break
		}
	}
	if !known {
		return apierror.BadRequest(fmt.Sprintf("protocol must be one of %v", params.Protocols()))
	}
	// Same registry gate as templates: unknown and platform-owned
	// parameters (credentials, gateways, recording…) are rejected.
	if v := params.ValidateTemplateParams(in.Protocol, in.Params); v != nil {
		return apierror.BadRequest("params: " + v.Error())
	}
	if in.MACAddress != "" {
		if _, err := normalizeMAC(in.MACAddress); err != nil {
			return apierror.BadRequest("macAddress: " + err.Error())
		}
	}
	return nil
}

// normalizeMAC validates and canonicalizes a MAC to lower-case
// colon-separated form (net.ParseMAC accepts colon/hyphen/dot notations).
func normalizeMAC(mac string) (string, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil {
		return "", fmt.Errorf("not a valid MAC address (expected aa:bb:cc:dd:ee:ff)")
	}
	return hw.String(), nil
}

// List returns the caller's remote workspaces (strictly own entries —
// even admins do not see others' remotes, the credentials are personal).
func (s *RemoteWorkspaceService) List(ctx context.Context, actor Actor) ([]model.RemoteWorkspace, error) {
	if err := s.requireFeature(ctx, actor); err != nil {
		return nil, err
	}
	return s.remotes.ListByOwner(ctx, actor.ID)
}

// Create registers a remote machine and stores its credentials in a
// dedicated Kubernetes Secret.
func (s *RemoteWorkspaceService) Create(ctx context.Context, actor Actor, in RemoteWorkspaceInput) (*model.RemoteWorkspace, error) {
	if err := s.requireFeature(ctx, actor); err != nil {
		return nil, err
	}
	if err := validateRemoteInput(in); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	rw := &model.RemoteWorkspace{
		ID:         uuid.NewString(),
		OwnerID:    actor.ID,
		Name:       strings.TrimSpace(in.Name),
		Hostname:   strings.TrimSpace(in.Hostname),
		Port:       in.Port,
		Protocol:   in.Protocol,
		MACAddress: canonicalMAC(in.MACAddress),
		Params:     in.Params,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	rw.SecretName = "waas-remote-" + rw.ID

	// Secret first (rollback is trivial), row second.
	keys, err := s.writeCredentials(ctx, rw, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	rw.CredentialKeys = keys
	if err := s.remotes.Create(ctx, rw); err != nil {
		_ = s.kube.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: s.namespace, Name: rw.SecretName}})
		if errors.Is(err, repository.ErrDuplicate) {
			return nil, apierror.Conflict(fmt.Sprintf("you already have a remote workspace named %q", rw.Name))
		}
		return nil, err
	}
	s.audit.Record(ctx, actor, "remote_workspace.created", "remote_workspace", rw.ID,
		fmt.Sprintf("name=%s target=%s:%d protocol=%s", rw.Name, rw.Hostname, rw.Port, rw.Protocol))
	return rw, nil
}

// Get returns one of the caller's remote workspaces.
func (s *RemoteWorkspaceService) Get(ctx context.Context, actor Actor, id string) (*model.RemoteWorkspace, error) {
	if err := s.requireFeature(ctx, actor); err != nil {
		return nil, err
	}
	return s.fetchOwned(ctx, actor, id)
}

// Update edits target/params and (optionally) rotates credentials.
func (s *RemoteWorkspaceService) Update(ctx context.Context, actor Actor, id string, in RemoteWorkspaceInput) (*model.RemoteWorkspace, error) {
	if err := s.requireFeature(ctx, actor); err != nil {
		return nil, err
	}
	rw, err := s.fetchOwned(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if err := validateRemoteInput(in); err != nil {
		return nil, err
	}
	rw.Name = strings.TrimSpace(in.Name)
	rw.Hostname = strings.TrimSpace(in.Hostname)
	rw.Port = in.Port
	rw.Protocol = in.Protocol
	rw.MACAddress = canonicalMAC(in.MACAddress)
	rw.Params = in.Params
	rw.UpdatedAt = time.Now().UTC()

	keys, err := s.writeCredentials(ctx, rw, in.Credentials, false)
	if err != nil {
		return nil, err
	}
	rw.CredentialKeys = keys
	if err := s.remotes.Update(ctx, rw); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return nil, apierror.Conflict(fmt.Sprintf("you already have a remote workspace named %q", rw.Name))
		}
		return nil, err
	}
	s.audit.Record(ctx, actor, "remote_workspace.updated", "remote_workspace", rw.ID,
		fmt.Sprintf("name=%s target=%s:%d protocol=%s credentialsRotated=%t", rw.Name, rw.Hostname, rw.Port, rw.Protocol, in.Credentials != nil))
	return rw, nil
}

// Delete removes the entry and its credentials Secret. No cluster
// resources are involved beyond the Secret — remote machines have no
// provisioning lifecycle by design.
func (s *RemoteWorkspaceService) Delete(ctx context.Context, actor Actor, id string) error {
	if err := s.requireFeature(ctx, actor); err != nil {
		return err
	}
	rw, err := s.fetchOwned(ctx, actor, id)
	if err != nil {
		return err
	}
	if err := s.remotes.Delete(ctx, rw.ID); err != nil {
		return err
	}
	if err := s.kube.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: s.namespace, Name: rw.SecretName}}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting credentials secret %s: %w", rw.SecretName, err)
	}
	s.audit.Record(ctx, actor, "remote_workspace.deleted", "remote_workspace", rw.ID, "name="+rw.Name)
	return nil
}

// Connect opens a guacd session towards the remote machine, mirroring
// the provisioned-workspace flow (session row + short-lived token).
func (s *RemoteWorkspaceService) Connect(ctx context.Context, actor Actor, id string, in ConnectInput) (*ConnectResult, error) {
	if err := s.requireFeature(ctx, actor); err != nil {
		return nil, err
	}
	rw, err := s.fetchOwned(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	// Connect-time tweaks go through the same registry gate as the stored
	// params; the owner registered the machine, so no template allow-list
	// applies beyond the platform tier ban.
	if v := params.ValidateTemplateParams(rw.Protocol, in.Params); v != nil {
		return nil, apierror.BadRequest("params: " + v.Error())
	}

	session := &model.Session{
		ID:            uuid.NewString(),
		UserID:        actor.ID,
		WorkspaceID:   rw.ID,
		WorkspaceName: rw.Name,
		Protocol:      rw.Protocol,
		ClientIP:      actor.ClientIP,
		StartedAt:     time.Now().UTC(),
		Params:        in.Params,
		Kind:          model.SessionKindRemote,
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("recording session: %w", err)
	}

	clipboard := resolveClipboardGrant(ctx, s.kube, s.namespace, s.users, actor)
	token, err := s.signer.Sign(auth.NewConnectionClaims(s.issuer, actor.ID, session.ID, rw.ID, clipboard, s.connectionTTL))
	if err != nil {
		return nil, fmt.Errorf("issuing connection token: %w", err)
	}
	s.audit.Record(ctx, actor, "session.started", "session", session.ID,
		fmt.Sprintf("remoteWorkspace=%s target=%s:%d protocol=%s", rw.Name, rw.Hostname, rw.Port, rw.Protocol))

	return &ConnectResult{
		SessionID:       session.ID,
		ConnectionToken: token,
		Protocol:        rw.Protocol,
		Capabilities: &model.SessionCapabilities{
			ClipboardCopy:  clipboard.Copy,
			ClipboardPaste: clipboard.Paste,
		},
	}, nil
}

// Wake emits a Wake-on-LAN magic packet for a remote workspace through
// the configured relay. Requires the feature, ownership, a stored MAC and
// a configured relay.
func (s *RemoteWorkspaceService) Wake(ctx context.Context, actor Actor, id string) error {
	if err := s.requireFeature(ctx, actor); err != nil {
		return err
	}
	rw, err := s.fetchOwned(ctx, actor, id)
	if err != nil {
		return err
	}
	if rw.MACAddress == "" {
		return apierror.BadRequest("this remote workspace has no MAC address; set one to enable Wake-on-LAN")
	}
	if s.wol == nil {
		return apierror.Unavailable("Wake-on-LAN is not configured on this platform (no relay)")
	}
	if err := s.wol.Wake(ctx, rw.MACAddress); err != nil {
		return apierror.Unavailable(fmt.Sprintf("wake relay failed: %v", err))
	}
	s.audit.Record(ctx, actor, "remote_workspace.woke", "remote_workspace", rw.ID,
		fmt.Sprintf("name=%s mac=%s", rw.Name, rw.MACAddress))
	return nil
}

// WoLEnabled reports whether the platform can emit WoL packets (relay set).
func (s *RemoteWorkspaceService) WoLEnabled() bool { return s.wol != nil }

// canonicalMAC normalizes a MAC (already validated) to colon form; empty
// stays empty.
func canonicalMAC(mac string) string {
	if mac == "" {
		return ""
	}
	if n, err := normalizeMAC(mac); err == nil {
		return n
	}
	return mac
}

func (s *RemoteWorkspaceService) fetchOwned(ctx context.Context, actor Actor, id string) (*model.RemoteWorkspace, error) {
	rw, err := s.remotes.FindByID(ctx, id)
	if errors.Is(err, repository.ErrRemoteWorkspaceNotFound) {
		return nil, apierror.NotFound("remote workspace not found")
	}
	if err != nil {
		return nil, err
	}
	// Ownership is strict — remotes and their credentials are personal.
	if rw.OwnerID != actor.ID {
		return nil, apierror.NotFound("remote workspace not found")
	}
	return rw, nil
}

// writeCredentials creates or patches the credentials Secret and returns
// the sorted list of keys it now holds. On create, a Secret always
// exists (possibly empty) so delete/rotate logic stays uniform.
func (s *RemoteWorkspaceService) writeCredentials(ctx context.Context, rw *model.RemoteWorkspace, in *RemoteCredentialsInput, isNew bool) ([]string, error) {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: s.namespace, Name: rw.SecretName}}
	if !isNew {
		if err := s.kube.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("reading credentials secret %s: %w", rw.SecretName, err)
		} else if apierrors.IsNotFound(err) {
			isNew = true
		}
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels["app.kubernetes.io/managed-by"] = "waas-api-server"
	secret.Labels["waas.xorhub.io/remote-workspace"] = rw.ID
	secret.Labels[ownerLabel] = rw.OwnerID

	for key, value := range (in).secretKeys() {
		switch {
		case value == nil:
			// keep stored value
		case *value == "":
			delete(secret.Data, key)
		default:
			secret.Data[key] = []byte(*value)
		}
	}

	var err error
	if isNew {
		err = s.kube.Create(ctx, secret)
	} else {
		err = s.kube.Update(ctx, secret)
	}
	if err != nil {
		return nil, fmt.Errorf("writing credentials secret %s: %w", rw.SecretName, err)
	}

	keys := make([]string, 0, len(secret.Data))
	for k := range secret.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}
