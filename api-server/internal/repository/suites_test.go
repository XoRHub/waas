package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/shared/auth"
)

// The dual-backend suites below assert the SAME behavior on sqlite and
// PostgreSQL, deliberately hitting the historical divergence traps:
// timestamp round-trips (RFC3339 scanners), JSON columns (groups,
// preferences, params, protocols) and NULL vs zero-value handling.

func TestUserRepositorySuite(t *testing.T) {
	forEachBackend(t, func(t *testing.T, db *database.DB) {
		repo := NewSQLUserRepository(db)
		ctx := context.Background()
		created := time.Date(2026, 7, 8, 10, 30, 0, 0, time.UTC)

		u := &model.User{
			ID: "u1", Username: "alice", Email: "a@x", Role: auth.RoleUser,
			Active: true, Groups: []string{"dev", "ops"},
			Preferences: model.UserPreferences{Language: "fr", WorkspaceFolders: map[string]string{"w1": "infra"}},
			CreatedAt:   created, UpdatedAt: created,
		}
		if err := repo.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		// Timestamp round-trip: the exact instant, whatever the backend
		// stores it as (the RFC3339-scanner divergence class).
		got, err := repo.FindByID(ctx, "u1")
		if err != nil {
			t.Fatal(err)
		}
		if !got.CreatedAt.Equal(created) {
			t.Fatalf("created_at round-trip: want %v got %v", created, got.CreatedAt)
		}
		if len(got.Groups) != 2 || got.Groups[0] != "dev" {
			t.Fatalf("groups JSON round-trip: %v", got.Groups)
		}
		if got.Preferences.WorkspaceFolders["w1"] != "infra" {
			t.Fatalf("preferences JSON round-trip: %+v", got.Preferences)
		}
		if got.TokensValidAfter != nil {
			t.Fatalf("fresh user must carry no token bound (NULL round-trip): %v", got.TokensValidAfter)
		}

		// Username lookup + duplicate rejection.
		if _, err := repo.FindByUsername(ctx, "alice"); err != nil {
			t.Fatal(err)
		}
		dup := *u
		if err := repo.Create(ctx, &dup); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("duplicate create must return ErrDuplicate, got %v", err)
		}

		// Update: groups replaced wholesale (the admin-edit contract).
		// The token bound keeps its sub-second precision through storage:
		// revocation compares it against a second-truncated JWT iat, so a
		// backend rounding it (the RFC3339-scanner divergence class) would
		// silently change which tokens die.
		bound := time.Date(2026, 7, 8, 11, 0, 0, 500_000_000, time.UTC)
		got.Groups = []string{"sec"}
		if err := repo.Update(ctx, got); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetRole(ctx, "u1", auth.RoleAdmin); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetTokensValidAfter(ctx, "u1", bound); err != nil {
			t.Fatal(err)
		}
		got, err = repo.FindByID(ctx, "u1")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Groups) != 1 || got.Groups[0] != "sec" || got.Role != auth.RoleAdmin {
			t.Fatalf("update round-trip: %+v", got)
		}
		if got.TokensValidAfter == nil || !got.TokensValidAfter.Equal(bound) {
			t.Fatalf("tokens_valid_after round-trip: want %v got %v", bound, got.TokensValidAfter)
		}

		// Revocation substrate must survive a later full-row write: Update
		// carries a copy read BEFORE the bound, demotion and deactivation
		// existed, and writing it back would resurrect revoked tokens, an
		// old role and a disabled account (audit 2026-07-25, F5). got was
		// fetched before these setters run, so it is exactly that stale
		// copy — admin, active, and (nulled below) no bound.
		if err := repo.SetRole(ctx, "u1", auth.RoleUser); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetActive(ctx, "u1", false); err != nil {
			t.Fatal(err)
		}
		got.TokensValidAfter = nil
		got.DisplayName = "post-revocation edit"
		if err := repo.Update(ctx, got); err != nil {
			t.Fatal(err)
		}
		got, err = repo.FindByID(ctx, "u1")
		if err != nil {
			t.Fatal(err)
		}
		if got.TokensValidAfter == nil || !got.TokensValidAfter.Equal(bound) {
			t.Fatalf("a full-row Update must not clear the token bound, got %v", got.TokensValidAfter)
		}
		if got.Role != auth.RoleUser {
			t.Fatalf("a full-row Update must not undo a demotion, got role %q", got.Role)
		}
		if got.Active {
			t.Fatal("a full-row Update must not reactivate a deactivated account")
		}
		if got.DisplayName != "post-revocation edit" {
			t.Fatalf("the non-substrate columns must still round-trip, got %q", got.DisplayName)
		}

		// RecordLogin: the login paths' only write — targeted, both stamps.
		loginAt := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
		if err := repo.RecordLogin(ctx, "u1", loginAt); err != nil {
			t.Fatal(err)
		}
		got, err = repo.FindByID(ctx, "u1")
		if err != nil {
			t.Fatal(err)
		}
		if got.LastLoginAt == nil || !got.LastLoginAt.Equal(loginAt) || !got.UpdatedAt.Equal(loginAt) {
			t.Fatalf("RecordLogin round-trip: last_login_at %v updated_at %v", got.LastLoginAt, got.UpdatedAt)
		}

		// Missing rows fail typed, not with sql.ErrNoRows. The targeted
		// writers need saying too: an UPDATE matching no row is not an SQL
		// error, so only the RowsAffected check stands between a caller and
		// a silent nil on a user that no longer exists.
		if _, err := repo.FindByID(ctx, "ghost"); !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("want ErrUserNotFound, got %v", err)
		}
		if err := repo.RecordLogin(ctx, "ghost", loginAt); !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("RecordLogin on a missing user: want ErrUserNotFound, got %v", err)
		}
		if err := repo.SetRole(ctx, "ghost", auth.RoleAdmin); !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("SetRole on a missing user: want ErrUserNotFound, got %v", err)
		}
		if err := repo.SetActive(ctx, "ghost", true); !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("SetActive on a missing user: want ErrUserNotFound, got %v", err)
		}

		if n, err := repo.Count(ctx); err != nil || n != 1 {
			t.Fatalf("count: %d %v", n, err)
		}
		if err := repo.Delete(ctx, "u1"); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.FindByID(ctx, "u1"); !errors.Is(err, ErrUserNotFound) {
			t.Fatal("deleted user must be gone")
		}
	})
}

// The admin floor is enforced by the WRITE, not by a count the caller
// took earlier: a pre-check cannot survive two admins dropping their
// rights at the same moment. Runs on both backends because the mechanism
// differs — PostgreSQL needs serializable isolation, SQLite serializes on
// its single connection.
func TestAdminFloorIsEnforcedByTheWrite(t *testing.T) {
	forEachBackend(t, func(t *testing.T, db *database.DB) {
		repo := NewSQLUserRepository(db)
		ctx := context.Background()
		now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
		seed := func(id string, role auth.Role, active bool) {
			t.Helper()
			u := &model.User{ID: id, Username: id, Role: role, Active: active, CreatedAt: now, UpdatedAt: now}
			if err := repo.Create(ctx, u); err != nil {
				t.Fatalf("seeding %s: %v", id, err)
			}
		}
		seed("admin-1", auth.RoleAdmin, true)
		seed("admin-2", auth.RoleAdmin, true)
		seed("plain", auth.RoleUser, true)

		// Two admins: either may step down.
		if err := repo.SetRoleUnlessLastAdmin(ctx, "admin-2", auth.RoleUser); err != nil {
			t.Fatalf("demoting one of two admins: %v", err)
		}
		// One left: it cannot, by demotion or by deactivation.
		if err := repo.SetRoleUnlessLastAdmin(ctx, "admin-1", auth.RoleUser); !errors.Is(err, ErrLastAdmin) {
			t.Fatalf("demoting the last admin: want ErrLastAdmin, got %v", err)
		}
		if err := repo.SetActiveUnlessLastAdmin(ctx, "admin-1", false); !errors.Is(err, ErrLastAdmin) {
			t.Fatalf("deactivating the last admin: want ErrLastAdmin, got %v", err)
		}
		// And the refusal rolled the write back rather than half-applying.
		if got, err := repo.FindByID(ctx, "admin-1"); err != nil {
			t.Fatal(err)
		} else if got.Role != auth.RoleAdmin || !got.Active {
			t.Fatalf("a refused write must leave the row untouched, got role=%s active=%v", got.Role, got.Active)
		}

		// The floor is about REMOVING the last admin, not about the table
		// always having one: a non-admin's edit is never held hostage by it.
		if err := repo.SetActiveUnlessLastAdmin(ctx, "plain", false); err != nil {
			t.Fatalf("deactivating a non-admin: %v", err)
		}
		if err := repo.SetRoleUnlessLastAdmin(ctx, "ghost", auth.RoleUser); !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("missing account: want ErrUserNotFound, got %v", err)
		}

		// Promoting is never refused — that is how you get out of the
		// refusal above.
		if err := repo.SetRoleUnlessLastAdmin(ctx, "admin-2", auth.RoleAdmin); err != nil {
			t.Fatalf("promoting back: %v", err)
		}
		if err := repo.SetRoleUnlessLastAdmin(ctx, "admin-1", auth.RoleUser); err != nil {
			t.Fatalf("demotion must be allowed once a replacement exists: %v", err)
		}
	})
}

// The property the floor actually has to hold: two admins dropping their
// rights at the SAME moment write two DIFFERENT rows, so row locks alone
// would let both pass a count that still sees the other — write skew,
// ending in a platform nobody can administer. Repeated because a race
// that only fails sometimes is a race that passes sometimes.
func TestAdminFloorSurvivesConcurrentDemotions(t *testing.T) {
	forEachBackend(t, func(t *testing.T, db *database.DB) {
		repo := NewSQLUserRepository(db)
		ctx := context.Background()
		now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

		for round := 0; round < 20; round++ {
			for _, id := range []string{"a", "b"} {
				_ = repo.Delete(ctx, id)
				u := &model.User{ID: id, Username: id, Role: auth.RoleAdmin, Active: true, CreatedAt: now, UpdatedAt: now}
				if err := repo.Create(ctx, u); err != nil {
					t.Fatalf("seeding %s: %v", id, err)
				}
			}

			start := make(chan struct{})
			errs := make(chan error, 2)
			for _, id := range []string{"a", "b"} {
				go func() {
					<-start
					errs <- repo.SetRoleUnlessLastAdmin(ctx, id, auth.RoleUser)
				}()
			}
			close(start)
			first, second := <-errs, <-errs

			admins, err := repo.CountActiveAdmins(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if admins < 1 {
				t.Fatalf("round %d: both demotions landed, no administrator left (%v / %v)", round, first, second)
			}
			// One of them must have been told why it did not go through,
			// rather than silently no-oping.
			if first == nil && second == nil {
				t.Fatalf("round %d: both reported success with %d admin(s) left", round, admins)
			}
			if first != nil && !errors.Is(first, ErrLastAdmin) {
				t.Fatalf("round %d: unexpected error %v", round, first)
			}
			if second != nil && !errors.Is(second, ErrLastAdmin) {
				t.Fatalf("round %d: unexpected error %v", round, second)
			}
		}
	})
}

func TestSessionRepositorySuite(t *testing.T) {
	forEachBackend(t, func(t *testing.T, db *database.DB) {
		repo := NewSQLSessionRepository(db)
		ctx := context.Background()
		started := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
		// sessions.user_id is a real FK: the user must exist.
		if err := NewSQLUserRepository(db).Create(ctx, &model.User{
			ID: "u1", Username: "alice", Role: auth.RoleUser, Active: true,
			CreatedAt: started, UpdatedAt: started,
		}); err != nil {
			t.Fatal(err)
		}

		s1 := &model.Session{
			ID: "s1", UserID: "u1", WorkspaceID: "w1", WorkspaceName: "ws one",
			Protocol: "kasmvnc", StartedAt: started,
			Params: map[string]string{"color-depth": "16"},
		}
		s2 := &model.Session{
			ID: "s2", UserID: "u1", WorkspaceID: "w1", WorkspaceName: "ws one",
			Protocol: "vnc", StartedAt: started.Add(time.Minute), Kind: model.SessionKindWorkspace,
		}
		for _, s := range []*model.Session{s1, s2} {
			if err := repo.Create(ctx, s); err != nil {
				t.Fatal(err)
			}
		}

		got, err := repo.FindByID(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if got.EndedAt != nil {
			t.Fatal("fresh session must be open (ended_at NULL round-trip)")
		}
		if got.Params["color-depth"] != "16" {
			t.Fatalf("params JSON round-trip: %v", got.Params)
		}
		if !got.StartedAt.Equal(started) {
			t.Fatalf("started_at round-trip: want %v got %v", started, got.StartedAt)
		}

		// End one, then close the rest via the workspace-wide sweep.
		endAt := started.Add(2 * time.Minute)
		if err := repo.End(ctx, "s1", endAt); err != nil {
			t.Fatal(err)
		}
		open, err := repo.ListOpen(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(open) != 1 || open[0].ID != "s2" {
			t.Fatalf("ListOpen after End: %+v", open)
		}
		n, err := repo.EndAllForWorkspace(ctx, "w1", endAt.Add(time.Minute))
		if err != nil || n != 1 {
			t.Fatalf("EndAllForWorkspace: n=%d err=%v", n, err)
		}

		activity, err := repo.Activity(ctx)
		if err != nil {
			t.Fatal(err)
		}
		act, ok := activity["w1"]
		if !ok || act.ActiveNow {
			t.Fatalf("activity after closing everything: %+v", activity)
		}
	})
}

func TestRemoteWorkspaceRepositorySuite(t *testing.T) {
	forEachBackend(t, func(t *testing.T, db *database.DB) {
		repo := NewSQLRemoteWorkspaceRepository(db)
		ctx := context.Background()
		// remote_workspaces.owner_id is a real FK: the owner must exist.
		now := time.Now().UTC()
		if err := NewSQLUserRepository(db).Create(ctx, &model.User{
			ID: "u1", Username: "alice", Role: auth.RoleUser, Active: true,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}

		rw := &model.RemoteWorkspace{
			ID: "r1", OwnerID: "u1", Name: "lab", Hostname: "10.0.0.5",
			Port: 22, Protocol: "ssh",
			Protocols: []model.RemoteProtocol{
				{Name: "ssh", Port: 22, Default: true},
				{Name: "kasmvnc", Port: 6901},
			},
			MACAddress: "aa:bb:cc:dd:ee:ff",
			SecretName: "waas-remote-r1",
		}
		if err := repo.Create(ctx, rw); err != nil {
			t.Fatal(err)
		}
		got, err := repo.FindByID(ctx, "r1")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Protocols) != 2 || got.Protocols[1].Name != "kasmvnc" {
			t.Fatalf("protocols JSON round-trip: %+v", got.Protocols)
		}
		if got.MACAddress != "aa:bb:cc:dd:ee:ff" || got.SecretName != "waas-remote-r1" {
			t.Fatalf("scalar round-trip: %+v", got)
		}

		byOwner, err := repo.ListByOwner(ctx, "u1")
		if err != nil || len(byOwner) != 1 {
			t.Fatalf("ListByOwner: %d %v", len(byOwner), err)
		}
		if all, err := repo.ListAll(ctx); err != nil || len(all) != 1 {
			t.Fatalf("ListAll: %v", err)
		}

		got.Hostname = "10.0.0.9"
		if err := repo.Update(ctx, got); err != nil {
			t.Fatal(err)
		}
		got, _ = repo.FindByID(ctx, "r1")
		if got.Hostname != "10.0.0.9" {
			t.Fatal("update must persist")
		}

		if err := repo.Delete(ctx, "r1"); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.FindByID(ctx, "r1"); !errors.Is(err, ErrRemoteWorkspaceNotFound) {
			t.Fatalf("want ErrRemoteWorkspaceNotFound, got %v", err)
		}
	})
}

func TestAuditRepositorySuite(t *testing.T) {
	forEachBackend(t, func(t *testing.T, db *database.DB) {
		repo := NewSQLAuditRepository(db)
		ctx := context.Background()
		base := time.Date(2026, 7, 8, 8, 0, 0, 0, time.UTC)

		for i, action := range []string{"workspace.created", "workspace.deleted", "session.started"} {
			entry := &model.AuditLog{
				ID:         fmt.Sprintf("a%d", i),
				OccurredAt: base.Add(time.Duration(i) * time.Hour),
				ActorID:    "u1", ActorUsername: "alice",
				Action: action, ResourceType: "workspace", ResourceID: "w1",
			}
			if err := repo.Insert(ctx, entry); err != nil {
				t.Fatal(err)
			}
		}

		// Prefix filter on action + total count with pagination.
		rows, total, err := repo.List(ctx, AuditFilter{Action: "workspace."}, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if total != 2 || len(rows) != 1 {
			t.Fatalf("action-prefix filter: total=%d rows=%d", total, len(rows))
		}

		// Time window bounds occurred_at on both ends.
		rows, total, err = repo.List(ctx, AuditFilter{From: base.Add(30 * time.Minute), To: base.Add(90 * time.Minute)}, 1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if total != 1 || rows[0].Action != "workspace.deleted" {
			t.Fatalf("time-window filter: total=%d rows=%+v", total, rows)
		}

		// Actor substring match.
		_, total, err = repo.List(ctx, AuditFilter{Actor: "lic"}, 1, 10)
		if err != nil || total != 3 {
			t.Fatalf("actor substring: total=%d err=%v", total, err)
		}
	})
}

func TestCatalogRepositorySuite(t *testing.T) {
	forEachBackend(t, func(t *testing.T, db *database.DB) {
		repo := NewSQLCatalogRepository(db)
		ctx := context.Background()
		synced := time.Date(2026, 7, 8, 8, 0, 0, 0, time.UTC)

		// No rows yet: an empty, non-nil slice, never an error.
		got, err := repo.ListEntries(ctx, "ubuntu-xfce")
		if err != nil || len(got) != 0 {
			t.Fatalf("empty catalog: got=%v err=%v", got, err)
		}

		first := []CatalogEntry{
			{Image: "docker.io/xorhub/ubuntu-xfce:1.0.0", OS: "linux", App: "ubuntu-xfce", Version: "1.0.0", Icon: "linux", DisplayName: "Ubuntu XFCE", Description: "Full XFCE desktop, VNC + RDP + SSH.", Profile: "hardened", Recommended: json.RawMessage(`{"podSecurityContext":{"runAsUser":1000}}`), Architectures: []string{"amd64"}, SyncedAt: synced},
			{Image: "docker.io/xorhub/firefox:1.0.0", App: "firefox", SyncedAt: synced},
		}
		if err := repo.ReplaceEntries(ctx, "ubuntu-xfce", first); err != nil {
			t.Fatal(err)
		}
		// A second, unrelated WorkspaceImage's rows must not leak across.
		if err := repo.ReplaceEntries(ctx, "windows-server", []CatalogEntry{
			{Image: "docker.io/xorhub/windows:2022", SyncedAt: synced},
		}); err != nil {
			t.Fatal(err)
		}

		got, err = repo.ListEntries(ctx, "ubuntu-xfce")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 entries, got %+v", got)
		}
		// image ordering (ORDER BY image): firefox before ubuntu-xfce.
		if got[0].Image != "docker.io/xorhub/firefox:1.0.0" || got[0].App != "firefox" {
			t.Fatalf("unexpected first entry: %+v", got[0])
		}
		if got[1].DisplayName != "Ubuntu XFCE" || got[1].Icon != "linux" || got[1].Description != "Full XFCE desktop, VNC + RDP + SSH." {
			t.Fatalf("scalar round-trip: %+v", got[1])
		}
		if got[0].Description != "" {
			t.Fatalf("absent description should stay empty: %+v", got[0])
		}
		// Structural comparison, not literal string equality: Postgres's
		// real jsonb column reformats to its own canonical text on
		// storage (whitespace, key order), unlike SQLite's TEXT-affinity
		// passthrough.
		var recommended struct {
			PodSecurityContext struct {
				RunAsUser int `json:"runAsUser"`
			} `json:"podSecurityContext"`
		}
		if got[1].Profile != "hardened" || got[1].Recommended == nil {
			t.Fatalf("profile/recommended round-trip: %+v", got[1])
		}
		if err := json.Unmarshal(got[1].Recommended, &recommended); err != nil {
			t.Fatalf("recommended is not valid JSON: %v (%s)", err, got[1].Recommended)
		}
		if recommended.PodSecurityContext.RunAsUser != 1000 {
			t.Fatalf("recommended round-trip mismatch: %+v", recommended)
		}
		if got[0].Profile != "" || got[0].Recommended != nil {
			t.Fatalf("absent profile/recommended should stay zero: %+v", got[0])
		}
		if len(got[1].Architectures) != 1 || got[1].Architectures[0] != "amd64" {
			t.Fatalf("architectures round-trip: %+v", got[1].Architectures)
		}
		if got[0].Architectures != nil {
			t.Fatalf("absent architectures should stay nil: %+v", got[0].Architectures)
		}
		if !got[1].SyncedAt.Equal(synced) {
			t.Fatalf("synced_at round-trip: want %v got %v", synced, got[1].SyncedAt)
		}

		other, err := repo.ListEntries(ctx, "windows-server")
		if err != nil || len(other) != 1 {
			t.Fatalf("windows-server entries: %v %v", other, err)
		}

		// A second sync fully replaces the first: dropped images
		// disappear, survivors keep their fresh values.
		second := []CatalogEntry{
			{Image: "docker.io/xorhub/firefox:1.1.0", App: "firefox", Version: "1.1.0", SyncedAt: synced.Add(time.Hour)},
		}
		if err := repo.ReplaceEntries(ctx, "ubuntu-xfce", second); err != nil {
			t.Fatal(err)
		}
		got, err = repo.ListEntries(ctx, "ubuntu-xfce")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Version != "1.1.0" {
			t.Fatalf("replace must swap the whole set: %+v", got)
		}
		// The other WorkspaceImage's rows are untouched by an unrelated
		// ReplaceEntries call.
		other, err = repo.ListEntries(ctx, "windows-server")
		if err != nil || len(other) != 1 {
			t.Fatalf("windows-server entries after unrelated replace: %v %v", other, err)
		}
	})
}
