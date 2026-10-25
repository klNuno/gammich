package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Clip struct {
	ID               int64
	RelPath          string
	Name             string
	OriginalFilename string
	SizeBytes        int64
	Mtime            int64
	DurationS        sql.NullFloat64
	Width            sql.NullInt64
	Height           sql.NullInt64
	Codec            sql.NullString
	CategoryID       sql.NullInt64
	CategoryName     sql.NullString
	Kind             string
	PreviewStatus    string
	AudioTrackCount  int64
	AudioCodec       sql.NullString
	AudioRate        int64
	AudioChannels    int64
	AudioTrackTitles sql.NullString // JSON array, see migration 0012
	MoovAtEnd        int64
	DeletedAt        sql.NullInt64
	CreatedAt        int64
	UpdatedAt        int64
}

type Category struct {
	ID        int64          `json:"id"`
	Slug      string         `json:"slug"`
	Name      string         `json:"name"`
	Hidden    bool           `json:"hidden"`
	ImageURL  sql.NullString `json:"-"`
	ClipCount int64          `json:"clip_count"`
	// Set by ListCategories only.
	LatestMtime  int64   `json:"-"`
	CoverClipIDs []int64 `json:"-"`
}

type Tag struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Auto bool   `json:"auto"`
}

type TagWithCount struct {
	Tag
	ClipCount int64 `json:"clip_count"`
}

type Stats struct {
	ClipsTotal      int64 `json:"clips_total"`
	ClipsActive     int64 `json:"clips_active"`
	ClipsDeleted    int64 `json:"clips_deleted"`
	BytesTotal      int64 `json:"bytes_total"`
	TagsTotal       int64 `json:"tags_total"`
	TagsAuto        int64 `json:"tags_auto"`
	CategoriesTotal int64 `json:"categories_total"`
	PreviewDone     int64 `json:"preview_done"`
	PreviewPending  int64 `json:"preview_pending"`
	PreviewFailed   int64 `json:"preview_failed"`
}

// DBTX is satisfied by both *sql.DB and *sql.Tx so the tag helpers can run
// inside or outside a transaction.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ErrInvalidRef signals a mutation referencing a tag_id or category_id that
// does not exist. Handlers map it to 400 rather than 500.
var ErrInvalidRef = errors.New("invalid reference")

func isFKErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY")
}

func mapRefErr(err error) error {
	if isFKErr(err) {
		return ErrInvalidRef
	}
	return err
}

func ensureTag(ctx context.Context, db DBTX, id int64) error {
	var x int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM tags WHERE id = ?`, id).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidRef
	}
	return err
}

func ensureCategory(ctx context.Context, db DBTX, id int64) error {
	var x int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM categories WHERE id = ?`, id).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidRef
	}
	return err
}

// UpsertCategory returns the category for slug, creating it when missing. An
// existing row keeps its name: the user may have renamed it, and naming the
// column in the conflict SET would fire trg_categories_fts_rename (migration
// 0010), rewriting the FTS rows of every clip in the category on each scan.
func UpsertCategory(ctx context.Context, db DBTX, slug, name string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO categories(slug, name) VALUES(?, ?)
		ON CONFLICT(slug) DO UPDATE SET slug = excluded.slug
		RETURNING id`, slug, name).Scan(&id)
	return id, err
}

// UpsertClip inserts or refreshes a clip row. Returns (id, created, changed):
// changed is true only when the row was actually mutated (new file, size/mtime
// drift, or restore from soft-delete).
func UpsertClip(ctx context.Context, db *sql.DB, c Clip) (int64, bool, bool, error) {
	now := time.Now().Unix()

	var id int64
	var size, mtime int64
	var deletedAt sql.NullInt64
	err := db.QueryRowContext(ctx,
		`SELECT id, size_bytes, mtime, deleted_at FROM clips WHERE rel_path = ?`, c.RelPath,
	).Scan(&id, &size, &mtime, &deletedAt)

	kind := c.Kind
	if kind == "" {
		kind = "video"
	}
	if errors.Is(err, sql.ErrNoRows) {
		// Screenshots go through the worker too, which makes their thumb
		// (otherwise the grid serves full-size originals).
		previewStatus := "pending"
		res, err := db.ExecContext(ctx, `
			INSERT INTO clips(rel_path, name, original_filename, size_bytes, mtime, category_id, kind, preview_status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.RelPath, c.Name, c.OriginalFilename, c.SizeBytes, c.Mtime,
			nullableInt(c.CategoryID), kind, previewStatus, now, now)
		if err != nil {
			return 0, false, false, err
		}
		newID, err := res.LastInsertId()
		return newID, true, true, err
	}
	if err != nil {
		return 0, false, false, err
	}

	if size != c.SizeBytes || mtime != c.Mtime {
		_, err := db.ExecContext(ctx, `
			UPDATE clips SET size_bytes = ?, mtime = ?, deleted_at = NULL,
				preview_status = 'pending', preview_attempts = 0, updated_at = ?
			WHERE id = ?`,
			c.SizeBytes, c.Mtime, now, id)
		if err != nil {
			return 0, false, false, err
		}
		return id, false, true, nil
	}
	if deletedAt.Valid {
		// Restore after a mount flap: content unchanged, keep the existing
		// preview_status so the whole library isn't re-probed.
		_, err := db.ExecContext(ctx, `
			UPDATE clips SET deleted_at = NULL, updated_at = ? WHERE id = ?`, now, id)
		if err != nil {
			return 0, false, false, err
		}
		return id, false, true, nil
	}
	return id, false, false, nil
}

func SoftDeleteMissing(ctx context.Context, db *sql.DB, seenIDs map[int64]struct{}) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM clips WHERE deleted_at IS NULL`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var missing []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		if _, ok := seenIDs[id]; !ok {
			missing = append(missing, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	now := time.Now().Unix()
	for _, id := range missing {
		if _, err := db.ExecContext(ctx,
			`UPDATE clips SET deleted_at = ?, updated_at = ? WHERE id = ?`,
			now, now, id); err != nil {
			return 0, err
		}
	}
	return len(missing), nil
}

type ListParams struct {
	Query      string
	CategoryID int64
	TagIDs     []int64
	Sort       string
	Limit      int
	Offset     int
	Kind       string
}

func ListClips(ctx context.Context, db *sql.DB, p ListParams) ([]Clip, int, error) {
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 60
	}
	whereArgs := []any{}
	where := []string{"c.deleted_at IS NULL"}

	// Default to videos (legacy); explicit "all" disables the filter.
	kind := strings.TrimSpace(p.Kind)
	if kind == "" {
		kind = "video"
	}
	if kind != "all" {
		where = append(where, "c.kind = ?")
		whereArgs = append(whereArgs, kind)
	}

	if p.CategoryID > 0 {
		where = append(where, "c.category_id = ?")
		whereArgs = append(whereArgs, p.CategoryID)
	} else {
		// hide clips whose category is flagged hidden, unless explicitly browsing that category.
		where = append(where, "(c.category_id IS NULL OR c.category_id NOT IN (SELECT id FROM categories WHERE hidden = 1))")
	}
	// Share a single FTS MATCH between the page query and the COUNT via a CTE.
	cte := ""
	args := []any{}
	if q := buildFTSQuery(p.Query); q != "" {
		cte = "WITH matched AS (SELECT rowid FROM clips_fts WHERE clips_fts MATCH ?)\n"
		where = append(where, "c.id IN (SELECT rowid FROM matched)")
		args = append(args, q)
	}
	for _, tid := range p.TagIDs {
		where = append(where, "c.id IN (SELECT clip_id FROM clip_tags WHERE tag_id = ?)")
		whereArgs = append(whereArgs, tid)
	}
	args = append(args, whereArgs...)

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	order := "c.mtime DESC"
	switch p.Sort {
	case "size_desc":
		order = "c.size_bytes DESC"
	case "size_asc":
		order = "c.size_bytes ASC"
	case "name_asc":
		order = "c.name COLLATE NOCASE ASC"
	case "name_desc":
		order = "c.name COLLATE NOCASE DESC"
	case "date_asc":
		order = "c.mtime ASC"
	case "duration_desc":
		order = "c.duration_s DESC" // NULLs sort last on DESC
	case "duration_asc":
		order = "c.duration_s IS NULL, c.duration_s ASC" // keep NULLs last
	}

	q := cte + `
SELECT ` + clipColumns + `
FROM clips c
LEFT JOIN categories cat ON cat.id = c.category_id
` + whereSQL + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`

	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, p.Limit, p.Offset)

	rows, err := db.QueryContext(ctx, q, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Clip
	for rows.Next() {
		c, err := scanClip(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	countQ := cte + `SELECT COUNT(*) FROM clips c ` + whereSQL
	var total int
	if err := db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// clipColumns is the SELECT list scanClip expects, over clips c LEFT JOIN
// categories cat.
const clipColumns = `c.id, c.rel_path, c.name, c.original_filename, c.size_bytes, c.mtime,
       c.duration_s, c.width, c.height, c.codec,
       c.category_id, cat.name, c.kind, c.preview_status, c.audio_track_count,
       c.audio_codec, c.audio_rate, c.audio_channels, c.audio_track_titles,
       c.moov_at_end, c.created_at, c.updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanClip(r rowScanner) (Clip, error) {
	var c Clip
	err := r.Scan(
		&c.ID, &c.RelPath, &c.Name, &c.OriginalFilename, &c.SizeBytes, &c.Mtime,
		&c.DurationS, &c.Width, &c.Height, &c.Codec,
		&c.CategoryID, &c.CategoryName, &c.Kind, &c.PreviewStatus, &c.AudioTrackCount,
		&c.AudioCodec, &c.AudioRate, &c.AudioChannels, &c.AudioTrackTitles,
		&c.MoovAtEnd, &c.CreatedAt, &c.UpdatedAt,
	)
	return c, err
}

func GetClip(ctx context.Context, db *sql.DB, id int64) (*Clip, error) {
	c, err := scanClip(db.QueryRowContext(ctx, `
SELECT `+clipColumns+`
FROM clips c
LEFT JOIN categories cat ON cat.id = c.category_id
WHERE c.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// categoryCovers is how many recent clip thumbnails ListCategories returns
// per category, for the mosaic shown when Steam has no cover.
const categoryCovers = 4

func ListCategories(ctx context.Context, db *sql.DB) ([]Category, error) {
	rows, err := db.QueryContext(ctx, `
SELECT cat.id, cat.slug, cat.name, cat.hidden, cat.image_url,
       COALESCE(agg.n, 0) AS clip_count, COALESCE(agg.latest, 0)
FROM categories cat
LEFT JOIN (SELECT category_id, COUNT(*) AS n, MAX(mtime) AS latest
           FROM clips WHERE deleted_at IS NULL GROUP BY category_id) agg
       ON agg.category_id = cat.id
ORDER BY clip_count DESC, cat.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	var out []Category
	idx := map[int64]int{}
	for rows.Next() {
		var c Category
		var hidden int64
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &hidden, &c.ImageURL, &c.ClipCount, &c.LatestMtime); err != nil {
			rows.Close()
			return nil, err
		}
		c.Hidden = hidden != 0
		idx[c.ID] = len(out)
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	covers, err := db.QueryContext(ctx, `
SELECT category_id, id FROM (
  SELECT category_id, id,
         ROW_NUMBER() OVER (PARTITION BY category_id ORDER BY mtime DESC) AS rn
  FROM clips
  WHERE deleted_at IS NULL AND category_id IS NOT NULL
    AND kind = 'video' AND preview_status = 'done')
WHERE rn <= ?
ORDER BY category_id, rn`, categoryCovers)
	if err != nil {
		return nil, err
	}
	defer covers.Close()
	for covers.Next() {
		var catID, clipID int64
		if err := covers.Scan(&catID, &clipID); err != nil {
			return nil, err
		}
		if i, ok := idx[catID]; ok {
			out[i].CoverClipIDs = append(out[i].CoverClipIDs, clipID)
		}
	}
	return out, covers.Err()
}

// CategoriesForImageRefresh returns every category with active clips, capped
// at limit. image_url is deliberately not filtered: refresh re-fetches and
// overwrites stale URLs (e.g. old landscape headers).
func CategoriesForImageRefresh(ctx context.Context, db *sql.DB, limit int) ([]Category, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := db.QueryContext(ctx, `
SELECT id, slug, name, hidden, image_url, 0
FROM categories
WHERE EXISTS (SELECT 1 FROM clips c WHERE c.category_id = categories.id AND c.deleted_at IS NULL)
ORDER BY name COLLATE NOCASE
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		var hidden int64
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &hidden, &c.ImageURL, &c.ClipCount); err != nil {
			return nil, err
		}
		c.Hidden = hidden != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

func SetCategoryImageURL(ctx context.Context, db *sql.DB, id int64, url string) error {
	_, err := db.ExecContext(ctx, `UPDATE categories SET image_url = ? WHERE id = ?`, url, id)
	return err
}

// CreateCategory inserts a new category; returns (nil, nil) when the slug
// already exists.
func CreateCategory(ctx context.Context, db *sql.DB, slug, name string) (*Category, error) {
	var c Category
	var hidden int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO categories(slug, name) VALUES(?, ?)
		ON CONFLICT(slug) DO NOTHING
		RETURNING id, slug, name, hidden`, slug, name).
		Scan(&c.ID, &c.Slug, &c.Name, &hidden)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Hidden = hidden != 0
	return &c, nil
}

// ClearCategoryImages wipes image_url for all categories and returns how many
// rows were touched.
func ClearCategoryImages(ctx context.Context, db *sql.DB) (int64, error) {
	res, err := db.ExecContext(ctx, `UPDATE categories SET image_url = NULL`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// AttachAutoTags upserts each name as an auto tag and attaches it to the clip.
// Safe to call with nil/empty input.
func AttachAutoTags(ctx context.Context, db *sql.DB, clipID int64, names []string) error {
	for _, name := range names {
		if name == "" {
			continue
		}
		t, err := UpsertTag(ctx, db, name, true)
		if err != nil {
			return err
		}
		if err := AttachTag(ctx, db, clipID, t.ID); err != nil {
			return err
		}
	}
	return nil
}

// SearchAll returns up to `limit` matches across tags, categories, and clips.
func SearchAll(ctx context.Context, db *sql.DB, query string, limit int) (tags []TagWithCount, cats []Category, clips []Clip, err error) {
	if limit <= 0 || limit > 20 {
		limit = 6
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil, nil, nil
	}
	tags, err = ListTagsWithCounts(ctx, db, q, limit)
	if err != nil {
		return nil, nil, nil, err
	}
	esc := escapeLike(q)
	rows, err := db.QueryContext(ctx, `
SELECT cat.id, cat.slug, cat.name, cat.hidden, cat.image_url,
       COALESCE((SELECT COUNT(*) FROM clips c WHERE c.category_id = cat.id AND c.deleted_at IS NULL), 0)
FROM categories cat
WHERE cat.name LIKE ? ESCAPE '\'
  AND EXISTS (SELECT 1 FROM clips c WHERE c.category_id = cat.id AND c.deleted_at IS NULL)
ORDER BY (CASE WHEN cat.name LIKE ? ESCAPE '\' THEN 0 ELSE 1 END), cat.name COLLATE NOCASE
LIMIT ?`, "%"+esc+"%", esc+"%", limit)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Category
		var hidden int64
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &hidden, &c.ImageURL, &c.ClipCount); err != nil {
			return nil, nil, nil, err
		}
		c.Hidden = hidden != 0
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}

	clips, _, err = ListClips(ctx, db, ListParams{Query: q, Limit: limit, Offset: 0})
	if err != nil {
		return nil, nil, nil, err
	}
	return tags, cats, clips, nil
}

func ListTags(ctx context.Context, db *sql.DB) ([]Tag, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, slug, name, auto FROM tags ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var t Tag
		var auto int64
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &auto); err != nil {
			return nil, err
		}
		t.Auto = auto != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

func ListTagsWithCounts(ctx context.Context, db *sql.DB, query string, limit int) ([]TagWithCount, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	args := []any{}
	where := ""
	if q := strings.TrimSpace(query); q != "" {
		where = `WHERE t.name LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(q)+"%")
	}
	sqlQ := `
SELECT t.id, t.slug, t.name, t.auto,
       COALESCE((
         SELECT COUNT(*) FROM clip_tags ct
         JOIN clips c ON c.id = ct.clip_id
         WHERE ct.tag_id = t.id AND c.deleted_at IS NULL
       ), 0) AS cnt
FROM tags t
` + where + `
ORDER BY cnt DESC, t.name COLLATE NOCASE
LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, sqlQ, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagWithCount
	for rows.Next() {
		var t TagWithCount
		var auto int64
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &auto, &t.ClipCount); err != nil {
			return nil, err
		}
		t.Auto = auto != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

func GetStats(ctx context.Context, db *sql.DB) (Stats, error) {
	var s Stats
	row := db.QueryRowContext(ctx, `
SELECT
  COUNT(*),
  COALESCE(SUM(CASE WHEN deleted_at IS NULL THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN deleted_at IS NOT NULL THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN deleted_at IS NULL THEN size_bytes ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN deleted_at IS NULL AND preview_status='done' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN deleted_at IS NULL AND preview_status='pending' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN deleted_at IS NULL AND preview_status='failed' THEN 1 ELSE 0 END), 0)
FROM clips`)
	if err := row.Scan(&s.ClipsTotal, &s.ClipsActive, &s.ClipsDeleted, &s.BytesTotal,
		&s.PreviewDone, &s.PreviewPending, &s.PreviewFailed); err != nil {
		return s, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(auto), 0) FROM tags`).
		Scan(&s.TagsTotal, &s.TagsAuto); err != nil {
		return s, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM categories`).Scan(&s.CategoriesTotal); err != nil {
		return s, err
	}
	return s, nil
}

func UpsertTag(ctx context.Context, db DBTX, name string, auto bool) (Tag, error) {
	slug := Slugify(name)
	if slug == "" {
		return Tag{}, errors.New("empty tag name")
	}
	a := 0
	if auto {
		a = 1
	}
	// Existing rows keep their auto flag and name. Naming `name` in the
	// conflict SET would fire trg_tags_fts_rename (migration 0010) and rewrite
	// the FTS rows of every clip carrying the tag, once per preview job for
	// the tech tags. Renames go through RenameTag.
	var t Tag
	var autoCol int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO tags(slug, name, auto) VALUES(?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET slug = excluded.slug
		RETURNING id, slug, name, auto`, slug, name, a).
		Scan(&t.ID, &t.Slug, &t.Name, &autoCol)
	if err != nil {
		return Tag{}, err
	}
	t.Auto = autoCol != 0
	return t, nil
}

func AttachTag(ctx context.Context, db DBTX, clipID, tagID int64) error {
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO clip_tags(clip_id, tag_id) VALUES(?, ?)`, clipID, tagID)
	return err
}

func CreateTag(ctx context.Context, db *sql.DB, name string) (Tag, error) {
	return UpsertTag(ctx, db, name, false)
}

// ErrConflict signals a rename onto a name another row already owns.
var ErrConflict = errors.New("name already taken")

// ErrAutoTag refuses edits to scanner/preview-owned tags: the next scan would
// recreate them under their original slug.
var ErrAutoTag = errors.New("auto tag")

// RenameTag renames a manual tag, slug included. Returns (nil, nil) when the
// tag does not exist.
func RenameTag(ctx context.Context, db *sql.DB, id int64, name string) (*Tag, error) {
	slug := Slugify(name)
	if slug == "" {
		return nil, errors.New("empty tag name")
	}
	var auto int64
	err := db.QueryRowContext(ctx, `SELECT auto FROM tags WHERE id = ?`, id).Scan(&auto)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if auto != 0 {
		return nil, ErrAutoTag
	}
	var other int64
	err = db.QueryRowContext(ctx, `SELECT id FROM tags WHERE slug = ? AND id <> ?`, slug, id).Scan(&other)
	if err == nil {
		return nil, ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var t Tag
	err = db.QueryRowContext(ctx, `
		UPDATE tags SET name = ?, slug = ? WHERE id = ?
		RETURNING id, slug, name, auto`, name, slug, id).Scan(&t.ID, &t.Slug, &t.Name, &auto)
	if err != nil {
		return nil, err
	}
	t.Auto = auto != 0
	return &t, nil
}

// UpdateCategory changes a category's display name and/or hidden flag. The
// slug stays: it ties the row to its folder for the scanner. Returns
// (nil, nil) when the category does not exist.
func UpdateCategory(ctx context.Context, db *sql.DB, id int64, name *string, hidden *bool) (*Category, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM categories WHERE id = ?`, id).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, nil
	}
	if name != nil {
		// Skip a same-name write: the FTS rename trigger fires on any SET name.
		if _, err := tx.ExecContext(ctx,
			`UPDATE categories SET name = ? WHERE id = ? AND name <> ?`, *name, id, *name); err != nil {
			return nil, err
		}
	}
	if hidden != nil {
		h := 0
		if *hidden {
			h = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE categories SET hidden = ? WHERE id = ?`, h, id); err != nil {
			return nil, err
		}
	}
	var c Category
	var hid int64
	err = tx.QueryRowContext(ctx, `
		SELECT id, slug, name, hidden, image_url,
		       (SELECT COUNT(*) FROM clips WHERE category_id = categories.id AND deleted_at IS NULL)
		FROM categories WHERE id = ?`, id).
		Scan(&c.ID, &c.Slug, &c.Name, &hid, &c.ImageURL, &c.ClipCount)
	if err != nil {
		return nil, err
	}
	c.Hidden = hid != 0
	return &c, tx.Commit()
}

func DeleteTag(ctx context.Context, db *sql.DB, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
	return err
}

func TagsForClip(ctx context.Context, db *sql.DB, clipID int64) ([]Tag, error) {
	rows, err := db.QueryContext(ctx, `
SELECT t.id, t.slug, t.name, t.auto
FROM clip_tags ct JOIN tags t ON t.id = ct.tag_id
WHERE ct.clip_id = ?
ORDER BY t.auto DESC, t.name COLLATE NOCASE`, clipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var t Tag
		var auto int64
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &auto); err != nil {
			return nil, err
		}
		t.Auto = auto != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

func TagsForClips(ctx context.Context, db *sql.DB, clipIDs []int64) (map[int64][]Tag, error) {
	out := make(map[int64][]Tag, len(clipIDs))
	if len(clipIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(clipIDs))
	args := make([]any, len(clipIDs))
	for i, id := range clipIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `
SELECT ct.clip_id, t.id, t.slug, t.name, t.auto
FROM clip_tags ct JOIN tags t ON t.id = ct.tag_id
WHERE ct.clip_id IN (` + strings.Join(placeholders, ",") + `)
ORDER BY t.auto DESC, t.name COLLATE NOCASE`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var clipID int64
		var t Tag
		var auto int64
		if err := rows.Scan(&clipID, &t.ID, &t.Slug, &t.Name, &auto); err != nil {
			return nil, err
		}
		t.Auto = auto != 0
		out[clipID] = append(out[clipID], t)
	}
	return out, rows.Err()
}

// ClipPatch carries the mutations applied by PatchClip. Nil pointer fields
// are left untouched; values are expected pre-validated by the caller.
type ClipPatch struct {
	Name         *string
	TagIDs       *[]int64
	AddTagNames  []string
	RemoveTagIDs []int64
	CategoryID   *int64
}

// PatchClip applies every mutation of a clip PATCH in a single transaction.
func PatchClip(ctx context.Context, db *sql.DB, clipID int64, p ClipPatch) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if p.Name != nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE clips SET name = ?, updated_at = ? WHERE id = ?`, *p.Name, now, clipID); err != nil {
			return err
		}
	}
	if p.TagIDs != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM clip_tags WHERE clip_id = ?`, clipID); err != nil {
			return err
		}
		for _, tid := range *p.TagIDs {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO clip_tags(clip_id, tag_id) VALUES(?, ?)`, clipID, tid); err != nil {
				return mapRefErr(err)
			}
		}
	}
	if p.CategoryID != nil {
		if *p.CategoryID > 0 {
			if err := ensureCategory(ctx, tx, *p.CategoryID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE clips SET category_id = ?, updated_at = ? WHERE id = ?`, *p.CategoryID, now, clipID); err != nil {
				return err
			}
		} else {
			// 0 clears the category (declassify), matching the bulk convention.
			if _, err := tx.ExecContext(ctx,
				`UPDATE clips SET category_id = NULL, updated_at = ? WHERE id = ?`, now, clipID); err != nil {
				return err
			}
		}
	}
	for _, name := range p.AddTagNames {
		t, err := UpsertTag(ctx, tx, name, false)
		if err != nil {
			return err
		}
		if err := AttachTag(ctx, tx, clipID, t.ID); err != nil {
			return err
		}
	}
	for _, tid := range p.RemoveTagIDs {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM clip_tags WHERE clip_id = ? AND tag_id = ?`, clipID, tid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BulkTag attaches an existing tag (by id) and/or a set of create-if-missing
// tag names to every clip, in one transaction. An unknown tagID (>0) yields
// ErrInvalidRef. Pass tagID==0 to attach names only.
func BulkTag(ctx context.Context, db *sql.DB, clipIDs []int64, tagID int64, tagNames []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var tagIDs []int64
	if tagID > 0 {
		if err := ensureTag(ctx, tx, tagID); err != nil {
			return err
		}
		tagIDs = append(tagIDs, tagID)
	}
	for _, name := range tagNames {
		t, err := UpsertTag(ctx, tx, name, false)
		if err != nil {
			return err
		}
		tagIDs = append(tagIDs, t.ID)
	}
	for _, cid := range clipIDs {
		for _, tid := range tagIDs {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO clip_tags(clip_id, tag_id) VALUES(?, ?)`, cid, tid); err != nil {
				return mapRefErr(err)
			}
		}
	}
	return tx.Commit()
}

// BulkSetCategory assigns category_id to every clip, or clears it (NULL) when
// categoryID is 0. An unknown category (>0) yields ErrInvalidRef.
func BulkSetCategory(ctx context.Context, db *sql.DB, clipIDs []int64, categoryID int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if categoryID > 0 {
		if err := ensureCategory(ctx, tx, categoryID); err != nil {
			return err
		}
		for _, cid := range clipIDs {
			if _, err := tx.ExecContext(ctx,
				`UPDATE clips SET category_id = ?, updated_at = ? WHERE id = ?`, categoryID, now, cid); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	for _, cid := range clipIDs {
		if _, err := tx.ExecContext(ctx,
			`UPDATE clips SET category_id = NULL, updated_at = ? WHERE id = ?`, now, cid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// buildFTSQuery turns user input into a safe FTS5 MATCH expression.
// Each token becomes a quoted prefix (foo -> "foo"*), so the query stays
// permissive without exposing FTS5 syntax.
func buildFTSQuery(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	tokens := strings.Fields(s)
	for i, tok := range tokens {
		// Drop quotes inside token, keep letters/digits and a couple of
		// common separators by replacing them with space (re-tokenised).
		clean := strings.Map(func(r rune) rune {
			if r == '"' || r == '\'' {
				return -1
			}
			return r
		}, tok)
		if clean == "" {
			continue
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('"')
		b.WriteString(clean)
		b.WriteString(`"*`)
	}
	return b.String()
}

func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r >= 0x00C0 && r <= 0x017F:
			b.WriteRune(foldDiacritic(r))
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func foldDiacritic(r rune) rune {
	switch {
	case r >= 0xC0 && r <= 0xC6:
		return 'a'
	case r >= 0xE0 && r <= 0xE6:
		return 'a'
	case r >= 0xC8 && r <= 0xCB:
		return 'e'
	case r >= 0xE8 && r <= 0xEB:
		return 'e'
	case r >= 0xCC && r <= 0xCF:
		return 'i'
	case r >= 0xEC && r <= 0xEF:
		return 'i'
	case r >= 0xD2 && r <= 0xD6, r == 0xD8:
		return 'o'
	case r >= 0xF2 && r <= 0xF6, r == 0xF8:
		return 'o'
	case r >= 0xD9 && r <= 0xDC:
		return 'u'
	case r >= 0xF9 && r <= 0xFC:
		return 'u'
	case r == 0xC7:
		return 'c'
	case r == 0xE7:
		return 'c'
	case r == 0xD1:
		return 'n'
	case r == 0xF1:
		return 'n'
	}
	return r
}

// escapeLike escapes LIKE wildcards in user input; pair with ESCAPE '\'.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func nullableInt(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

// TrackTitlesJSON encodes per-track audio names for audio_track_titles: '[]'
// when no track has a name, so "probed, nothing found" differs from NULL.
func TrackTitlesJSON(titles []string) string {
	named := false
	for _, t := range titles {
		if t != "" {
			named = true
			break
		}
	}
	if !named {
		return "[]"
	}
	b, _ := json.Marshal(titles)
	return string(b)
}

// ParseTrackTitles decodes an audio_track_titles value; nil when empty or
// invalid.
func ParseTrackTitles(raw string) []string {
	var out []string
	if json.Unmarshal([]byte(raw), &out) != nil || len(out) == 0 {
		return nil
	}
	return out
}

// CommonTrackTitles returns the most frequent track-name set among active
// clips with n audio tracks that carry the tag sourceSlug, or nil when none of
// them has names. A recorder keeps its track layout across files, so the
// named ones (OBS MKV) label the unnamed ones (OBS MP4).
func CommonTrackTitles(ctx context.Context, db *sql.DB, n int64, sourceSlug string) ([]string, error) {
	var raw string
	err := db.QueryRowContext(ctx, `
SELECT c.audio_track_titles FROM clips c
JOIN clip_tags ct ON ct.clip_id = c.id
JOIN tags t ON t.id = ct.tag_id AND t.slug = ?
WHERE c.deleted_at IS NULL AND c.audio_track_count = ?
  AND c.audio_track_titles IS NOT NULL AND c.audio_track_titles != '[]'
GROUP BY c.audio_track_titles
ORDER BY COUNT(*) DESC
LIMIT 1`, sourceSlug, n).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	titles := ParseTrackTitles(raw)
	if int64(len(titles)) != n {
		return nil, nil
	}
	return titles, nil
}
