package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Open(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "foreign_keys(ON)")
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "synchronous(NORMAL)")
	// Fire triggers for FK cascade actions too, so deleting a tag reindexes
	// clips_fts (the clip_tags cascade delete drives trg_clip_tags_del).
	params.Add("_pragma", "recursive_triggers(ON)")
	dsn := fmt.Sprintf("file:%s?%s", abs, params.Encode())

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func Migrate(db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return goose.Up(db, "migrations")
}
