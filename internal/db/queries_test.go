package db

import (
	"context"
	"database/sql"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func clipState(t *testing.T, db *sql.DB, id int64) (status string, attempts int, deleted sql.NullInt64) {
	t.Helper()
	err := db.QueryRow(`SELECT preview_status, preview_attempts, deleted_at FROM clips WHERE id = ?`, id).
		Scan(&status, &attempts, &deleted)
	if err != nil {
		t.Fatalf("clipState: %v", err)
	}
	return
}

func TestUpsertClipLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	c := Clip{RelPath: "game/a.mp4", Name: "a", OriginalFilename: "a.mp4", SizeBytes: 100, Mtime: 1000, Kind: "video"}

	id, created, changed, err := UpsertClip(ctx, db, c)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if !created || !changed {
		t.Fatalf("first insert: created=%v changed=%v, want both true", created, changed)
	}

	// Idempotent re-scan: nothing mutated.
	if _, created, changed, err = UpsertClip(ctx, db, c); err != nil {
		t.Fatalf("noop upsert: %v", err)
	} else if created || changed {
		t.Fatalf("noop upsert: created=%v changed=%v, want both false", created, changed)
	}

	// Pretend the preview ran and failed a few times.
	if _, err := db.ExecContext(ctx,
		`UPDATE clips SET preview_status='done', preview_attempts=3 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}

	// mtime drift → back to pending, attempts reset.
	drift := c
	drift.Mtime = 2000
	if _, created, changed, err = UpsertClip(ctx, db, drift); err != nil {
		t.Fatalf("drift upsert: %v", err)
	} else if created || !changed {
		t.Fatalf("drift upsert: created=%v changed=%v, want changed only", created, changed)
	}
	if status, attempts, _ := clipState(t, db, id); status != "pending" || attempts != 0 {
		t.Fatalf("after drift: status=%q attempts=%d, want pending/0", status, attempts)
	}

	// Mark done again, then soft-delete.
	if _, err := db.ExecContext(ctx,
		`UPDATE clips SET preview_status='done', deleted_at=42 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}

	// Restore of an unchanged file keeps preview_status (no re-probe).
	restore := drift // same size/mtime as current row
	if _, created, changed, err = UpsertClip(ctx, db, restore); err != nil {
		t.Fatalf("restore upsert: %v", err)
	} else if created || !changed {
		t.Fatalf("restore upsert: created=%v changed=%v, want changed only", created, changed)
	}
	if status, _, deleted := clipState(t, db, id); status != "done" || deleted.Valid {
		t.Fatalf("after restore: status=%q deleted=%v, want done/NULL", status, deleted)
	}
}

func seedClip(t *testing.T, db *sql.DB, rel, name string, catID sql.NullInt64) int64 {
	t.Helper()
	ctx := context.Background()
	id, _, _, err := UpsertClip(ctx, db, Clip{
		RelPath: rel, Name: name, OriginalFilename: name + ".mp4",
		SizeBytes: 1, Mtime: 1, CategoryID: catID, Kind: "video",
	})
	if err != nil {
		t.Fatalf("seedClip: %v", err)
	}
	// UpsertClip inserts as pending; mark done so it isn't special-cased.
	if _, err := db.ExecContext(ctx, `UPDATE clips SET preview_status='done' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestBulkTagAndCategory(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	c1 := seedClip(t, db, "g/a.mp4", "a", sql.NullInt64{})
	c2 := seedClip(t, db, "g/b.mp4", "b", sql.NullInt64{})
	catID, err := UpsertCategory(ctx, db, "cat", "Cat")
	if err != nil {
		t.Fatal(err)
	}
	tag, err := UpsertTag(ctx, db, "existing", false)
	if err != nil {
		t.Fatal(err)
	}

	// tag_id + create-if-missing names in one shot.
	if err := BulkTag(ctx, db, []int64{c1, c2}, tag.ID, []string{"fresh"}); err != nil {
		t.Fatalf("BulkTag: %v", err)
	}
	tags, err := TagsForClip(ctx, db, c1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 {
		t.Fatalf("c1 tags=%d, want 2", len(tags))
	}

	// Unknown tag id → ErrInvalidRef.
	if err := BulkTag(ctx, db, []int64{c1}, 99999, nil); err != ErrInvalidRef {
		t.Fatalf("BulkTag bad id: %v, want ErrInvalidRef", err)
	}

	// Assign then clear (declassify via 0).
	if err := BulkSetCategory(ctx, db, []int64{c1, c2}, catID); err != nil {
		t.Fatalf("BulkSetCategory assign: %v", err)
	}
	if got := categoryOf(t, db, c1); !got.Valid || got.Int64 != catID {
		t.Fatalf("c1 category=%v, want %d", got, catID)
	}
	if err := BulkSetCategory(ctx, db, []int64{c1}, 0); err != nil {
		t.Fatalf("BulkSetCategory clear: %v", err)
	}
	if got := categoryOf(t, db, c1); got.Valid {
		t.Fatalf("c1 category=%v after clear, want NULL", got)
	}

	// Unknown category id → ErrInvalidRef.
	if err := BulkSetCategory(ctx, db, []int64{c1}, 99999); err != ErrInvalidRef {
		t.Fatalf("BulkSetCategory bad id: %v, want ErrInvalidRef", err)
	}
}

func categoryOf(t *testing.T, db *sql.DB, id int64) sql.NullInt64 {
	t.Helper()
	var c sql.NullInt64
	if err := db.QueryRow(`SELECT category_id FROM clips WHERE id=?`, id).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestBuildFTSQuery(t *testing.T) {
	// Quotes and FTS operators must not leak into the MATCH expression.
	cases := map[string]string{
		"foo":      `"foo"*`,
		`foo"`:     `"foo"*`,
		`foo bar`:  `"foo"* "bar"*`,
		`foo" OR`:  `"foo"* "OR"*`,
		"  ":       "",
		`'quoted'`: `"quoted"*`,
	}
	for in, want := range cases {
		if got := buildFTSQuery(in); got != want {
			t.Errorf("buildFTSQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListClipsInjectionSafe(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedClip(t, db, "game/a.mp4", "alpha", sql.NullInt64{})
	seedClip(t, db, "game/b.mp4", "beta", sql.NullInt64{})

	// None of these must error the MATCH parse.
	for _, q := range []string{`foo" OR`, `"; DROP TABLE clips`, `a"b`, `*`, `()`, `NEAR`} {
		if _, _, err := ListClips(ctx, db, ListParams{Query: q, Limit: 10}); err != nil {
			t.Fatalf("ListClips(q=%q): %v", q, err)
		}
	}
}

func TestListClipsFiltersAndSort(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	visID, err := UpsertCategory(ctx, db, "visible", "Visible")
	if err != nil {
		t.Fatal(err)
	}
	hidID, err := UpsertCategory(ctx, db, "hidden", "Hidden")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE categories SET hidden=1 WHERE id=?`, hidID); err != nil {
		t.Fatal(err)
	}

	seedClip(t, db, "Visible/apple.mp4", "apple", sql.NullInt64{Int64: visID, Valid: true})
	seedClip(t, db, "Visible/Cherry.mp4", "Cherry", sql.NullInt64{Int64: visID, Valid: true})
	bID := seedClip(t, db, "Visible/banana.mp4", "banana", sql.NullInt64{Int64: visID, Valid: true})
	seedClip(t, db, "Hidden/secret.mp4", "secret", sql.NullInt64{Int64: hidID, Valid: true})

	// A screenshot must not surface under the default (video) kind filter.
	if _, _, _, err := UpsertClip(ctx, db, Clip{
		RelPath: "Visible/shot.png", Name: "shot", OriginalFilename: "shot.png",
		SizeBytes: 1, Mtime: 1, CategoryID: sql.NullInt64{Int64: visID, Valid: true}, Kind: "screenshot",
	}); err != nil {
		t.Fatal(err)
	}

	// Attach a tag to banana and filter by it.
	tag, err := UpsertTag(ctx, db, "clutch", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := AttachTag(ctx, db, bID, tag.ID); err != nil {
		t.Fatal(err)
	}

	// Default listing: 3 videos in the visible category, hidden one excluded.
	clips, total, err := ListClips(ctx, db, ListParams{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(clips) != 3 {
		t.Fatalf("default list: total=%d len=%d, want 3/3", total, len(clips))
	}

	// name_desc, case-insensitive: Cherry, banana, apple.
	clips, _, err = ListClips(ctx, db, ListParams{Sort: "name_desc", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	gotOrder := []string{clips[0].Name, clips[1].Name, clips[2].Name}
	wantOrder := []string{"Cherry", "banana", "apple"}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("name_desc order = %v, want %v", gotOrder, wantOrder)
		}
	}

	// Browsing the hidden category directly returns its clip.
	_, total, err = ListClips(ctx, db, ListParams{CategoryID: hidID, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("hidden category list total=%d, want 1", total)
	}

	// Tag filter narrows to banana only.
	clips, total, err = ListClips(ctx, db, ListParams{TagIDs: []int64{tag.ID}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(clips) != 1 || clips[0].ID != bID {
		t.Fatalf("tag filter: total=%d len=%d, want 1 clip (banana)", total, len(clips))
	}

	// FTS query stays consistent between page and count.
	clips, total, err = ListClips(ctx, db, ListParams{Query: "apple", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != len(clips) || total != 1 {
		t.Fatalf("fts apple: total=%d len=%d, want 1/1", total, len(clips))
	}
}
