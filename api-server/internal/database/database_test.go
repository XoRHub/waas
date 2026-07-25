package database

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestOpenCapsSQLitePool pins the serialized-access guarantee the DSN
// pragmas rely on.
func TestOpenCapsSQLitePool(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "cap.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("sqlite MaxOpenConnections = %d, want 1", got)
	}
}

// TestOpenCapsPostgresPool proves the pool bound is actually applied on
// the pgx path — without it a Postgres slowdown amplifies into unbounded
// connection growth (one uncached read per authenticated request).
// Same gating as the repository suites: skipped without WAAS_TEST_PG_URL,
// run by `make test-go-pg` and the CI service container.
func TestOpenCapsPostgresPool(t *testing.T) {
	pgURL := os.Getenv("WAAS_TEST_PG_URL")
	if pgURL == "" {
		t.Skip("WAAS_TEST_PG_URL not set — postgres leg skipped (CI runs it)")
	}
	// One throwaway database, like forEachBackend: Open migrates, and the
	// shared base database must stay schema-free for the other suites.
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
	db, err := Open(u.String())
	if err != nil {
		t.Fatalf("opening test database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec("DROP DATABASE " + name); err != nil {
			t.Logf("dropping test database %s: %v", name, err)
		}
	})
	if got := db.Stats().MaxOpenConnections; got != 25 {
		t.Fatalf("postgres MaxOpenConnections = %d, want 25", got)
	}
}
