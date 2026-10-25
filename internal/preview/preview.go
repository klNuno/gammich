// Package preview runs FFmpeg to extract a sprite contact sheet (10 frames)
// and a single mid-clip thumbnail for each clip indexed in the database.
//
// Sprite layout: 10 frames horizontally tiled, 320x180 each → 3200x180 JPEG.
// Thumbnail: 1 frame at 50% duration, 640x360 JPEG.
//
// The worker pulls clips with preview_status='pending' or 'failed', invokes
// ffmpeg twice (sprite + thumb), and updates the DB on completion. Designed
// to run in-process alongside the API; on a small CPU-only server it generates a few
// previews per minute. For large backlogs, run the external Windows script
// (scripts/gen-previews.ps1) using NVDEC on a GPU host and POST results via
// the ingest endpoint.
package preview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	gammichdb "github.com/klNuno/gammich/internal/db"
	"github.com/klNuno/gammich/internal/faststart"
)

// errSourceUnavailable marks a clip whose source file could not be stat'd (NAS
// flap). runWorker leaves such clips pending instead of burning a retry.
var errSourceUnavailable = errors.New("source unavailable")

// jobTimeout bounds one preview job (probe + sprite + thumb). Generous for a
// slow CPU-bound remux; a job past it means a wedged mount or ffmpeg hang.
const jobTimeout = 10 * time.Minute

type Worker struct {
	DB         *sql.DB
	VideoRoot  string
	CacheRoot  string
	FFmpegPath string
	HWAccel    string // "" (cpu/libdav1d), "cuda", "vaapi"
	Workers    int
	Log        *slog.Logger

	inflightMu sync.Mutex
	inflight   map[int64]struct{}
}

const (
	spriteFrames = 10
	spriteWidth  = 320
	spriteHeight = 180
	thumbWidth   = 640
	thumbHeight  = 360
)

// Run continuously polls for pending previews. Blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	if w.Workers <= 0 {
		w.Workers = 1
	}
	if w.FFmpegPath == "" {
		w.FFmpegPath = "ffmpeg"
	}
	if w.Log == nil {
		w.Log = slog.Default()
	}
	w.Log.Info("preview worker start", "workers", w.Workers, "hwaccel", w.HWAccel)

	jobs := make(chan int64, w.Workers*2)
	for i := 0; i < w.Workers; i++ {
		go w.runWorker(ctx, jobs)
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	w.enqueuePending(ctx, jobs)
	for {
		select {
		case <-ctx.Done():
			close(jobs)
			return
		case <-ticker.C:
			w.enqueuePending(ctx, jobs)
		}
	}
}

func (w *Worker) enqueuePending(ctx context.Context, jobs chan<- int64) {
	// Failed clips get up to 5 retries; attempts ASC keeps old failures from
	// starving behind a stream of fresh pendings.
	rows, err := w.DB.QueryContext(ctx, `
		SELECT id FROM clips
		WHERE deleted_at IS NULL
		  AND (preview_status = 'pending'
		       OR (preview_status = 'failed' AND preview_attempts < 5))
		ORDER BY preview_attempts ASC, mtime DESC
		LIMIT 50`)
	if err != nil {
		w.Log.Error("enqueue query", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		if !w.markInflight(id) {
			continue // already queued/processing (Workers >= 2)
		}
		select {
		case <-ctx.Done():
			w.unmarkInflight(id)
			return
		case jobs <- id:
		default:
			// queue full, retry next tick
			w.unmarkInflight(id)
			return
		}
	}
}

func (w *Worker) markInflight(id int64) bool {
	w.inflightMu.Lock()
	defer w.inflightMu.Unlock()
	if w.inflight == nil {
		w.inflight = make(map[int64]struct{})
	}
	if _, ok := w.inflight[id]; ok {
		return false
	}
	w.inflight[id] = struct{}{}
	return true
}

func (w *Worker) unmarkInflight(id int64) {
	w.inflightMu.Lock()
	delete(w.inflight, id)
	w.inflightMu.Unlock()
}

func (w *Worker) runWorker(ctx context.Context, jobs <-chan int64) {
	for id := range jobs {
		if ctx.Err() != nil {
			w.unmarkInflight(id)
			return
		}
		err := w.processOne(ctx, id)
		w.unmarkInflight(id)
		if err == nil {
			continue
		}
		if errors.Is(err, errSourceUnavailable) {
			// NAS flap: leave pending, don't spend a retry on a missing mount.
			w.Log.Warn("preview source unavailable, leaving pending", "clip_id", id, "err", err)
			continue
		}
		w.Log.Warn("preview failed", "clip_id", id, "err", err)
		_, _ = w.DB.ExecContext(ctx,
			`UPDATE clips SET preview_status='failed', preview_error=?,
				preview_attempts=preview_attempts+1, updated_at=? WHERE id=?`,
			truncate(err.Error(), 500), time.Now().Unix(), id)
	}
}

func (w *Worker) processOne(ctx context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()

	var relPath string
	var status string
	var kind string
	err := w.DB.QueryRowContext(ctx,
		`SELECT rel_path, preview_status, kind FROM clips WHERE id = ?`, id,
	).Scan(&relPath, &status, &kind)
	if err != nil {
		return err
	}
	if status == "done" {
		return nil
	}

	src := filepath.Join(w.VideoRoot, filepath.FromSlash(relPath))
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("%w: %v", errSourceUnavailable, err)
	}

	if kind == "screenshot" {
		return w.processScreenshot(ctx, id, src)
	}

	probe, err := w.probe(ctx, src)
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	moov := faststart.DetectFile(src)

	spritePath := filepath.Join(w.CacheRoot, "sprites", strconv.FormatInt(id, 10)+".jpg")
	thumbPath := filepath.Join(w.CacheRoot, "thumbs", strconv.FormatInt(id, 10)+".jpg")
	if err := os.MkdirAll(filepath.Dir(spritePath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(thumbPath), 0o755); err != nil {
		return err
	}

	// Always regenerate: status is pending/failed here, so any existing file
	// may belong to a stale version of the clip. ffmpeg overwrites in place
	// (-y), which is fine for cache JPEGs.
	if err := w.genSprite(ctx, src, spritePath, probe.DurationS); err != nil {
		return fmt.Errorf("sprite: %w", err)
	}
	if err := w.genThumb(ctx, src, thumbPath, probe.DurationS); err != nil {
		return fmt.Errorf("thumb: %w", err)
	}

	now := time.Now().Unix()
	if _, err := w.DB.ExecContext(ctx, `
		UPDATE clips SET
			preview_status='done', preview_error=NULL,
			duration_s=?, width=?, height=?, codec=?,
			fps_num=?, fps_den=?, audio_track_count=?, moov_at_end=?,
			audio_codec=?, audio_rate=?, audio_channels=?, audio_track_titles=?,
			updated_at=?
		WHERE id=?`,
		probe.DurationS, probe.Width, probe.Height, probe.Codec,
		probe.FPSNum, probe.FPSDen, probe.Audio.Tracks, moov,
		audioCodecArg(probe), probe.Audio.Rate, probe.Audio.Channels, audioTitlesArg(probe),
		now, id); err != nil {
		return err
	}

	if err := gammichdb.AttachAutoTags(ctx, w.DB, id,
		TechAutoTags(probe.Width, probe.Height, probe.FPSNum, probe.FPSDen, probe.Codec)); err != nil {
		w.Log.Warn("attach tech tags", "clip_id", id, "err", err)
	}
	return nil
}

// processScreenshot generates a thumbnail for an image clip (no sprite, no
// duration). Dimensions are probed best-effort; ffprobe handles PNG/JPEG fine.
func (w *Worker) processScreenshot(ctx context.Context, id int64, src string) error {
	thumbPath := filepath.Join(w.CacheRoot, "thumbs", strconv.FormatInt(id, 10)+".jpg")
	if err := os.MkdirAll(filepath.Dir(thumbPath), 0o755); err != nil {
		return err
	}
	args := []string{"-y", "-v", "error",
		"-i", src,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", thumbWidth, thumbHeight),
		"-q:v", "3",
		thumbPath}
	if err := runCmd(ctx, w.FFmpegPath, args); err != nil {
		return fmt.Errorf("screenshot thumb: %w", err)
	}

	var width, height int64
	if probe, err := w.probe(ctx, src); err == nil {
		width, height = probe.Width, probe.Height
	}

	now := time.Now().Unix()
	_, err := w.DB.ExecContext(ctx, `
		UPDATE clips SET
			preview_status='done', preview_error=NULL,
			width=?, height=?, updated_at=?
		WHERE id=?`,
		width, height, now, id)
	return err
}

// CleanCache removes cached sprite/thumb JPEGs whose clip no longer exists or
// has been soft-deleted for more than 7 days. Reads the cache dirs before the
// DB snapshot so files created concurrently are never considered.
func CleanCache(ctx context.Context, db *sql.DB, cacheRoot string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	type candidate struct {
		path string
		id   int64
	}
	var candidates []candidate
	for _, dir := range []string{"sprites", "thumbs"} {
		entries, err := os.ReadDir(filepath.Join(cacheRoot, dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jpg") {
				continue
			}
			id, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".jpg"), 10, 64)
			if err != nil {
				continue
			}
			candidates = append(candidates, candidate{filepath.Join(cacheRoot, dir, e.Name()), id})
		}
	}
	if len(candidates) == 0 {
		return
	}

	cutoff := time.Now().Add(-7 * 24 * time.Hour).Unix()
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM clips WHERE deleted_at IS NULL OR deleted_at > ?`, cutoff)
	if err != nil {
		log.Error("cache gc query", "err", err)
		return
	}
	defer rows.Close()
	keep := make(map[int64]struct{}, 4096)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			log.Error("cache gc scan", "err", err)
			return
		}
		keep[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		log.Error("cache gc rows", "err", err)
		return
	}

	removed := 0
	for _, c := range candidates {
		if _, ok := keep[c.id]; ok {
			continue
		}
		if err := os.Remove(c.path); err == nil {
			removed++
		}
	}
	if removed > 0 {
		log.Info("cache gc done", "removed", removed)
	}
}

type probeResult struct {
	DurationS      float64
	Width, Height  int64
	Codec          string
	FPSNum, FPSDen int64
	Audio          AudioInfo
	AudioOK        bool // false: audio probe failed, columns stay NULL for the backfill
}

// TechAutoTags returns the auto-tag names to attach based on probed metadata.
// Resolution buckets to common labels (1080p, 1440p, 4K) with `WxH` fallback.
// FPS snaps to the closest common cadence within 1 frame.
func TechAutoTags(width, height, fpsNum, fpsDen int64, codec string) []string {
	var out []string
	switch {
	case height >= 2160:
		out = append(out, "4K")
	case height >= 1440:
		out = append(out, "1440p")
	case height >= 1080:
		out = append(out, "1080p")
	case height >= 720:
		out = append(out, "720p")
	case height > 0 && width > 0:
		out = append(out, fmt.Sprintf("%dx%d", width, height))
	}
	if fpsNum > 0 && fpsDen > 0 {
		fps := math.Round(float64(fpsNum) / float64(fpsDen))
		if fps > 0 {
			best := int(fps)
			for _, c := range []int{24, 25, 30, 48, 50, 60, 90, 120, 144, 165, 240} {
				if math.Abs(float64(c)-fps) <= 1 {
					best = c
					break
				}
			}
			out = append(out, fmt.Sprintf("%dfps", best))
		}
	}
	switch strings.ToLower(codec) {
	case "h264", "avc":
		out = append(out, "h264")
	case "hevc", "h265":
		out = append(out, "h265")
	case "av1":
		out = append(out, "av1")
	case "vp9":
		out = append(out, "vp9")
	}
	return out
}

func (w *Worker) probe(ctx context.Context, src string) (probeResult, error) {
	ffprobe := ffprobePath(w.FFmpegPath)
	cmd := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height,r_frame_rate,avg_frame_rate:format=duration",
		"-of", "default=nw=1",
		src)
	out, err := cmd.Output()
	if err != nil {
		return probeResult{}, cmdErr(err, out)
	}
	var res probeResult
	res.FPSDen = 1
	for _, line := range splitLines(string(out)) {
		k, v, ok := splitKV(line)
		if !ok {
			continue
		}
		switch k {
		case "codec_name":
			res.Codec = v
		case "width":
			res.Width, _ = strconv.ParseInt(v, 10, 64)
		case "height":
			res.Height, _ = strconv.ParseInt(v, 10, 64)
		case "duration":
			res.DurationS, _ = strconv.ParseFloat(v, 64)
		case "r_frame_rate", "avg_frame_rate":
			if res.FPSNum == 0 {
				num, den := parseFraction(v)
				if num > 0 && den > 0 {
					res.FPSNum, res.FPSDen = num, den
				}
			}
		}
	}

	// Audio streams need their own pass: the main probe selects v:0 only.
	if a, err := ProbeAudio(ctx, ffprobe, src); err == nil {
		res.Audio, res.AudioOK = a, true
	}

	return res, nil
}

// AudioInfo describes a clip's audio: track count plus codec, sample rate and
// channel count of the first track. Codec is "" when the file has no audio.
// Titles holds one name per track, "" when the file does not name it.
type AudioInfo struct {
	Tracks   int64
	Codec    string
	Rate     int64
	Channels int64
	Titles   []string
}

// audioCodecArg is the audio_codec value to store: NULL when the audio probe
// failed, so BackfillAudio retries it.
func audioCodecArg(p probeResult) any {
	if !p.AudioOK {
		return nil
	}
	return p.Audio.Codec
}

// audioTitlesArg is the audio_track_titles value to store, NULL like
// audioCodecArg when the audio probe failed.
func audioTitlesArg(p probeResult) any {
	if !p.AudioOK {
		return nil
	}
	return gammichdb.TrackTitlesJSON(p.Audio.Titles)
}

// trackTitle picks a track's display name from its stream tags: the title
// (MKV, OBS track names), else the MP4 handler name unless it is the muxer's
// placeholder ("SoundHandler" from OBS, "SoundHandle" from ShadowPlay).
func trackTitle(title, handler string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	h := strings.TrimSpace(handler)
	l := strings.ToLower(h)
	if h == "" || strings.Contains(l, "handle") || l == "core media audio" {
		return ""
	}
	return h
}

// ffprobePath derives the ffprobe binary from the configured ffmpeg path.
func ffprobePath(ffmpeg string) string {
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	if !strings.ContainsAny(ffmpeg, `/\`) {
		return "ffprobe"
	}
	if filepath.Base(ffmpeg) == "ffmpeg.exe" {
		return filepath.Join(filepath.Dir(ffmpeg), "ffprobe.exe")
	}
	return filepath.Join(filepath.Dir(ffmpeg), "ffprobe")
}

// ProbeAudio lists the audio streams of src with ffprobe.
func ProbeAudio(ctx context.Context, ffprobe, src string) (AudioInfo, error) {
	cmd := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-select_streams", "a",
		"-show_entries", "stream=index,codec_name,sample_rate,channels:stream_tags=title,handler_name",
		"-of", "default=nw=1",
		src)
	out, err := cmd.Output()
	if err != nil {
		return AudioInfo{}, cmdErr(err, out)
	}
	var a AudioInfo
	var titles, handlers []string
	for _, line := range splitLines(string(out)) {
		k, v, ok := splitKV(line)
		if !ok {
			continue
		}
		if k == "index" {
			a.Tracks++
			titles, handlers = append(titles, ""), append(handlers, "")
			continue
		}
		if a.Tracks == 0 {
			continue
		}
		switch strings.ToLower(k) {
		case "tag:title":
			titles[a.Tracks-1] = v
			continue
		case "tag:handler_name":
			handlers[a.Tracks-1] = v
			continue
		}
		if a.Tracks != 1 {
			continue // only the first track's details are kept
		}
		switch k {
		case "codec_name":
			a.Codec = v
		case "sample_rate":
			a.Rate, _ = strconv.ParseInt(v, 10, 64)
		case "channels":
			a.Channels, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	for i := range titles {
		a.Titles = append(a.Titles, trackTitle(titles[i], handlers[i]))
	}
	return a, nil
}

// BackfillAudio probes the audio of active video clips never probed since the
// audio columns exist (audio_codec IS NULL), which also repairs their
// audio_track_count, and of multi-track clips whose track names were never
// read (audio_track_titles IS NULL, migration 0012). Unreachable files (NAS
// flap) stay NULL for the next run.
func BackfillAudio(ctx context.Context, db *sql.DB, videoRoot, ffmpeg string, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	// Collect first: the pool is capped at one connection.
	rows, err := db.QueryContext(ctx, `
		SELECT id, rel_path FROM clips
		WHERE deleted_at IS NULL AND kind = 'video'
		  AND (audio_codec IS NULL OR (audio_track_titles IS NULL AND audio_track_count > 1))
		ORDER BY mtime DESC`)
	if err != nil {
		log.Error("audio backfill query", "err", err)
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
	log.Info("audio backfill start", "total", len(items))
	ffprobe := ffprobePath(ffmpeg)
	done, skipped := 0, 0
	for _, it := range items {
		if ctx.Err() != nil {
			return
		}
		src := filepath.Join(videoRoot, filepath.FromSlash(it.rel))
		if _, err := os.Stat(src); err != nil {
			skipped++
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, time.Minute)
		a, err := ProbeAudio(pctx, ffprobe, src)
		cancel()
		if err != nil {
			skipped++
			log.Warn("audio backfill probe", "clip_id", it.id, "err", err)
			continue
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE clips SET audio_codec = ?, audio_rate = ?, audio_channels = ?,
				audio_track_count = ?, audio_track_titles = ? WHERE id = ?`,
			a.Codec, a.Rate, a.Channels, a.Tracks, gammichdb.TrackTitlesJSON(a.Titles), it.id); err != nil {
			log.Warn("audio backfill update", "clip_id", it.id, "err", err)
			continue
		}
		done++
	}
	log.Info("audio backfill done", "probed", done, "skipped", skipped)
}

func parseFraction(s string) (int64, int64) {
	if s == "" || s == "0/0" {
		return 0, 0
	}
	parts := strings.SplitN(s, "/", 2)
	num, _ := strconv.ParseInt(parts[0], 10, 64)
	den := int64(1)
	if len(parts) == 2 {
		den, _ = strconv.ParseInt(parts[1], 10, 64)
	}
	return num, den
}

func (w *Worker) genSprite(ctx context.Context, src, dst string, durationS float64) error {
	if durationS <= 0 {
		durationS = 1
	}
	args := []string{"-y", "-v", "error"}
	if w.HWAccel != "" {
		args = append(args, "-hwaccel", w.HWAccel)
	}
	// Step across the clip at evenly spaced times rather than relying on
	// frame counts (N is unreliable on AV1 streams with VFR / open GOP).
	step := durationS / float64(spriteFrames+1)
	if step < 0.01 {
		step = 0.01
	}
	args = append(args,
		"-i", src,
		"-vf", fmt.Sprintf(
			"fps=1/%.4f,"+
				"scale=%d:%d:force_original_aspect_ratio=decrease,"+
				"pad=%d:%d:(ow-iw)/2:(oh-ih)/2,"+
				"tile=%dx1",
			step, spriteWidth, spriteHeight, spriteWidth, spriteHeight, spriteFrames),
		"-frames:v", "1",
		"-q:v", "4",
		dst)
	return runCmd(ctx, w.FFmpegPath, args)
}

func (w *Worker) genThumb(ctx context.Context, src, dst string, durationS float64) error {
	if durationS <= 0 {
		durationS = 1
	}
	seek := durationS * 0.5
	args := []string{"-y", "-v", "error", "-ss", strconv.FormatFloat(seek, 'f', 3, 64)}
	if w.HWAccel != "" {
		args = append(args, "-hwaccel", w.HWAccel)
	}
	args = append(args,
		"-i", src,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", thumbWidth, thumbHeight),
		"-q:v", "3",
		dst)
	return runCmd(ctx, w.FFmpegPath, args)
}

func runCmd(ctx context.Context, bin string, args []string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, truncate(string(out), 400))
	}
	return nil
}

// cmdErr enriches an exec error with the process stderr. cmd.Output() leaves
// stderr in ExitError.Stderr, which the bare error string drops, losing the
// ffprobe "-v error" message that says why it failed.
func cmdErr(err error, stdout []byte) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("%v: %s", err, truncate(string(ee.Stderr), 400))
	}
	if len(stdout) > 0 {
		return fmt.Errorf("%v: %s", err, truncate(string(stdout), 400))
	}
	return err
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, trimCR(s[start:i]))
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, trimCR(s[start:]))
	}
	return out
}

func trimCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}

func splitKV(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
