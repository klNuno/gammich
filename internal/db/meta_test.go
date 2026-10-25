package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// The scanner upserts every category and source tag on each run, and the
// preview worker upserts tech tags on each job. Neither may touch `name`: the
// migration 0010 triggers rewrite the FTS rows of every affected clip on any
// UPDATE OF name, and a user rename must survive the next scan.
func TestUpsertsLeaveNamesAlone(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE rename_log (what TEXT);
		CREATE TRIGGER log_cat AFTER UPDATE OF name ON categories BEGIN INSERT INTO rename_log VALUES ('cat'); END;
		CREATE TRIGGER log_tag AFTER UPDATE OF name ON tags BEGIN INSERT INTO rename_log VALUES ('tag'); END;`); err != nil {
		t.Fatal(err)
	}

	catID, err := UpsertCategory(ctx, db, "apex-legends", "Apex Legends")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateCategory(ctx, db, catID, ptr("Apex"), nil); err != nil {
		t.Fatal(err)
	}
	again, err := UpsertCategory(ctx, db, "apex-legends", "Apex Legends")
	if err != nil || again != catID {
		t.Fatalf("re-upsert = %d, %v; want %d", again, err, catID)
	}
	tag, err := UpsertTag(ctx, db, "1080p", true)
	if err != nil {
		t.Fatal(err)
	}
	if t2, err := UpsertTag(ctx, db, "1080P", true); err != nil || t2.ID != tag.ID || t2.Name != "1080p" {
		t.Fatalf("re-upsert tag = %+v, %v", t2, err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM categories WHERE id = ?`, catID).Scan(&name); err != nil || name != "Apex" {
		t.Fatalf("category name after scan = %q, %v; want the user rename kept", name, err)
	}
	var fired int
	if err := db.QueryRow(`SELECT COUNT(*) FROM rename_log`).Scan(&fired); err != nil {
		t.Fatal(err)
	}
	if fired != 1 { // the explicit UpdateCategory only
		t.Fatalf("name triggers fired %d times, want 1", fired)
	}

	// Same-name update does not fire the trigger either.
	if _, err := UpdateCategory(ctx, db, catID, ptr("Apex"), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM rename_log`).Scan(&fired); err != nil || fired != 1 {
		t.Fatalf("same-name update fired the trigger (%d)", fired)
	}
}

func TestRenameTagAndUpdateCategory(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	clutch, err := CreateTag(ctx, db, "clutch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateTag(ctx, db, "ace"); err != nil {
		t.Fatal(err)
	}
	auto, err := UpsertTag(ctx, db, "60fps", true)
	if err != nil {
		t.Fatal(err)
	}

	got, err := RenameTag(ctx, db, clutch.ID, "Clutch 1v5")
	if err != nil || got == nil || got.Name != "Clutch 1v5" || got.Slug != "clutch-1v5" {
		t.Fatalf("rename = %+v, %v", got, err)
	}
	if _, err := RenameTag(ctx, db, clutch.ID, "ACE"); !errors.Is(err, ErrConflict) {
		t.Fatalf("rename onto existing slug = %v, want ErrConflict", err)
	}
	if _, err := RenameTag(ctx, db, auto.ID, "sixty"); !errors.Is(err, ErrAutoTag) {
		t.Fatalf("rename auto tag = %v, want ErrAutoTag", err)
	}
	if got, err := RenameTag(ctx, db, 9999, "x"); got != nil || err != nil {
		t.Fatalf("rename missing = %+v, %v; want nil, nil", got, err)
	}

	catID, err := UpsertCategory(ctx, db, "obs", "OBS")
	if err != nil {
		t.Fatal(err)
	}
	c, err := UpdateCategory(ctx, db, catID, nil, ptr(true))
	if err != nil || c == nil || !c.Hidden || c.Name != "OBS" {
		t.Fatalf("hide = %+v, %v", c, err)
	}
	if c, err := UpdateCategory(ctx, db, 9999, ptr("x"), nil); c != nil || err != nil {
		t.Fatalf("update missing = %+v, %v; want nil, nil", c, err)
	}
}

func TestListCategoriesCovers(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	catID, err := UpsertCategory(ctx, db, "game", "Game")
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := int64(1); i <= 6; i++ {
		id, _, _, err := UpsertClip(ctx, db, Clip{
			RelPath: "game/" + string(rune('a'+i)) + ".mp4", Name: "c", OriginalFilename: "c.mp4",
			SizeBytes: 1, Mtime: i * 100, Kind: "video", CategoryID: sql.NullInt64{Int64: catID, Valid: true}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// Clip 6 (newest) has no preview yet: skipped as a cover.
	if _, err := db.Exec(`UPDATE clips SET preview_status = 'done' WHERE id <> ?`, ids[5]); err != nil {
		t.Fatal(err)
	}
	cats, err := ListCategories(ctx, db)
	if err != nil || len(cats) != 1 {
		t.Fatalf("ListCategories = %+v, %v", cats, err)
	}
	c := cats[0]
	want := []int64{ids[4], ids[3], ids[2], ids[1]}
	if c.ClipCount != 6 || c.LatestMtime != 600 || len(c.CoverClipIDs) != 4 {
		t.Fatalf("category = %+v", c)
	}
	for i := range want {
		if c.CoverClipIDs[i] != want[i] {
			t.Fatalf("covers = %v, want %v", c.CoverClipIDs, want)
		}
	}
}

func ptr[T any](v T) *T { return &v }
