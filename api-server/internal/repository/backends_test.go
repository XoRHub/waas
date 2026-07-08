package repository

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xorhub/waas/api-server/internal/database"
)

// forEachBackend runs one suite against BOTH storage backends: sqlite
// (always — the dev/default store) and PostgreSQL (when
// WAAS_TEST_PG_URL points at a server — the CI job provides a service
// container; locally: docker run -e POSTGRES_PASSWORD=pg -p 5432:5432
// postgres, then WAAS_TEST_PG_URL=postgres://postgres:pg@localhost/postgres).
//
// The dual-backend divergences are exactly where past bugs lived
// (RFC3339 scanners, JSON columns): a repository test that only runs on
// sqlite proves nothing about production.
func forEachBackend(t *testing.T, run func(t *testing.T, db *database.DB)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		db, err := database.Open(filepath.Join(t.TempDir(), "repo.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		run(t, db)
	})

	pgURL := os.Getenv("WAAS_TEST_PG_URL")
	if pgURL == "" {
		t.Log("WAAS_TEST_PG_URL not set — postgres leg skipped (CI runs it)")
		return
	}
	t.Run("postgres", func(t *testing.T) {
		// One throwaway database per suite: fresh migrations, no
		// cross-test truncation games, dropped afterwards.
		admin, err := sql.Open("pgx", pgURL)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		name := fmt.Sprintf("waas_test_%d", time.Now().UnixNano())
		if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
			t.Fatalf("creating test database: %v", err)
		}
		u, err := url.Parse(pgURL)
		if err != nil {
			t.Fatal(err)
		}
		u.Path = "/" + name
		db, err := database.Open(u.String())
		if err != nil {
			t.Fatalf("opening test database: %v", err)
		}
		t.Cleanup(func() {
			db.Close()
			if _, err := admin.Exec("DROP DATABASE " + name); err != nil {
				t.Logf("dropping test database %s: %v", name, err)
			}
		})
		run(t, db)
	})
}
