package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/model"
)

func seedAudit(t *testing.T) *SQLAuditRepository {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	repo := NewSQLAuditRepository(db)

	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 25; i++ {
		actor, action := "alice", "workspace.created"
		if i%2 == 1 {
			actor, action = "bob", "catalog.image_updated"
		}
		entry := &model.AuditLog{
			ID:            fmt.Sprintf("e%02d", i),
			OccurredAt:    base.Add(time.Duration(i) * time.Hour),
			ActorUsername: actor,
			Action:        action,
			ResourceType:  "workspace",
		}
		if err := repo.Insert(context.Background(), entry); err != nil {
			t.Fatalf("seeding entry %d: %v", i, err)
		}
	}
	return repo
}

func TestAuditListPaginatesServerSide(t *testing.T) {
	repo := seedAudit(t)
	ctx := context.Background()

	page1, total, err := repo.List(ctx, AuditFilter{}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 25 || len(page1) != 10 {
		t.Fatalf("expected total=25 len=10, got total=%d len=%d", total, len(page1))
	}
	// Newest first: the first row of page 1 is the latest entry.
	if page1[0].ID != "e24" {
		t.Fatalf("expected newest first (e24), got %s", page1[0].ID)
	}
	page3, _, err := repo.List(ctx, AuditFilter{}, 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page3) != 5 {
		t.Fatalf("expected last page of 5, got %d", len(page3))
	}
	if page3[len(page3)-1].ID != "e00" {
		t.Fatalf("expected oldest entry last, got %s", page3[len(page3)-1].ID)
	}
}

func TestAuditListFilters(t *testing.T) {
	repo := seedAudit(t)
	ctx := context.Background()

	// Actor substring — total must reflect the FILTERED count.
	rows, total, err := repo.List(ctx, AuditFilter{Actor: "ali"}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if total != 13 || len(rows) != 13 {
		t.Fatalf("actor filter: expected 13, got total=%d len=%d", total, len(rows))
	}
	for _, e := range rows {
		if e.ActorUsername != "alice" {
			t.Fatalf("actor filter leaked %q", e.ActorUsername)
		}
	}

	// Action prefix (namespaced actions).
	_, total, err = repo.List(ctx, AuditFilter{Action: "catalog."}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if total != 12 {
		t.Fatalf("action filter: expected 12, got %d", total)
	}

	// Date range, combined with actor.
	from := time.Date(2026, 7, 1, 20, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 2, 4, 0, 0, 0, time.UTC)
	rows, total, err = repo.List(ctx, AuditFilter{Actor: "alice", From: from, To: to}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	// alice entries at even offsets: hours 20, 22, 0(+1d), 2(+1d), 4(+1d) → e08..e16.
	if total != 5 {
		t.Fatalf("combined filter: expected 5, got %d (%v)", total, rows)
	}
}
