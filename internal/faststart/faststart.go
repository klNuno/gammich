// Package faststart detects MP4 files whose moov atom sits after mdat
// (typical game-recorder output: the recorder streams mdat first, writes the
// index when recording stops). Firefox's <video> demuxer fails to find the
// audio track on such files served over Range requests; Chrome copes. The fix
// is virtual (virtual.go): the moov is rebuilt in memory with shifted chunk
// offsets and spliced in front of mdat at serve time. Source files are never
// modified and nothing is written to disk.
package faststart

import (
	"context"
	"database/sql"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Values of the clips.moov_at_end column.
const (
	MoovUnknown = -1
	MoovFront   = 0
	MoovAtEnd   = 1
)

// Detect walks the top-level MP4 box headers and reports whether moov comes
// after mdat. Anything unparseable returns MoovFront: never remux what we
// don't understand.
func Detect(r io.ReadSeeker) int {
	var hdr [8]byte
	var off int64
	// Real files have a handful of top-level boxes; the bound only guards
	// against crafted streams of tiny boxes.
	for i := 0; i < 1<<16; i++ {
		if _, err := r.Seek(off, io.SeekStart); err != nil {
			return MoovFront
		}
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return MoovFront
		}
		for _, b := range hdr[4:8] {
			if b < 0x20 || b > 0x7e {
				return MoovFront
			}
		}
		switch string(hdr[4:8]) {
		case "moov":
			return MoovFront
		case "mdat":
			return MoovAtEnd
		}
		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		switch size {
		case 0:
			// Box extends to EOF and is neither moov nor mdat: give up.
			return MoovFront
		case 1:
			var large [8]byte
			if _, err := io.ReadFull(r, large[:]); err != nil {
				return MoovFront
			}
			size = int64(binary.BigEndian.Uint64(large[:]))
			if size < 16 {
				return MoovFront
			}
		default:
			if size < 8 {
				return MoovFront
			}
		}
		off += size
	}
	return MoovFront
}

// DetectFile opens path and runs Detect; unreadable files report MoovFront.
func DetectFile(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return MoovFront
	}
	defer f.Close()
	return Detect(f)
}

// Backfill resolves moov_at_end for active video clips still at MoovUnknown
// (rows created before the column existed, or ingested externally). A few
// header reads per file: thousands of clips finish in seconds on a warm mount.
func Backfill(ctx context.Context, db *sql.DB, videoRoot string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	// Collect first: the pool is capped at one connection, so updating while
	// the rows cursor is open would deadlock.
	rows, err := db.QueryContext(ctx, `
		SELECT id, rel_path FROM clips
		WHERE deleted_at IS NULL AND kind = 'video' AND moov_at_end = ?`, MoovUnknown)
	if err != nil {
		log.Error("moov backfill query", "err", err)
		return
	}
	type item struct {
		id  int64
		rel string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.rel); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()
	if len(items) == 0 {
		return
	}

	front, atEnd, skipped := 0, 0, 0
	for _, it := range items {
		if ctx.Err() != nil {
			return
		}
		src := filepath.Join(videoRoot, filepath.FromSlash(it.rel))
		// Unreachable file (NAS flap) must stay unknown, not be pinned to
		// MoovFront forever.
		if _, err := os.Stat(src); err != nil {
			skipped++
			continue
		}
		v := DetectFile(src)
		if _, err := db.ExecContext(ctx,
			`UPDATE clips SET moov_at_end = ? WHERE id = ?`, v, it.id); err != nil {
			log.Warn("moov backfill update", "clip_id", it.id, "err", err)
			continue
		}
		if v == MoovAtEnd {
			atEnd++
		} else {
			front++
		}
	}
	log.Info("moov backfill done",
		"total", len(items), "faststart_ok", front, "moov_at_end", atEnd, "skipped", skipped)
}
