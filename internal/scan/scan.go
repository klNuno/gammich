package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	gammichdb "github.com/klNuno/gammich/internal/db"
)

// ErrAlreadyRunning is returned when a scan is requested while another one
// (boot, ticker, or POST /scan) still holds the lock.
var ErrAlreadyRunning = errors.New("scan already running")

var videoExts = map[string]struct{}{
	".mp4": {}, ".mkv": {}, ".mov": {}, ".webm": {}, ".avi": {},
}

var imageExts = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".webp": {}, ".gif": {}, ".bmp": {},
}

type Scanner struct {
	DB   *sql.DB
	Root string
	Log  *slog.Logger

	mu sync.Mutex
}

type Result struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Deleted int `json:"deleted"`
	Errors  int `json:"errors"`
}

func (s *Scanner) Run(ctx context.Context) (Result, error) {
	if !s.mu.TryLock() {
		return Result{}, ErrAlreadyRunning
	}
	defer s.mu.Unlock()

	if s.Log == nil {
		s.Log = slog.Default()
	}
	if s.Root == "" {
		return Result{}, errors.New("scan: root not set")
	}

	start := time.Now()
	s.Log.Info("scan start", "root", s.Root)

	seen := make(map[int64]struct{}, 4096)
	res := Result{}
	videosSeen := 0

	// Per-walk caches: one upsert per category/source tag instead of per file.
	catIDs := make(map[string]int64)
	sourceTagIDs := make(map[string]int64)

	rootFS := filepath.Clean(s.Root)
	err := filepath.WalkDir(rootFS, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			s.Log.Warn("walk error", "path", path, "err", err)
			res.Errors++
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		kind := ""
		if _, ok := videoExts[ext]; ok {
			kind = "video"
		} else if _, ok := imageExts[ext]; ok {
			kind = "screenshot"
		} else {
			return nil
		}
		if kind == "video" {
			videosSeen++
		}

		info, err := d.Info()
		if err != nil {
			res.Errors++
			return nil
		}
		rel, err := filepath.Rel(rootFS, path)
		if err != nil {
			res.Errors++
			return nil
		}
		rel = filepath.ToSlash(rel)
		parts := strings.SplitN(rel, "/", 2)
		categoryName := ""
		if len(parts) == 2 {
			categoryName = parts[0]
		}

		var catID sql.NullInt64
		var autoTagID int64
		if categoryName != "" {
			slug := gammichdb.Slugify(categoryName)
			if slug == "" {
				// A non-latin folder slugifies to "": hash the original name so
				// two distinct folders don't collapse into one category.
				slug = "cat-" + shortHash(categoryName)
			}
			id, ok := catIDs[slug]
			if !ok {
				var err error
				id, err = gammichdb.UpsertCategory(ctx, s.DB, slug, categoryName)
				if err != nil {
					s.Log.Warn("upsert category", "name", categoryName, "err", err)
					res.Errors++
					return nil
				}
				catIDs[slug] = id
			}
			catID = sql.NullInt64{Int64: id, Valid: true}
		}

		filename := d.Name()
		if src := sourceTagFor(categoryName, filename); src != "" {
			tagID, ok := sourceTagIDs[src]
			if !ok {
				tag, err := gammichdb.UpsertTag(ctx, s.DB, src, true)
				if err != nil {
					s.Log.Warn("upsert source tag", "name", src, "err", err)
				} else {
					tagID = tag.ID
					sourceTagIDs[src] = tagID
				}
			}
			autoTagID = tagID
		}
		displayName := strings.TrimSuffix(filename, filepath.Ext(filename))

		clip := gammichdb.Clip{
			RelPath:          rel,
			Name:             displayName,
			OriginalFilename: filename,
			SizeBytes:        info.Size(),
			Mtime:            info.ModTime().Unix(),
			CategoryID:       catID,
			Kind:             kind,
		}
		id, created, changed, err := gammichdb.UpsertClip(ctx, s.DB, clip)
		if err != nil {
			s.Log.Warn("upsert clip", "rel", rel, "err", err)
			res.Errors++
			return nil
		}
		if autoTagID > 0 {
			if err := gammichdb.AttachTag(ctx, s.DB, id, autoTagID); err != nil {
				s.Log.Warn("attach auto tag", "clip", id, "err", err)
			}
		}
		seen[id] = struct{}{}
		switch {
		case created:
			res.Added++
		case changed:
			res.Updated++
		}
		return nil
	})
	if err != nil {
		return res, err
	}

	done := func(deleted int) (Result, error) {
		res.Deleted = deleted
		s.Log.Info("scan done",
			"added", res.Added, "updated", res.Updated, "deleted", deleted,
			"errors", res.Errors, "dur", time.Since(start))
		return res, nil
	}

	// A failed or degraded NAS mount makes the walk see few/no files, or hit
	// walk errors. Soft-deleting on that basis would nuke the library and
	// force a full re-probe once the mount returns. Skip the delete pass.
	var activeVideos int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM clips WHERE deleted_at IS NULL AND kind = 'video'`).Scan(&activeVideos); err != nil {
		s.Log.Warn("active video count failed, skipping soft-delete", "err", err)
		return done(0)
	}
	if skip, reason := shouldSkipDelete(videosSeen, activeVideos, res.Errors); skip {
		s.Log.Warn("skipping soft-delete: "+reason, "root", s.Root)
		return done(0)
	}

	deleted, err := gammichdb.SoftDeleteMissing(ctx, s.DB, seen)
	if err != nil {
		return res, err
	}
	return done(deleted)
}

// shouldSkipDelete decides whether the soft-delete pass should run. Any sign of
// a degraded mount (no files, walk errors, or a sharp drop below half the
// active library) skips it so a mount flap can't wipe the DB.
func shouldSkipDelete(videosSeen, activeVideos, errCount int) (bool, string) {
	switch {
	case videosSeen == 0:
		return true, "saw 0 video files (NAS mount missing?)"
	case errCount > 0:
		return true, fmt.Sprintf("walk hit %d errors (NAS unstable?)", errCount)
	case activeVideos > 0 && videosSeen*2 < activeVideos:
		return true, fmt.Sprintf("saw %d < 50%% of %d active", videosSeen, activeVideos)
	}
	return false, ""
}

func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return strconv.FormatUint(uint64(h.Sum32()), 16)
}

var (
	// ShadowPlay: "<Game> 2026.01.02 - 21.14.05.03.DVR.mp4", screenshots too.
	shadowPlayName = regexp.MustCompile(`\d{4}\.\d{2}\.\d{2} - \d{2}\.\d{2}\.\d{2}`)
	// OBS default: "2026-01-02 21-14-05.mkv".
	obsName = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}-\d{2}-\d{2}`)
)

// sourceTagFor returns the auto "source" tag of a file: the recorder that
// made it, from its top-level folder or its file name, or "" when unknown.
func sourceTagFor(folder, filename string) string {
	switch strings.ToLower(folder) {
	case "allstar":
		return "allstar"
	case "obs":
		return "OBS"
	}
	switch {
	case obsName.MatchString(filename):
		return "OBS"
	case shadowPlayName.MatchString(filename):
		return "shadowplay"
	}
	return ""
}
