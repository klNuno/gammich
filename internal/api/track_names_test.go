package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	gammichdb "github.com/klNuno/gammich/internal/db"
	"github.com/klNuno/gammich/internal/preview"
)

// TestTrackNames checks where audio track names come from: the file itself
// (OBS MKV titles), then named clips of the same recorder for an unnamed
// file (OBS MP4), then ShadowPlay's fixed two-track layout.
func TestTrackNames(t *testing.T) {
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
	fixture := func(name string, meta ...string) preview.AudioInfo {
		t.Helper()
		src := filepath.Join(root, name)
		args := []string{"-v", "error", "-y",
			"-f", "lavfi", "-i", "testsrc=size=160x90:rate=30:duration=1",
			"-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
			"-map", "0:v", "-map", "1:a", "-map", "1:a", "-t", "1",
			"-c:v", "mpeg4", "-c:a", "pcm_f32le"}
		args = append(append(args, meta...), src)
		if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture %s: %v: %s", name, err, out)
		}
		a, err := preview.ProbeAudio(ctx, ffprobe, src)
		if err != nil {
			t.Fatalf("probe %s: %v", name, err)
		}
		return a
	}

	named := fixture("obs.mkv", "-metadata:s:a:0", "title=Normal", "-metadata:s:a:1", "title=Micro")
	if !reflect.DeepEqual(named.Titles, []string{"Normal", "Micro"}) {
		t.Fatalf("mkv titles = %q, want [Normal Micro]", named.Titles)
	}
	// ffmpeg's MP4 muxer writes "SoundHandler", like OBS: not a name.
	unnamed := fixture("obs.mp4")
	if !reflect.DeepEqual(unnamed.Titles, []string{"", ""}) {
		t.Fatalf("mp4 titles = %q, want two empty", unnamed.Titles)
	}
	if got := gammichdb.TrackTitlesJSON(unnamed.Titles); got != "[]" {
		t.Fatalf("TrackTitlesJSON(unnamed) = %q, want []", got)
	}

	db, err := gammichdb.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := gammichdb.Migrate(db); err != nil {
		t.Fatal(err)
	}
	add := func(rel, source string, titles []string) int64 {
		t.Helper()
		id, _, _, err := gammichdb.UpsertClip(ctx, db, gammichdb.Clip{
			RelPath: rel, Name: rel, OriginalFilename: rel, SizeBytes: 1, Mtime: 1, Kind: "video"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE clips SET audio_codec='pcm_f32le', audio_track_count=2, audio_track_titles=? WHERE id=?`,
			gammichdb.TrackTitlesJSON(titles), id); err != nil {
			t.Fatal(err)
		}
		if source != "" {
			if err := gammichdb.AttachAutoTags(ctx, db, id, []string{source}); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	mkv := add("OBS/a.mkv", "OBS", named.Titles)
	mp4 := add("OBS/b.mp4", "OBS", unnamed.Titles)
	sp := add("CS/c.DVR.mp4", "ShadowPlay", []string{"", ""})
	bare := add("Desktop/d.mp4", "", []string{"", ""})

	s := &Server{DB: db, VideoRoot: root, FFmpegPath: ffmpeg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ts := httptest.NewServer(s.Router())
	defer ts.Close()

	for _, tc := range []struct {
		name    string
		id      int64
		want    []string
		guessed bool
	}{
		{"named mkv", mkv, []string{"Normal", "Micro"}, false},
		{"unnamed obs mp4", mp4, []string{"Normal", "Micro"}, true},
		{"shadowplay", sp, []string{"Game", "Microphone"}, true},
		{"no recorder tag", bare, nil, false},
	} {
		resp, err := http.Get(ts.URL + "/api/clips/" + strconv.FormatInt(tc.id, 10))
		if err != nil {
			t.Fatal(err)
		}
		var dto clipDTO
		err = json.NewDecoder(resp.Body).Decode(&dto)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%s: decode: %v", tc.name, err)
		}
		if !reflect.DeepEqual(dto.AudioTrackNames, tc.want) || dto.AudioTrackNamesGuessed != tc.guessed {
			t.Errorf("%s: names %q guessed %v, want %q %v",
				tc.name, dto.AudioTrackNames, dto.AudioTrackNamesGuessed, tc.want, tc.guessed)
		}
	}
}
