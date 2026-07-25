// Package repository defines the persistence interfaces and their SQL
// implementations. Services depend on the interfaces only.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/shared/auth"
)

var (
	ErrUserNotFound            = errors.New("user not found")
	ErrSessionNotFound         = errors.New("session not found")
	ErrRemoteWorkspaceNotFound = errors.New("remote workspace not found")
	ErrDuplicate               = errors.New("duplicate record")
)

// UserRepository persists platform accounts.
type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	FindByID(ctx context.Context, id string) (*model.User, error)
	FindByUsername(ctx context.Context, username string) (*model.User, error)
	FindByOIDCSubject(ctx context.Context, subject string) (*model.User, error)
	// ListUsernames returns every account's username, unpaginated: the
	// callers compare DNS-1123 projections, which SQL cannot express.
	ListUsernames(ctx context.Context) ([]string, error)
	List(ctx context.Context, page, pageSize int) ([]model.User, int, error)
	// Update rewrites the row EXCEPT tokens_valid_after, role and active:
	// those are revocation substrate, written only through the targeted
	// setters below so a read-modify-write can never carry a stale copy
	// over a concurrent revocation, demotion or deactivation (rationale
	// on SQLUserRepository.Update).
	Update(ctx context.Context, user *model.User) error
	// RecordLogin stamps last_login_at/updated_at in one targeted write —
	// the login paths' only row write, since their copy of the row
	// predates password verification.
	RecordLogin(ctx context.Context, id string, at time.Time) error
	SetRole(ctx context.Context, id string, role auth.Role) error
	SetActive(ctx context.Context, id string, active bool) error
	SetTokensValidAfter(ctx context.Context, id string, at time.Time) error
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (int, error)
	// CountActiveAdmins counts the accounts that can still administer the
	// platform — deactivated ones excluded, they cannot sign in.
	CountActiveAdmins(ctx context.Context) (int, error)
}

// SessionRepository persists desktop connection sessions.
type SessionRepository interface {
	Create(ctx context.Context, session *model.Session) error
	FindByID(ctx context.Context, id string) (*model.Session, error)
	End(ctx context.Context, id string, at time.Time) error
	// EndAllForWorkspace closes every still-open session of one workspace
	// (or remote workspace) and returns how many it closed. Called when
	// the target is deleted: an open session row without a target would
	// stay "active" forever and pollute the activity aggregates.
	EndAllForWorkspace(ctx context.Context, workspaceID string, at time.Time) (int, error)
	// ListOpen returns every session without an end timestamp — the
	// session sweeper's orphan-detection input.
	ListOpen(ctx context.Context) ([]model.Session, error)
	List(ctx context.Context, page, pageSize int) ([]model.Session, int, error)
	// Activity summarizes desktop usage per workspace for the idle
	// sweeper: last observed activity and whether a session is open now.
	Activity(ctx context.Context) (map[string]WorkspaceActivity, error)
}

// WorkspaceActivity is one workspace's session summary.
type WorkspaceActivity struct {
	LastActivity time.Time
	ActiveNow    bool
}

// RemoteWorkspaceRepository persists user-registered external machines.
// Credentials are NOT part of this contract — they live in Kubernetes
// Secrets handled by the service layer.
type RemoteWorkspaceRepository interface {
	Create(ctx context.Context, rw *model.RemoteWorkspace) error
	FindByID(ctx context.Context, id string) (*model.RemoteWorkspace, error)
	ListByOwner(ctx context.Context, ownerID string) ([]model.RemoteWorkspace, error)
	// ListAll returns every remote workspace (admin fleet view). Metadata
	// only — credentials never leave their Secret.
	ListAll(ctx context.Context) ([]model.RemoteWorkspace, error)
	Update(ctx context.Context, rw *model.RemoteWorkspace) error
	Delete(ctx context.Context, id string) error
}

// AuditFilter narrows an audit listing. Zero values mean "no filter":
// Actor matches the username as a substring, Action as a prefix
// (actions are namespaced, e.g. "workspace."), From/To bound occurred_at.
type AuditFilter struct {
	Actor  string
	Action string
	From   time.Time
	To     time.Time
}

// AuditRepository is append-only: it deliberately exposes no update or
// delete operation, matching the audit_logs table contract.
type AuditRepository interface {
	Insert(ctx context.Context, entry *model.AuditLog) error
	List(ctx context.Context, filter AuditFilter, page, pageSize int) ([]model.AuditLog, int, error)
}

// CatalogRepository persists the images discovered by CatalogSyncWorker
// out of each registry-mode WorkspaceImage's published manifest —
// display metadata only (os/app/version/icon), never read by
// enforcement.
type CatalogRepository interface {
	// ReplaceEntries atomically replaces every row of
	// workspaceImageName with entries — one registry's sync is one
	// all-or-nothing swap, never a mix of old and new. Must not be
	// called with entries built from a failed fetch/parse: the caller
	// (CatalogSyncWorker) leaves the table untouched on failure so a
	// stale-but-served row survives a transient sync error.
	ReplaceEntries(ctx context.Context, workspaceImageName string, entries []CatalogEntry) error
	// ListEntries returns the discovered images of one WorkspaceImage,
	// for the admin/catalog API projections.
	ListEntries(ctx context.Context, workspaceImageName string) ([]CatalogEntry, error)
}

// CatalogEntry is one discovered image, mirroring the catalog.yaml wire
// format (shared/catalog.Entry) plus the sync timestamp.
type CatalogEntry struct {
	Image       string
	OS          string
	App         string
	Version     string
	Icon        string
	DisplayName string
	// Description mirrors shared/catalog.Entry's Description: the full
	// per-image description, a template-form prefill hint only. Empty =
	// unknown (manifest without the field).
	Description string
	// Profile and Recommended mirror shared/catalog.Entry's
	// Profile/Recommended: display/prefill hints only, opaque to this
	// layer. Recommended is stored as the raw JSON produced by the
	// worker from shared/catalog.Recommendation — this repository
	// never parses it, so it isn't coupled to the wire-format's or the
	// API model's compatibility cadence.
	Profile     string
	Recommended json.RawMessage
	// Architectures the image is published for (amd64/arm64), per
	// image — a template-form prefill hint, never enforcement. Nil =
	// unknown (manifest without the field).
	Architectures []string
	SyncedAt      time.Time
}
