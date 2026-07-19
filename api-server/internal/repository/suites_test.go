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

		// Username lookup + duplicate rejection.
		if _, err := repo.FindByUsername(ctx, "alice"); err != nil {
			t.Fatal(err)
		}
		dup := *u
		if err := repo.Create(ctx, &dup); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("duplicate create must return ErrDuplicate, got %v", err)
		}

		// Update: groups replaced wholesale (the admin-edit contract).
		got.Groups = []string{"sec"}
		got.Role = auth.RoleAdmin
		if err := repo.Update(ctx, got); err != nil {
			t.Fatal(err)
		}
		got, err = repo.FindByID(ctx, "u1")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Groups) != 1 || got.Groups[0] != "sec" || got.Role != auth.RoleAdmin {
			t.Fatalf("update round-trip: %+v", got)
		}

		// Missing rows fail typed, not with sql.ErrNoRows.
		if _, err := repo.FindByID(ctx, "ghost"); !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("want ErrUserNotFound, got %v", err)
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
