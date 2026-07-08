package store

import (
	"database/sql"
	"embed"
	"fmt"

	// mattn/go-sqlite3's init() registers the "sqlite3" driver used by
	// sql.Open below; store_helper.go imports it non-blank for sqlite3.Error.
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}

	// Enable WAL mode, busy timeout, and foreign keys for safe concurrent usage.
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: enable WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: set busy timeout: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: enable foreign keys: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func runMigrations(db *sql.DB) error {
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("store: goose dialect: %w", err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("store: migrations: %w", err)
	}

	return nil
}
