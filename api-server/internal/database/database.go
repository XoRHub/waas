// Package database opens the platform store and applies embedded migrations
// automatically at startup. Production runs PostgreSQL (pgx/v5); dev mode
// runs pure-Go SQLite so the binary stays CGO-free and multi-arch.
package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"
	_ "modernc.org/sqlite"             // database/sql driver "sqlite"

	"github.com/xorhub/waas/api-server/migrations"
)

// Dialect identifies the SQL flavor in use.
type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectSQLite   Dialect = "sqlite"
)

// DB bundles the connection pool with its dialect so repositories can adapt
// placeholders.
type DB struct {
	*sql.DB
	Dialect Dialect
}

// Open connects to the store selected by databaseURL and applies all pending
// migrations. A postgres:// URL selects PostgreSQL; anything else is treated
// as a SQLite file path (dev only).
func Open(databaseURL string) (*DB, error) {
	var (
		driver  string
		dialect Dialect
		dsn     = databaseURL
	)
	if strings.HasPrefix(databaseURL, "postgres://") || strings.HasPrefix(databaseURL, "postgresql://") {
		driver, dialect = "pgx", DialectPostgres
	} else {
		driver, dialect = "sqlite", DialectSQLite
		// Serialized access keeps SQLite happy under the pool.
		dsn = databaseURL + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if dialect == DialectSQLite {
		db.SetMaxOpenConns(1)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	if err := applyMigrations(db, dialect); err != nil {
		return nil, err
	}
	return &DB{DB: db, Dialect: dialect}, nil
}

func applyMigrations(db *sql.DB, dialect Dialect) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("loading embedded migrations: %w", err)
	}

	var driver migratedb.Driver
	switch dialect {
	case DialectPostgres:
		driver, err = migratepgx.WithInstance(db, &migratepgx.Config{})
	case DialectSQLite:
		driver, err = migratesqlite.WithInstance(db, &migratesqlite.Config{})
	}
	if err != nil {
		return fmt.Errorf("preparing migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, string(dialect), driver)
	if err != nil {
		return fmt.Errorf("preparing migrations: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return nil
}

// Rebind converts `?` placeholders to the dialect's native form ($1, $2, …
// for PostgreSQL). Repositories author queries with `?` once.
func (db *DB) Rebind(query string) string {
	if db.Dialect != DialectPostgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
