package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	gammichdb "github.com/klNuno/gammich/internal/db"
	"github.com/klNuno/gammich/internal/faststart"
	"github.com/klNuno/gammich/internal/preview"
)

// TestStreamPCM builds a 3 s clip shaped like the OBS recordings Firefox
// cannot play (MP4, two pcm_f32le tracks) and checks the PCM window endpoint
// end to end with the real ffmpeg.
func TestStreamPCM(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	ctx := context.Background()
	root := t.TempDir()
	src := filepath.Join(root, "obs.mp4")
	// Track 0: 440 Hz tone, track 1: silence.
	out, err := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=160x90:rate=30:duration=3",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=3",
		"-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-map", "0:v", "-map", "1:a", "-map", "2:a", "-t", "3",
		"-c:v", "mpeg4", "-c:a", "pcm_f32le", "-ac", "2", src).CombinedOutput()
	if err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}

	a, err := preview.ProbeAudio(ctx, ffprobe, src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if a.Tracks != 2 || a.Codec != "pcm_f32le" || a.Rate != 48000 || a.Channels != 2 {
		t.Fatalf("probe = %+v, want 2 tracks pcm_f32le 48000 Hz stereo", a)
	}

	db, err := gammichdb.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := gammichdb.Migrate(db); err != nil {
		t.Fatal(err)
	}
	id, _, _, err := gammichdb.UpsertClip(ctx, db, gammichdb.Clip{
		RelPath: "obs.mp4", Name: "obs", OriginalFilename: "obs.mp4", SizeBytes: 1, Mtime: 1, Kind: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE clips SET audio_codec=?, audio_rate=?, audio_channels=?, audio_track_count=? WHERE id=?`,
		a.Codec, a.Rate, a.Channels, a.Tracks, id); err != nil {
		t.Fatal(err)
	}

	s := &Server{DB: db, VideoRoot: root, FFmpegPath: ffmpeg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ts := httptest.NewServer(s.Router())
	defer ts.Close()
	base := ts.URL + "/api/clips/" + strconv.FormatInt(id, 10)

	// The DTO flags the clip for the Web Audio path.
	c, err := gammichdb.GetClip(ctx, db, id)
	if err != nil || c == nil {
		t.Fatalf("GetClip: %v", err)
	}
	if dto := clipToDTO(*c); !dto.AudioPCM || dto.AudioTracks != 2 {
		t.Fatalf("dto audio_pcm=%v audio_tracks=%d, want true 2", dto.AudioPCM, dto.AudioTracks)
	}

	get := func(q string) (*http.Response, []byte) {
		t.Helper()
		resp, err := http.Get(base + "/pcm?" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, body
	}

	resp, body := get("track=0&from=1&dur=1")
	if resp.StatusCode != 200 {
		t.Fatalf("track 0: status %d: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Audio-Rate") != "48000" || resp.Header.Get("X-Audio-Channels") != "2" {
		t.Fatalf("headers rate=%q channels=%q", resp.Header.Get("X-Audio-Rate"), resp.Header.Get("X-Audio-Channels"))
	}
	// 1 s of s16le stereo at 48 kHz, give or take one demuxed packet.
	if want := 48000 * 2 * 2; math.Abs(float64(len(body)-want)) > 4096 {
		t.Fatalf("track 0: %d bytes, want about %d", len(body), want)
	}
	if peak(body) < 1000 { // lavfi sine is 1/8 full scale, minus 3 dB for the stereo upmix
		t.Fatalf("track 0 peak %d, want an audible tone", peak(body))
	}

	resp, body = get("track=1&from=0&dur=2")
	if resp.StatusCode != 200 || peak(body) != 0 {
		t.Fatalf("track 1: status %d peak %d, want 200 and silence", resp.StatusCode, peak(body))
	}

	if resp, _ = get("track=2"); resp.StatusCode != 400 {
		t.Fatalf("track 2: status %d, want 400", resp.StatusCode)
	}
	if resp, _ = get("from=-1"); resp.StatusCode != 400 {
		t.Fatalf("from=-1: status %d, want 400", resp.StatusCode)
	}
	if resp, body = get("from=10&dur=5"); resp.StatusCode != 200 || len(body) != 0 {
		t.Fatalf("past end: status %d, %d bytes, want 200 empty", resp.StatusCode, len(body))
	}

	// The fixture is moov-at-end (ffmpeg's default): /stream must serve it
	// with the moov first, and /download the untouched original.
	if faststart.DetectFile(src) != faststart.MoovAtEnd {
		t.Fatal("fixture is not moov-at-end")
	}
	for _, tc := range []struct {
		path string
		want int
	}{{"/stream", faststart.MoovFront}, {"/download", faststart.MoovAtEnd}} {
		resp, err := http.Get(base + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if got := faststart.Detect(bytes.NewReader(body)); got != tc.want {
			t.Fatalf("%s: moov layout %d, want %d", tc.path, got, tc.want)
		}
	}
	req, _ := http.NewRequest("GET", base+"/stream", nil)
	req.Header.Set("Range", "bytes=0-99")
	rresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := io.ReadAll(rresp.Body)
	rresp.Body.Close()
	if rresp.StatusCode != 206 || len(head) != 100 {
		t.Fatalf("range: status %d, %d bytes", rresp.StatusCode, len(head))
	}

	_ = os.Remove(src)
	if resp, _ = get("from=0"); resp.StatusCode != 410 {
		t.Fatalf("missing file: status %d, want 410", resp.StatusCode)
	}
}

func peak(b []byte) int {
	m := 0
	for i := 0; i+1 < len(b); i += 2 {
		v := int(int16(binary.LittleEndian.Uint16(b[i:])))
		if v < 0 {
			v = -v
		}
		if v > m {
			m = v
		}
	}
	return m
}
