package api

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gammichdb "github.com/klNuno/gammich/internal/db"
)

// PCM windows feed the client-side Web Audio player. Firefox cannot decode
// pcm_* audio inside MP4/MKV, so for those clips the <video> element plays
// muted and the page pulls the selected track here as raw samples, demuxed by
// ffmpeg without touching the source.
const (
	pcmMaxWindow  = 60 * time.Second
	pcmConcurrent = 2 // each ffmpeg holds ~160 MB of MP4 index on a 5-track OBS clip, a small box has 1 GB
	pcmTimeout    = 2 * time.Minute
	pcmChannels   = 2 // stereo out whatever the source layout: the client mixes 2 channels only
)

var pcmSem = make(chan struct{}, pcmConcurrent)

// pcmAudioCodec reports whether a codec is one Firefox refuses in <video>.
func pcmAudioCodec(codec string) bool {
	return strings.HasPrefix(codec, "pcm_")
}

// streamPCM serves GET /api/clips/{id}/pcm?track=N&from=S&dur=D as
// interleaved little-endian int16 stereo at the track's native rate.
// Headers X-Audio-Rate, X-Audio-Channels and X-Audio-From describe the body.
func (s *Server) streamPCM(w http.ResponseWriter, r *http.Request) {
	c, ok := s.lookupClipForFile(w, r)
	if !ok {
		return
	}
	if c.Kind == "screenshot" {
		http.Error(w, "no audio", 404)
		return
	}
	q := r.URL.Query()
	track, err := strconv.Atoi(q.Get("track"))
	if q.Get("track") == "" {
		track, err = 0, nil
	}
	if err != nil || track < 0 || (c.AudioTrackCount > 0 && int64(track) >= c.AudioTrackCount) {
		http.Error(w, "bad track", 400)
		return
	}
	from, err := strconv.ParseFloat(q.Get("from"), 64)
	if q.Get("from") == "" {
		from, err = 0, nil
	}
	if err != nil || from < 0 {
		http.Error(w, "bad from", 400)
		return
	}
	dur, err := strconv.ParseFloat(q.Get("dur"), 64)
	if q.Get("dur") == "" {
		dur, err = 20, nil
	}
	if err != nil || dur <= 0 {
		http.Error(w, "bad dur", 400)
		return
	}
	if max := pcmMaxWindow.Seconds(); dur > max {
		dur = max
	}
	rate := c.AudioRate
	if rate <= 0 {
		rate = 48000
	}

	src := filepath.Join(s.VideoRoot, filepath.FromSlash(c.RelPath))
	if _, err := os.Stat(src); err != nil {
		http.Error(w, "missing on disk", 410)
		return
	}

	select {
	case pcmSem <- struct{}{}:
		defer func() { <-pcmSem }()
	case <-r.Context().Done():
		return
	case <-time.After(10 * time.Second):
		w.Header().Set("Retry-After", "1")
		http.Error(w, "busy", 503)
		return
	}

	// The request context kills ffmpeg when the client drops the window
	// (seek, track switch, page closed).
	ctx, cancel := context.WithTimeout(r.Context(), pcmTimeout)
	defer cancel()
	ffmpeg := s.FFmpegPath
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-nostdin", "-v", "error",
		"-ss", strconv.FormatFloat(from, 'f', 3, 64),
		"-i", src,
		"-t", strconv.FormatFloat(dur, 'f', 3, 64),
		"-map", "0:a:"+strconv.Itoa(track),
		"-vn", "-sn", "-dn",
		"-ac", strconv.Itoa(pcmChannels),
		"-ar", strconv.FormatInt(rate, 10),
		"-c:a", "pcm_s16le", "-f", "s16le",
		"pipe:1")
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, n: 2048}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	if err := cmd.Start(); err != nil {
		s.Log.Error("pcm start", "clip_id", c.ID, "err", err)
		http.Error(w, "internal error", 500)
		return
	}

	// Hold the headers until ffmpeg produced something, so a failed demux
	// (bad track, unreadable file) is a real HTTP error, not an empty 200.
	br := bufio.NewReaderSize(stdout, 64<<10)
	if _, err := br.Peek(1); err != nil && !errors.Is(err, io.EOF) {
		_ = cmd.Wait()
		s.Log.Warn("pcm read", "clip_id", c.ID, "err", err)
		http.Error(w, "internal error", 500)
		return
	} else if errors.Is(err, io.EOF) {
		werr := cmd.Wait()
		if werr != nil {
			s.Log.Warn("pcm demux", "clip_id", c.ID, "track", track, "err", werr, "stderr", stderr.String())
			http.Error(w, "demux failed", 422)
			return
		}
		// Past the end of the track: an empty window is a valid answer.
	}

	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Audio-Rate", strconv.FormatInt(rate, 10))
	h.Set("X-Audio-Channels", strconv.Itoa(pcmChannels))
	h.Set("X-Audio-Format", "s16le")
	h.Set("X-Audio-From", strconv.FormatFloat(from, 'f', 3, 64))
	h.Set("Access-Control-Expose-Headers", "X-Audio-Rate, X-Audio-Channels, X-Audio-Format, X-Audio-From")
	w.WriteHeader(200)
	if _, err := io.Copy(w, br); err != nil {
		cancel() // client gone: stop ffmpeg now
	}
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		s.Log.Warn("pcm demux", "clip_id", c.ID, "track", track, "err", err, "stderr", stderr.String())
	}
}

// limitedWriter keeps the first n bytes and drops the rest, so a chatty
// ffmpeg cannot grow the stderr buffer without bound.
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	total := len(p)
	if l.n > 0 {
		if len(p) > l.n {
			p = p[:l.n]
		}
		k, _ := l.w.Write(p)
		l.n -= k
	}
	return total, nil
}

// sourceTags are the recorder tags the scanner attaches, which tell which
// track layout a clip follows.
var sourceTags = []string{"obs", "shadowplay", "allstar"}

// guessTrackNames labels the tracks of a multi-track clip whose file names
// none of them: OBS writes track names into MKV but not MP4, ShadowPlay never
// does. It borrows the most common layout among named clips of the same
// recorder, else ShadowPlay's fixed "separate both tracks" layout (game, then
// microphone). Names found this way are flagged as guessed.
func (s *Server) guessTrackNames(ctx context.Context, dto *clipDTO) {
	if dto.AudioTracks < 2 {
		return
	}
	for _, n := range dto.AudioTrackNames {
		if n != "" {
			return
		}
	}
	source := ""
	for _, t := range dto.Tags {
		for _, st := range sourceTags {
			if t.Slug == st {
				source = st
			}
		}
	}
	if source == "" {
		return
	}
	names, err := gammichdb.CommonTrackTitles(ctx, s.DB, dto.AudioTracks, source)
	if err != nil {
		s.Log.Warn("guess track names", "clip_id", dto.ID, "err", err)
		return
	}
	if names == nil && source == "shadowplay" && dto.AudioTracks == 2 {
		names = []string{"Game", "Microphone"}
	}
	if names != nil {
		dto.AudioTrackNames, dto.AudioTrackNamesGuessed = names, true
	}
}
