package scan

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	gammichdb "github.com/klNuno/gammich/internal/db"
)

func TestShouldSkipDelete(t *testing.T) {
	cases := []struct {
		name               string
		seen, active, errs int
		skip               bool
	}{
		{"no files", 0, 100, 0, true},
		{"walk errors", 90, 100, 3, true},
		{"big drop below half", 40, 100, 0, true},
		{"healthy full", 100, 100, 0, false},
		{"healthy minor drop", 98, 100, 0, false},
		{"empty db", 0, 0, 0, true}, // seen==0 wins
		{"fresh db some files", 5, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			skip, reason := shouldSkipDelete(c.seen, c.active, c.errs)
			if skip != c.skip {
				t.Fatalf("skip=%v (%q), want %v", skip, reason, c.skip)
			}
		})
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := gammichdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := gammichdb.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func writeVideos(t *testing.T, dir string, n int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, "clip"+string(rune('a'+i))+".mp4")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func activeCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM clips WHERE deleted_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func discardScanner(db *sql.DB, root string) *Scanner {
	return &Scanner{DB: db, Root: root, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestScanSkipsDeleteWhenEmpty(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	root := t.TempDir()
	writeVideos(t, filepath.Join(root, "game"), 10)
	res, err := discardScanner(db, root).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 10 {
		t.Fatalf("seed added=%d, want 10", res.Added)
	}

	empty := t.TempDir()
	res, err = discardScanner(db, empty).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 0 {
		t.Fatalf("empty scan deleted=%d, want 0", res.Deleted)
	}
	if got := activeCount(t, db); got != 10 {
		t.Fatalf("active=%d after empty scan, want 10 (nothing deleted)", got)
	}
}

func TestScanSkipsDeleteOnBigDrop(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	root := t.TempDir()
	game := filepath.Join(root, "game")
	writeVideos(t, game, 10)
	if _, err := discardScanner(db, root).Run(ctx); err != nil {
		t.Fatal(err)
	}

	// Drop 8 of 10 files: a mount flap, not real deletions.
	entries, _ := os.ReadDir(game)
	for i := 0; i < 8; i++ {
		_ = os.Remove(filepath.Join(game, entries[i].Name()))
	}
	res, err := discardScanner(db, root).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 0 {
		t.Fatalf("big-drop scan deleted=%d, want 0", res.Deleted)
	}
	if got := activeCount(t, db); got != 10 {
		t.Fatalf("active=%d after big drop, want 10", got)
	}
}

func TestScanDeletesMissingWhenHealthy(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	root := t.TempDir()
	game := filepath.Join(root, "game")
	writeVideos(t, game, 10)
	if _, err := discardScanner(db, root).Run(ctx); err != nil {
		t.Fatal(err)
	}

	// Remove 2: above the 50% floor, so the delete pass runs.
	entries, _ := os.ReadDir(game)
	for i := 0; i < 2; i++ {
		_ = os.Remove(filepath.Join(game, entries[i].Name()))
	}
	res, err := discardScanner(db, root).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 2 {
		t.Fatalf("healthy scan deleted=%d, want 2", res.Deleted)
	}
	if got := activeCount(t, db); got != 8 {
		t.Fatalf("active=%d after healthy scan, want 8", got)
	}
}

func TestSourceTagFor(t *testing.T) {
	cases := []struct{ folder, file, want string }{
		{"Counter-Strike 2", "Counter-Strike 2 2026.01.02 - 21.14.05.03.DVR.mp4", "shadowplay"},
		{"Counter-Strike 2", "Counter-Strike 2 Screenshot 2026.01.02 - 21.14.05.03.png", "shadowplay"},
		{"Counter-Strike 2", "2026-01-02 21-14-05.mkv", "OBS"},
		{"OBS", "anything.mp4", "OBS"},
		{"allstar", "clip.mp4", "allstar"},
		// Unknown recorder: no source tag rather than a wrong one.
		{"Hollow Knight", "boss fight.mp4", ""},
		{"", "2026-01-02 21-14-05.mp4", "OBS"},
	}
	for _, c := range cases {
		if got := sourceTagFor(c.folder, c.file); got != c.want {
			t.Errorf("sourceTagFor(%q, %q) = %q, want %q", c.folder, c.file, got, c.want)
		}
	}
}
