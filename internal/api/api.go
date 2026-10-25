package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	gammichdb "github.com/klNuno/gammich/internal/db"
	"github.com/klNuno/gammich/internal/faststart"
	"github.com/klNuno/gammich/internal/preview"
	"github.com/klNuno/gammich/internal/scan"
)

type Server struct {
	DB          *sql.DB
	VideoRoot   string
	CacheRoot   string
	StaticFS    fs.FS
	Log         *slog.Logger
	OnScan      func(context.Context) (scan.Result, error)
	IngestToken string
	// ProxySecret is injected by the reverse proxy as X-Gammich-Proxy-Secret;
	// it gates mutating routes and the trust in Authelia Remote-* headers.
	ProxySecret string
	// Password enables the built-in login: reads and edits need a session.
	Password string
	// TrustLoopback lets loopback peers edit without credentials (local dev).
	// Off by default: a reverse proxy on the same host is a loopback peer.
	TrustLoopback bool
	// Version is reported by /api/session.
	Version string
	// TrustedProxies are reverse proxies whose X-Forwarded-For is believed
	// when rate-limiting logins.
	TrustedProxies []*net.IPNet
	logins         loginLimiter
	// RootCtx parents background jobs spawned by handlers so they die with
	// the server instead of leaking past shutdown.
	RootCtx context.Context
	// Plans caches the in-memory faststart layouts of moov-at-end MP4s
	// (Firefox loses the audio track on the originals). Nil gets a default.
	Plans     *faststart.PlanCache
	plansOnce sync.Once
	// FFmpegPath demuxes PCM audio windows for the Web Audio player.
	FFmpegPath string

	statsMu      sync.Mutex
	statsAt      time.Time
	statsBytes   int64
	statsSprites int64
	statsThumbs  int64

	refreshImagesMu sync.Mutex
	backfillMu      sync.Mutex
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(noSniff)

	r.Route("/api", func(r chi.Router) {
		r.Get("/healthz", s.healthz)
		r.Get("/session", s.session)
		r.Post("/login", s.login)
		r.Post("/logout", s.logout)

		// Ingest keeps its own token check.
		r.Post("/ingest/preview", s.ingestPreview)
		r.Get("/ingest/pending", s.ingestPending)

		r.Group(func(r chi.Router) {
			r.Use(s.requireRead)
			r.Get("/clips", s.listClips)
			r.Get("/clips/{id}", s.getClip)
			r.Get("/clips/{id}/stream", s.streamClip)
			r.Get("/clips/{id}/download", s.downloadClip)
			r.Get("/clips/{id}/pcm", s.streamPCM)
			r.Get("/clips/{id}/thumb.jpg", s.serveThumb)
			r.Get("/clips/{id}/sprite.jpg", s.serveSprite)
			r.Get("/categories", s.listCategories)
			r.Get("/tags", s.listTags)
			r.Get("/stats", s.getStats)
			r.Get("/search", s.unifiedSearch)
		})

		r.Group(func(r chi.Router) {
			r.Use(s.requireMutateAuth)
			r.Patch("/clips/{id}", s.patchClip)
			r.Post("/clips/bulk-tag", s.bulkAddTag)
			r.Post("/clips/bulk-category", s.bulkCategory)
			r.Post("/categories", s.createCategory)
			r.Patch("/categories/{id}", s.patchCategory)
			r.Post("/categories/refresh-images", s.refreshCategoryImages)
			r.Post("/categories/clear-images", s.clearCategoryImages)
			r.Post("/tags", s.createTag)
			r.Patch("/tags/{id}", s.patchTag)
			r.Delete("/tags/{id}", s.deleteTag)
			r.Post("/scan", s.triggerScan)
			r.Post("/backfill-tech-tags", s.backfillTechTags)
		})
	})

	if s.StaticFS != nil {
		r.Handle("/*", s.spaHandler())
	}
	return r
}

func noSniff(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rootCtx() context.Context {
	if s.RootCtx != nil {
		return s.RootCtx
	}
	return context.Background()
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	// Deep by default so the external watchdog sees a departed NAS or a wedged
	// DB. ?lite=1 stays cheap for anything that only needs "process is up".
	if r.URL.Query().Get("lite") == "1" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	var one int
	if err := s.DB.QueryRowContext(r.Context(), "SELECT 1").Scan(&one); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error", "check": "db"})
		return
	}
	if _, err := os.Stat(s.VideoRoot); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error", "check": "videos"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listClips(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(q.Get("per_page"))
	if perPage <= 0 || perPage > 200 {
		perPage = 60
	}
	catID, _ := strconv.ParseInt(q.Get("category"), 10, 64)

	p := gammichdb.ListParams{
		Query:      q.Get("q"),
		CategoryID: catID,
		Sort:       q.Get("sort"),
		Limit:      perPage,
		Offset:     (page - 1) * perPage,
		Kind:       q.Get("kind"),
	}
	tagIDs := parseInt64List(q.Get("tags"))
	p.TagIDs = tagIDs

	clips, total, err := gammichdb.ListClips(r.Context(), s.DB, p)
	if err != nil {
		s.Log.Error("listClips", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	ids := make([]int64, len(clips))
	for i, c := range clips {
		ids[i] = c.ID
	}
	tagMap, err := gammichdb.TagsForClips(r.Context(), s.DB, ids)
	if err != nil {
		s.Log.Error("tagsForClips", "err", err)
	}
	dtos := clipsToDTO(clips)
	for i := range dtos {
		dtos[i].Tags = tagMap[dtos[i].ID]
	}
	writeJSON(w, 200, map[string]any{
		"clips":    dtos,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

func parseInt64List(s string) []int64 {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseInt(p, 10, 64)
		if err == nil && v > 0 {
			if _, dup := seen[v]; dup {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

func (s *Server) getClip(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	c, err := gammichdb.GetClip(r.Context(), s.DB, id)
	if err != nil {
		s.Log.Error("getClip", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	if c == nil || c.DeletedAt.Valid {
		http.Error(w, "not found", 404)
		return
	}
	tags, err := gammichdb.TagsForClip(r.Context(), s.DB, id)
	if err != nil {
		s.Log.Error("getClipTags", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	dto := clipToDTO(*c)
	dto.Tags = tags
	s.guessTrackNames(r.Context(), &dto)
	writeJSON(w, 200, dto)
}

func (s *Server) patchClip(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	var body struct {
		Name         *string  `json:"name"`
		TagIDs       *[]int64 `json:"tag_ids"`
		AddTagNames  []string `json:"add_tag_names,omitempty"`
		RemoveTagIDs []int64  `json:"remove_tag_ids,omitempty"`
		CategoryID   *int64   `json:"category_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	patch := gammichdb.ClipPatch{
		TagIDs:       body.TagIDs,
		RemoveTagIDs: body.RemoveTagIDs,
		CategoryID:   body.CategoryID,
	}
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" {
			http.Error(w, "name empty", 400)
			return
		}
		if len(name) > 200 {
			http.Error(w, "name too long", 400)
			return
		}
		patch.Name = &name
	}
	for _, name := range body.AddTagNames {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 64 {
			continue
		}
		patch.AddTagNames = append(patch.AddTagNames, name)
	}
	if err := gammichdb.PatchClip(r.Context(), s.DB, id, patch); err != nil {
		if errors.Is(err, gammichdb.ErrInvalidRef) {
			http.Error(w, "unknown tag_id or category_id", 400)
			return
		}
		s.Log.Error("patchClip", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	s.getClip(w, r)
}

func (s *Server) bulkAddTag(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		ClipIDs  []int64  `json:"clip_ids"`
		TagID    int64    `json:"tag_id"`
		TagNames []string `json:"tag_names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if len(body.ClipIDs) == 0 {
		http.Error(w, "missing clip_ids", 400)
		return
	}
	if len(body.ClipIDs) > 1000 {
		http.Error(w, "too many clip_ids (max 1000)", 400)
		return
	}
	var names []string
	for _, n := range body.TagNames {
		n = strings.TrimSpace(n)
		if n == "" || len(n) > 64 {
			continue
		}
		names = append(names, n)
	}
	if body.TagID == 0 && len(names) == 0 {
		http.Error(w, "missing tag_id or tag_names", 400)
		return
	}
	if err := gammichdb.BulkTag(r.Context(), s.DB, body.ClipIDs, body.TagID, names); err != nil {
		if errors.Is(err, gammichdb.ErrInvalidRef) {
			http.Error(w, "unknown tag_id", 400)
			return
		}
		s.Log.Error("bulkAddTag", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "count": len(body.ClipIDs)})
}

func (s *Server) bulkCategory(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		ClipIDs    []int64 `json:"clip_ids"`
		CategoryID int64   `json:"category_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if len(body.ClipIDs) == 0 {
		http.Error(w, "missing clip_ids", 400)
		return
	}
	if len(body.ClipIDs) > 1000 {
		http.Error(w, "too many clip_ids (max 1000)", 400)
		return
	}
	// category_id == 0 clears the category (declassify); >0 assigns it.
	if err := gammichdb.BulkSetCategory(r.Context(), s.DB, body.ClipIDs, body.CategoryID); err != nil {
		if errors.Is(err, gammichdb.ErrInvalidRef) {
			http.Error(w, "unknown category_id", 400)
			return
		}
		s.Log.Error("bulkCategory", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "count": len(body.ClipIDs)})
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	withCounts := q.Get("with_counts") == "1"
	search := q.Get("q")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if withCounts || search != "" {
		tags, err := gammichdb.ListTagsWithCounts(r.Context(), s.DB, search, limit)
		if err != nil {
			s.Log.Error("listTagsCounts", "err", err)
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, tags)
		return
	}
	tags, err := gammichdb.ListTags(r.Context(), s.DB)
	if err != nil {
		s.Log.Error("listTags", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, tags)
}

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	st, err := gammichdb.GetStats(r.Context(), s.DB)
	if err != nil {
		s.Log.Error("getStats", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	cacheBytes, sprites, thumbs := s.cacheStats()
	writeJSON(w, 200, map[string]any{
		"clips_total":      st.ClipsTotal,
		"clips_active":     st.ClipsActive,
		"clips_deleted":    st.ClipsDeleted,
		"bytes_total":      st.BytesTotal,
		"tags_total":       st.TagsTotal,
		"tags_auto":        st.TagsAuto,
		"categories_total": st.CategoriesTotal,
		"preview_done":     st.PreviewDone,
		"preview_pending":  st.PreviewPending,
		"preview_failed":   st.PreviewFailed,
		"cache_bytes":      cacheBytes,
		"cache_sprites":    sprites,
		"cache_thumbs":     thumbs,
	})
}

func (s *Server) cacheStats() (int64, int64, int64) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if time.Since(s.statsAt) < 60*time.Second {
		return s.statsBytes, s.statsSprites, s.statsThumbs
	}
	var total, spr, th int64
	count := func(dir string, n *int64) {
		entries, err := os.ReadDir(filepath.Join(s.CacheRoot, dir))
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			*n++
			total += info.Size()
		}
	}
	count("sprites", &spr)
	count("thumbs", &th)
	s.statsAt = time.Now()
	s.statsBytes = total
	s.statsSprites = spr
	s.statsThumbs = th
	return total, spr, th
}

func (s *Server) createTag(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	t, err := gammichdb.CreateTag(r.Context(), s.DB, body.Name)
	if err != nil {
		s.Log.Warn("createTag", "err", err)
		http.Error(w, "invalid tag name", 400)
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) patchTag(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 100 {
		http.Error(w, "invalid name", 400)
		return
	}
	t, err := gammichdb.RenameTag(r.Context(), s.DB, id, name)
	switch {
	case errors.Is(err, gammichdb.ErrConflict):
		http.Error(w, "tag exists", 409)
		return
	case errors.Is(err, gammichdb.ErrAutoTag):
		http.Error(w, "auto tags cannot be renamed", 400)
		return
	case err != nil:
		s.Log.Error("patchTag", "err", err)
		http.Error(w, "internal error", 500)
		return
	case t == nil:
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) patchCategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Name   *string `json:"name"`
		Hidden *bool   `json:"hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if body.Name != nil {
		n := strings.TrimSpace(*body.Name)
		if n == "" || len(n) > 200 {
			http.Error(w, "invalid name", 400)
			return
		}
		body.Name = &n
	}
	c, err := gammichdb.UpdateCategory(r.Context(), s.DB, id, body.Name, body.Hidden)
	if err != nil {
		s.Log.Error("patchCategory", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	if c == nil {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, 200, categoriesToDTO([]gammichdb.Category{*c})[0])
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	if err := gammichdb.DeleteTag(r.Context(), s.DB, id); err != nil {
		s.Log.Error("deleteTag", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) streamClip(w http.ResponseWriter, r *http.Request) {
	c, ok := s.lookupClipForFile(w, r)
	if !ok {
		return
	}
	path := filepath.Join(s.VideoRoot, filepath.FromSlash(c.RelPath))
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "missing on disk", 410)
			return
		}
		s.Log.Error("open clip", "path", path, "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.Header().Set("Content-Type", mimeForExt(filepath.Ext(path)))
	w.Header().Set("Accept-Ranges", "bytes")

	// Firefox can't find the audio track of moov-at-end MP4s over Range
	// requests: serve them with the moov spliced in front of mdat, rebuilt in
	// memory. downloadClip keeps returning the exact original.
	if c.MoovAtEnd != faststart.MoovFront && c.Kind != "screenshot" {
		key := faststart.PlanKey{ID: c.ID, Size: info.Size(), Mtime: info.ModTime().UnixNano()}
		plan, err := s.plans().Get(key, f)
		if err != nil {
			s.Log.Warn("faststart plan, serving original", "clip_id", c.ID, "err", err)
		} else if plan != nil {
			http.ServeContent(w, r, c.OriginalFilename, info.ModTime(), plan.NewReader(f))
			return
		}
	}
	http.ServeContent(w, r, c.OriginalFilename, info.ModTime(), f)
}

// plans returns the shared faststart plan cache, created on first use.
func (s *Server) plans() *faststart.PlanCache {
	s.plansOnce.Do(func() {
		if s.Plans == nil {
			s.Plans = &faststart.PlanCache{MaxBytes: 32 << 20}
		}
	})
	return s.Plans
}

func (s *Server) downloadClip(w http.ResponseWriter, r *http.Request) {
	c, ok := s.lookupClipForFile(w, r)
	if !ok {
		return
	}
	path := filepath.Join(s.VideoRoot, filepath.FromSlash(c.RelPath))
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "missing on disk", 410)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	cd := mime.FormatMediaType("attachment", map[string]string{"filename": c.OriginalFilename})
	if cd == "" {
		cd = "attachment"
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", cd)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	_, _ = io.Copy(w, f)
}

func (s *Server) serveThumb(w http.ResponseWriter, r *http.Request) {
	s.serveCacheFile(w, r, "thumbs")
}

func (s *Server) serveSprite(w http.ResponseWriter, r *http.Request) {
	s.serveCacheFile(w, r, "sprites")
}

func (s *Server) serveCacheFile(w http.ResponseWriter, r *http.Request, kind string) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	path := filepath.Join(s.CacheRoot, kind, strconv.FormatInt(id, 10)+".jpg")
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := gammichdb.ListCategories(r.Context(), s.DB)
	if err != nil {
		s.Log.Error("listCategories", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, categoriesToDTO(cats))
}

func (s *Server) createCategory(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 200 {
		http.Error(w, "invalid name", 400)
		return
	}
	slug := gammichdb.Slugify(name)
	if slug == "" {
		http.Error(w, "invalid name", 400)
		return
	}
	c, err := gammichdb.CreateCategory(r.Context(), s.DB, slug, name)
	if err != nil {
		s.Log.Error("createCategory", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	if c == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "category exists"})
		return
	}
	writeJSON(w, 200, categoriesToDTO([]gammichdb.Category{*c})[0])
}

func (s *Server) clearCategoryImages(w http.ResponseWriter, r *http.Request) {
	n, err := gammichdb.ClearCategoryImages(r.Context(), s.DB)
	if err != nil {
		s.Log.Error("clearCategoryImages", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]int64{"cleared": n})
}

func (s *Server) unifiedSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 20 {
		limit = 6
	}
	if q == "" {
		writeJSON(w, 200, map[string]any{"tags": []any{}, "categories": []any{}, "clips": []any{}})
		return
	}
	tags, cats, clips, err := gammichdb.SearchAll(r.Context(), s.DB, q, limit)
	if err != nil {
		s.Log.Error("search", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	ids := make([]int64, len(clips))
	for i, c := range clips {
		ids[i] = c.ID
	}
	tagMap, _ := gammichdb.TagsForClips(r.Context(), s.DB, ids)
	clipDTOs := clipsToDTO(clips)
	for i := range clipDTOs {
		clipDTOs[i].Tags = tagMap[clipDTOs[i].ID]
	}
	writeJSON(w, 200, map[string]any{
		"tags":       tags,
		"categories": categoriesToDTO(cats),
		"clips":      clipDTOs,
	})
}

func (s *Server) refreshCategoryImages(w http.ResponseWriter, r *http.Request) {
	if !s.refreshImagesMu.TryLock() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already running"})
		return
	}
	go func() {
		defer s.refreshImagesMu.Unlock()
		s.runRefreshCategoryImages(s.rootCtx())
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) runRefreshCategoryImages(ctx context.Context) {
	// Re-fetch every category, image_url set or not: refresh is also how old
	// landscape (header.jpg) URLs get replaced by verified portraits.
	cats, err := gammichdb.CategoriesForImageRefresh(ctx, s.DB, 500)
	if err != nil {
		s.Log.Error("categoriesForImageRefresh", "err", err)
		return
	}
	s.Log.Info("refresh category images start", "total", len(cats))
	updated, skipped, failed := 0, 0, 0
	client := &http.Client{Timeout: 10 * time.Second}
	for _, c := range cats {
		url, err := steamPortraitForName(ctx, client, c.Name)
		if err != nil {
			failed++
			s.Log.Warn("steam lookup", "name", c.Name, "err", err)
		} else if url == "" {
			skipped++
			_ = gammichdb.SetCategoryImageURL(ctx, s.DB, c.ID, "none")
		} else if err := gammichdb.SetCategoryImageURL(ctx, s.DB, c.ID, url); err != nil {
			failed++
		} else {
			updated++
		}
		time.Sleep(400 * time.Millisecond)
	}
	s.Log.Info("refresh category images done", "updated", updated, "skipped", skipped, "failed", failed)
}

// steamPortraitForName queries Steam's public storesearch for a game name and
// returns a verified portrait (library_600x900) URL on the Steam CDN, or ""
// when nothing qualifies. Storesearch is fuzzy, so only an exact name match
// (after normalization) is accepted: a wrong game's cover is worse than the
// frontend's gradient fallback. The URL is HEAD-checked before being returned
// so an unverified image is never stored.
func steamPortraitForName(ctx context.Context, client *http.Client, name string) (string, error) {
	u := "https://store.steampowered.com/api/storesearch/?term=" +
		urlEscape(name) + "&cc=us&l=en"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "gammich/1.0 (homelab)")
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", errors.New("steam status " + strconv.Itoa(res.StatusCode))
	}
	var body struct {
		Items []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	want := normalizeGameName(name)
	if want == "" {
		return "", nil
	}
	for _, it := range body.Items {
		if normalizeGameName(it.Name) != want {
			continue
		}
		base := "https://cdn.cloudflare.steamstatic.com/steam/apps/" +
			strconv.FormatInt(it.ID, 10) + "/"
		for _, file := range []string{"library_600x900_2x.jpg", "library_600x900.jpg"} {
			ok, err := urlExists(ctx, client, base+file)
			if err != nil {
				return "", err
			}
			if ok {
				return base + file, nil
			}
		}
		// Exact match without a portrait asset: try the next exact match
		// (duplicate/delisted entries) rather than falling back to landscape.
	}
	return "", nil
}

// normalizeGameName lowercases and strips everything non-alphanumeric so
// "Counter-Strike 2" and "counter strike 2" compare equal.
func normalizeGameName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func urlExists(ctx context.Context, client *http.Client, url string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "gammich/1.0 (homelab)")
	res, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	return res.StatusCode == http.StatusOK, nil
}

func urlEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			out = append(out, c)
		default:
			out = append(out, '%')
			const hex = "0123456789ABCDEF"
			out = append(out, hex[c>>4], hex[c&0x0F])
		}
	}
	return string(out)
}

type categoryDTO struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Hidden    bool   `json:"hidden"`
	ImageURL  string `json:"image_url,omitempty"`
	ClipCount int64  `json:"clip_count"`
	// LatestMtime and CoverClipIDs come from the full listing only.
	LatestMtime  int64   `json:"latest_mtime,omitempty"`
	CoverClipIDs []int64 `json:"cover_clip_ids,omitempty"`
}

func categoriesToDTO(in []gammichdb.Category) []categoryDTO {
	out := make([]categoryDTO, len(in))
	for i, c := range in {
		dto := categoryDTO{
			ID:           c.ID,
			Slug:         c.Slug,
			Name:         c.Name,
			Hidden:       c.Hidden,
			ClipCount:    c.ClipCount,
			LatestMtime:  c.LatestMtime,
			CoverClipIDs: c.CoverClipIDs,
		}
		if c.ImageURL.Valid && c.ImageURL.String != "none" {
			dto.ImageURL = c.ImageURL.String
		}
		out[i] = dto
	}
	return out
}

func (s *Server) ingestPending(w http.ResponseWriter, r *http.Request) {
	if !s.checkIngestAuth(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT id, rel_path, size_bytes, mtime, preview_status
		FROM clips
		WHERE deleted_at IS NULL AND preview_status IN ('pending', 'failed')
		ORDER BY mtime DESC
		LIMIT ?`, limit)
	if err != nil {
		s.Log.Error("ingestPending", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	defer rows.Close()
	type pending struct {
		ID            int64  `json:"id"`
		RelPath       string `json:"rel_path"`
		SizeBytes     int64  `json:"size_bytes"`
		Mtime         int64  `json:"mtime"`
		PreviewStatus string `json:"preview_status"`
	}
	out := make([]pending, 0)
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.ID, &p.RelPath, &p.SizeBytes, &p.Mtime, &p.PreviewStatus); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		out = append(out, p)
	}
	writeJSON(w, 200, out)
}

const maxIngestUploadBytes = 20 << 20

func (s *Server) ingestPreview(w http.ResponseWriter, r *http.Request) {
	if !s.checkIngestAuth(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxIngestUploadBytes)
	if err := r.ParseMultipartForm(maxIngestUploadBytes); err != nil {
		http.Error(w, "bad multipart", 400)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("clip_id"), 10, 64)
	if err != nil {
		http.Error(w, "bad clip_id", 400)
		return
	}
	durationS, _ := strconv.ParseFloat(r.FormValue("duration_s"), 64)
	width, _ := strconv.ParseInt(r.FormValue("width"), 10, 64)
	height, _ := strconv.ParseInt(r.FormValue("height"), 10, 64)
	codec := r.FormValue("codec")
	audioTracks := int64(-1) // -1 = not provided, keep existing value
	if v := r.FormValue("audio_track_count"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			audioTracks = n
		}
	}

	c, err := gammichdb.GetClip(r.Context(), s.DB, id)
	if err != nil {
		s.Log.Error("ingest getClip", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	if c == nil || c.DeletedAt.Valid {
		http.Error(w, "unknown clip", 404)
		return
	}

	// Stage (write + validate) both JPEGs before committing either, so a bad
	// thumb never leaves a committed sprite orphaned (sprite/thumb desync).
	spriteDst := filepath.Join(s.CacheRoot, "sprites", strconv.FormatInt(id, 10)+".jpg")
	thumbDst := filepath.Join(s.CacheRoot, "thumbs", strconv.FormatInt(id, 10)+".jpg")
	spriteTmp, err := s.stageUpload(r, "sprite", spriteDst)
	if err != nil {
		s.uploadError(w, "sprite", err)
		return
	}
	thumbTmp, err := s.stageUpload(r, "thumb", thumbDst)
	if err != nil {
		_ = os.Remove(spriteTmp)
		s.uploadError(w, "thumb", err)
		return
	}
	if err := os.Rename(spriteTmp, spriteDst); err != nil {
		_ = os.Remove(spriteTmp)
		_ = os.Remove(thumbTmp)
		s.uploadError(w, "sprite", err)
		return
	}
	if err := os.Rename(thumbTmp, thumbDst); err != nil {
		_ = os.Remove(thumbTmp)
		s.uploadError(w, "thumb", err)
		return
	}

	// Ingested clips skip the local preview worker, so probe moov placement
	// here; otherwise they'd wait for the next boot backfill. Unreachable
	// source (NAS flap) stays unknown rather than getting pinned to 0.
	moov := int64(faststart.MoovUnknown)
	if c.Kind != "screenshot" {
		src := filepath.Join(s.VideoRoot, filepath.FromSlash(c.RelPath))
		if _, err := os.Stat(src); err == nil {
			moov = int64(faststart.DetectFile(src))
		}
	}

	now := time.Now().Unix()
	if _, err := s.DB.ExecContext(r.Context(), `
		UPDATE clips SET
			preview_status='done', preview_error=NULL,
			duration_s=COALESCE(NULLIF(?, 0), duration_s),
			width=COALESCE(NULLIF(?, 0), width),
			height=COALESCE(NULLIF(?, 0), height),
			codec=COALESCE(NULLIF(?, ''), codec),
			audio_track_count=COALESCE(NULLIF(?, -1), audio_track_count),
			moov_at_end=COALESCE(NULLIF(?, -1), moov_at_end),
			updated_at=?
		WHERE id=?`,
		durationS, width, height, codec, audioTracks, moov, now, id); err != nil {
		s.Log.Error("ingest update", "err", err)
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "clip_id": id})
}

var errNotJPEG = errors.New("upload is not a JPEG")

func (s *Server) uploadError(w http.ResponseWriter, field string, err error) {
	if errors.Is(err, errNotJPEG) || errors.Is(err, http.ErrMissingFile) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		http.Error(w, field+" must be a JPEG file", 400)
		return
	}
	s.Log.Error("save upload", "field", field, "err", err)
	http.Error(w, "internal error", 500)
}

// stageUpload writes the uploaded JPEG to dst+".tmp" and validates the magic
// bytes, returning the tmp path. The caller commits it with a rename once every
// staged upload has validated. On any error the tmp file is cleaned up.
func (s *Server) stageUpload(r *http.Request, field, dst string) (string, error) {
	file, _, err := r.FormFile(field)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var magic [3]byte
	if _, err := io.ReadFull(file, magic[:]); err != nil {
		return "", err
	}
	if magic[0] != 0xFF || magic[1] != 0xD8 || magic[2] != 0xFF {
		return "", errNotJPEG
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(magic[:]); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if _, err := io.Copy(f, file); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

func (s *Server) backfillTechTags(w http.ResponseWriter, r *http.Request) {
	// The full pass can take minutes on large libraries: 202 immediately and
	// let the background worker log progress.
	if !s.backfillMu.TryLock() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already running"})
		return
	}
	go func() {
		defer s.backfillMu.Unlock()
		s.runBackfillTechTags(s.rootCtx())
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) runBackfillTechTags(ctx context.Context) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, COALESCE(width, 0), COALESCE(height, 0),
		       COALESCE(fps_num, 0), COALESCE(fps_den, 1),
		       COALESCE(codec, '')
		FROM clips
		WHERE deleted_at IS NULL AND preview_status = 'done'
		  AND (width IS NOT NULL OR codec IS NOT NULL)`)
	if err != nil {
		s.Log.Error("backfill query", "err", err)
		return
	}
	type job struct {
		id, w, h, fn, fd int64
		codec            string
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.w, &j.h, &j.fn, &j.fd, &j.codec); err == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	s.Log.Info("backfill tech tags start", "total", len(jobs))
	processed, attached := 0, 0
	for _, j := range jobs {
		names := preview.TechAutoTags(j.w, j.h, j.fn, j.fd, j.codec)
		processed++
		if len(names) == 0 {
			continue
		}
		if err := gammichdb.AttachAutoTags(ctx, s.DB, j.id, names); err != nil {
			s.Log.Warn("backfill attach", "clip_id", j.id, "err", err)
			continue
		}
		attached += len(names)
	}
	s.Log.Info("backfill tech tags done", "processed", processed, "tags_attached", attached)
}

func (s *Server) triggerScan(w http.ResponseWriter, r *http.Request) {
	if s.OnScan == nil {
		http.Error(w, "scan not configured", 503)
		return
	}
	res, err := s.OnScan(r.Context())
	if err != nil {
		if errors.Is(err, scan.ErrAlreadyRunning) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "scan already running"})
			return
		}
		s.Log.Error("scan", "err", err)
		http.Error(w, "scan failed", 500)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) lookupClipForFile(w http.ResponseWriter, r *http.Request) (*gammichdb.Clip, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return nil, false
	}
	c, err := gammichdb.GetClip(r.Context(), s.DB, id)
	if err != nil {
		s.Log.Error("lookupClip", "err", err)
		http.Error(w, "internal error", 500)
		return nil, false
	}
	if c == nil || c.DeletedAt.Valid {
		http.Error(w, "not found", 404)
		return nil, false
	}
	return c, true
}

func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.StaticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		f, err := s.StaticFS.Open(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		_ = f.Close()
		fileServer.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type clipDTO struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	OriginalFilename string  `json:"original_filename"`
	RelPath          string  `json:"rel_path"`
	SizeBytes        int64   `json:"size_bytes"`
	Mtime            int64   `json:"mtime"`
	DurationS        float64 `json:"duration_s,omitempty"`
	Width            int64   `json:"width,omitempty"`
	Height           int64   `json:"height,omitempty"`
	Codec            string  `json:"codec,omitempty"`
	CategoryID       int64   `json:"category_id,omitempty"`
	CategoryName     string  `json:"category_name,omitempty"`
	Kind             string  `json:"kind"`
	PreviewStatus    string  `json:"preview_status"`
	AudioTracks      int64   `json:"audio_tracks"`
	AudioCodec       string  `json:"audio_codec,omitempty"`
	AudioRate        int64   `json:"audio_rate,omitempty"`
	AudioChannels    int64   `json:"audio_channels,omitempty"`
	// AudioPCM: the audio is pcm_*, which Firefox's <video> cannot decode;
	// the client plays it through /pcm and Web Audio instead.
	AudioPCM bool `json:"audio_pcm,omitempty"`
	// AudioTrackNames has one entry per track, "" for an unnamed one. When
	// AudioTrackNamesGuessed is set they come from other clips of the same
	// recorder, not from this file (see guessTrackNames).
	AudioTrackNames        []string        `json:"audio_track_names,omitempty"`
	AudioTrackNamesGuessed bool            `json:"audio_track_names_guessed,omitempty"`
	Tags                   []gammichdb.Tag `json:"tags,omitempty"`
}

func clipToDTO(c gammichdb.Clip) clipDTO {
	kind := c.Kind
	if kind == "" {
		kind = "video"
	}
	dto := clipDTO{
		ID:               c.ID,
		Name:             c.Name,
		OriginalFilename: c.OriginalFilename,
		RelPath:          c.RelPath,
		SizeBytes:        c.SizeBytes,
		Mtime:            c.Mtime,
		Kind:             kind,
		PreviewStatus:    c.PreviewStatus,
		AudioTracks:      c.AudioTrackCount,
		AudioRate:        c.AudioRate,
		AudioChannels:    c.AudioChannels,
	}
	if c.AudioCodec.Valid {
		dto.AudioCodec = c.AudioCodec.String
		dto.AudioPCM = pcmAudioCodec(c.AudioCodec.String)
	}
	if c.AudioTrackTitles.Valid && c.AudioTrackCount > 1 {
		if names := gammichdb.ParseTrackTitles(c.AudioTrackTitles.String); int64(len(names)) == c.AudioTrackCount {
			dto.AudioTrackNames = names
		}
	}
	if c.DurationS.Valid {
		dto.DurationS = c.DurationS.Float64
	}
	if c.Width.Valid {
		dto.Width = c.Width.Int64
	}
	if c.Height.Valid {
		dto.Height = c.Height.Int64
	}
	if c.Codec.Valid {
		dto.Codec = c.Codec.String
	}
	if c.CategoryID.Valid {
		dto.CategoryID = c.CategoryID.Int64
	}
	if c.CategoryName.Valid {
		dto.CategoryName = c.CategoryName.String
	}
	return dto
}

func clipsToDTO(in []gammichdb.Clip) []clipDTO {
	out := make([]clipDTO, len(in))
	for i, c := range in {
		out[i] = clipToDTO(c)
	}
	return out
}

func mimeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	default:
		return "application/octet-stream"
	}
}
