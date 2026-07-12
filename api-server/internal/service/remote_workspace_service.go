package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
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
	// events notifies the SSE hub on mutations; nil = no live updates.
	events *EventHub

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

// WithEvents wires the SSE hub (same optional pattern as WithWoL).
func (s *RemoteWorkspaceService) WithEvents(hub *EventHub) *RemoteWorkspaceService {
	s.events = hub
	return s
}

// notifyChange is nil-safe: deployments without the hub just skip it.
func (s *RemoteWorkspaceService) notifyChange(ownerID string) {
	if s.events != nil {
		s.events.Notify("remote-workspaces", ownerID)
	}
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

// RemoteProtocolInput is one endpoint of the machine.
type RemoteProtocolInput struct {
	Name    string            `json:"name"`
	Port    int32             `json:"port"`
	Default bool              `json:"default,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
}

// RemoteWorkspaceInput is the create/update payload. Protocols is the
// multi-endpoint shape; the legacy single Port/Protocol/Params fields
// stay accepted (used when Protocols is empty) so older clients keep
// working unchanged.
type RemoteWorkspaceInput struct {
	Name        string                  `json:"name"`
	Hostname    string                  `json:"hostname"`
	Port        int32                   `json:"port,omitempty"`
	Protocol    string                  `json:"protocol,omitempty"`
	Protocols   []RemoteProtocolInput   `json:"protocols,omitempty"`
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

// validateRemoteProtocolName gates which protocols a remote workspace may
// declare or connect with: every registry protocol except kasmvnc.
// KasmVNC is in-cluster only — the wwt reverse-proxy targets a KasmVNC
// server co-located in the cluster, and the "external machine" semantics
// have no kasm equivalent (never exercised, not documented, not tested).
func validateRemoteProtocolName(name string) error {
	if name == "kasmvnc" {
		return apierror.BadRequest("kasmvnc is not supported for remote workspaces")
	}
	if !slices.Contains(params.Protocols(), name) {
		allowed := slices.DeleteFunc(slices.Clone(params.Protocols()), func(p string) bool { return p == "kasmvnc" })
		return apierror.BadRequest(fmt.Sprintf("protocol must be one of %v", allowed))
	}
	return nil
}

// normalizeRemoteProtocols validates the endpoint list and returns the
// canonical form: the legacy single-protocol input becomes a one-entry
// list, exactly one entry is default (the first when none is marked),
// every entry's params go through the same registry gate as templates.
func normalizeRemoteProtocols(in RemoteWorkspaceInput) ([]model.RemoteProtocol, error) {
	entries := in.Protocols
	if len(entries) == 0 {
		entries = []RemoteProtocolInput{{Name: in.Protocol, Port: in.Port, Default: true, Params: in.Params}}
	}
	out := make([]model.RemoteProtocol, 0, len(entries))
	seen := map[string]bool{}
	defaults := 0
	for _, e := range entries {
		if err := validateRemoteProtocolName(e.Name); err != nil {
			return nil, err
		}
		if seen[e.Name] {
			return nil, apierror.BadRequest(fmt.Sprintf("protocol %q is declared twice", e.Name))
		}
		seen[e.Name] = true
		if e.Port < 1 || e.Port > 65535 {
			return nil, apierror.BadRequest(fmt.Sprintf("protocols[%s]: port must be between 1 and 65535", e.Name))
		}
		// Same registry gate as templates: unknown and platform-owned
		// parameters (credentials, gateways, recording…) are rejected.
		if v := params.ValidateTemplateParams(e.Name, e.Params); v != nil {
			return nil, apierror.BadRequest(fmt.Sprintf("protocols[%s].params: %v", e.Name, v))
		}
		if e.Default {
			defaults++
		}
		out = append(out, model.RemoteProtocol{Name: e.Name, Port: e.Port, Default: e.Default, Params: e.Params})
	}
	if defaults > 1 {
		return nil, apierror.BadRequest("at most one protocol may be marked default")
	}
	if defaults == 0 {
		out[0].Default = true
	}
	return out, nil
}

func validateRemoteInput(in RemoteWorkspaceInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return apierror.BadRequest("name is required")
	}
	host := strings.TrimSpace(in.Hostname)
	if host == "" || strings.ContainsAny(host, " \t/@") {
		return apierror.BadRequest("hostname must be a bare host or IP (no scheme, no path, no credentials)")
	}
	if in.MACAddress != "" {
		if _, err := normalizeMAC(in.MACAddress); err != nil {
			return apierror.BadRequest("macAddress: " + err.Error())
		}
	}
	return nil
}

// syncLegacyProtocolFields mirrors the default endpoint into the legacy
// single-protocol fields, which stay stored and serialized so older
// clients and the admin fleet view keep a meaningful protocol/port.
func syncLegacyProtocolFields(rw *model.RemoteWorkspace) {
	def := rw.DefaultProtocol()
	rw.Protocol, rw.Port, rw.Params = def.Name, def.Port, def.Params
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
	protocols, err := normalizeRemoteProtocols(in)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	rw := &model.RemoteWorkspace{
		ID:         uuid.NewString(),
		OwnerID:    actor.ID,
		Name:       strings.TrimSpace(in.Name),
		Hostname:   strings.TrimSpace(in.Hostname),
		Protocols:  protocols,
		MACAddress: canonicalMAC(in.MACAddress),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	syncLegacyProtocolFields(rw)
	rw.SecretName = "waas-remote-" + rw.ID

	// Secret first (rollback is trivial), row second.
	keys, err := s.writeCredentials(ctx, rw, in.Credentials, true)
	if err != nil {
		return nil, err
	}
	rw.CredentialKeys = keys
	if err := s.remotes.Create(ctx, rw); err != nil {
		// Roll the Secret back; a failure here leaves an orphan Secret and
		// must at least be visible (the audit script also lists them).
		if delErr := s.kube.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: s.namespace, Name: rw.SecretName}}); delErr != nil && !apierrors.IsNotFound(delErr) {
			slog.Error("rolling back credentials secret failed; secret is orphaned",
				"secret", rw.SecretName, "error", delErr)
		}
		if errors.Is(err, repository.ErrDuplicate) {
			return nil, apierror.Conflict(fmt.Sprintf("you already have a remote workspace named %q", rw.Name))
		}
		return nil, err
	}
	s.audit.Record(ctx, actor, "remote_workspace.created", "remote_workspace", rw.ID,
		fmt.Sprintf("name=%s target=%s:%d protocol=%s", rw.Name, rw.Hostname, rw.Port, rw.Protocol))
	s.notifyChange(rw.OwnerID)
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
	protocols, err := normalizeRemoteProtocols(in)
	if err != nil {
		return nil, err
	}
	rw.Name = strings.TrimSpace(in.Name)
	rw.Hostname = strings.TrimSpace(in.Hostname)
	rw.Protocols = protocols
	rw.MACAddress = canonicalMAC(in.MACAddress)
	rw.UpdatedAt = time.Now().UTC()
	syncLegacyProtocolFields(rw)

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
	s.notifyChange(rw.OwnerID)
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
	// Same contract as provisioned workspaces: no session may stay
	// "active" on a deleted target (the sweeper re-covers failures).
	if _, err := s.sessions.EndAllForWorkspace(ctx, rw.ID, time.Now().UTC()); err != nil {
		slog.Error("ending sessions of deleted remote workspace failed; the session sweeper will retry",
			"remoteWorkspace", rw.Name, "error", err)
	}
	s.audit.Record(ctx, actor, "remote_workspace.deleted", "remote_workspace", rw.ID, "name="+rw.Name)
	s.notifyChange(rw.OwnerID)
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
	// The caller may pick any endpoint the machine declares (protocol
	// quick-switch); default when unspecified — same contract as
	// provisioned workspaces.
	entry := rw.DefaultProtocol()
	if in.Protocol != "" {
		chosen := rw.ProtocolNamed(in.Protocol)
		if chosen == nil {
			return nil, apierror.BadRequest(fmt.Sprintf("protocol %q is not declared by this remote workspace", in.Protocol))
		}
		entry = *chosen
	}
	// Entries stored before the kasmvnc ban stay rejected at connect time.
	if err := validateRemoteProtocolName(entry.Name); err != nil {
		return nil, err
	}
	// Connect-time tweaks go through the same registry gate as the stored
	// params; the owner registered the machine, so no template allow-list
	// applies beyond the platform tier ban.
	if v := params.ValidateTemplateParams(entry.Name, in.Params); v != nil {
		return nil, apierror.BadRequest("params: " + v.Error())
	}

	session := &model.Session{
		ID:            uuid.NewString(),
		UserID:        actor.ID,
		WorkspaceID:   rw.ID,
		WorkspaceName: rw.Name,
		Protocol:      entry.Name,
		ClientIP:      actor.ClientIP,
		StartedAt:     time.Now().UTC(),
		Params:        in.Params,
		Kind:          model.SessionKindRemote,
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("recording session: %w", err)
	}

	// Policy grant clamped by the effective disable-copy/disable-paste
	// params (stored registration overlaid with connect-time tweaks) —
	// same rule as provisioned workspaces: params restrict, never grant.
	policyGrant := resolveClipboardGrant(ctx, s.kube, s.namespace, s.users, actor)
	clipboard := clampClipboardGrant(policyGrant, mergeParams(entry.Params, in.Params))
	token, err := s.signer.Sign(auth.NewConnectionClaims(s.issuer, actor.ID, session.ID, rw.ID, clipboard, s.connectionTTL))
	if err != nil {
		return nil, fmt.Errorf("issuing connection token: %w", err)
	}
	s.audit.Record(ctx, actor, "session.started", "session", session.ID,
		fmt.Sprintf("remoteWorkspace=%s target=%s:%d protocol=%s", rw.Name, rw.Hostname, entry.Port, entry.Name))

	return &ConnectResult{
		SessionID:       session.ID,
		ConnectionToken: token,
		Protocol:        entry.Name,
		Capabilities:    clipboardCapabilities(policyGrant, clipboard),
	}, nil
}

// AdminList returns every remote workspace for the admin fleet view:
// metadata + owner + last connection, never credentials. Admin-only
// (mounted behind RequireAdmin); the personal ownership rule does not
// apply to this read since no secret material is exposed.
func (s *RemoteWorkspaceService) AdminList(ctx context.Context) ([]model.RemoteWorkspaceAdmin, error) {
	all, err := s.remotes.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	activity, err := s.sessions.Activity(ctx)
	if err != nil {
		return nil, err
	}
	usernames := map[string]string{}
	out := make([]model.RemoteWorkspaceAdmin, 0, len(all))
	for i := range all {
		rw := &all[i]
		name, ok := usernames[rw.OwnerID]
		if !ok {
			if u, err := s.users.FindByID(ctx, rw.OwnerID); err == nil {
				name = u.Username
			}
			usernames[rw.OwnerID] = name
		}
		row := model.RemoteWorkspaceAdmin{
			ID: rw.ID, OwnerID: rw.OwnerID, OwnerUsername: name,
			Name: rw.Name, Hostname: rw.Hostname, Port: rw.Port, Protocol: rw.Protocol,
			MACAddress: rw.MACAddress, HasCredentials: len(rw.CredentialKeys) > 0,
			CreatedAt: rw.CreatedAt,
		}
		if act, ok := activity[rw.ID]; ok {
			t := act.LastActivity
			row.LastConnectedAt = &t
			row.ActiveNow = act.ActiveNow
		}
		out = append(out, row)
	}
	return out, nil
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
