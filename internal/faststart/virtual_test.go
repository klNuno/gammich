package faststart

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
)

// mkBox builds a box with a 32-bit header around the concatenated payload.
func mkBox(typ string, payload ...[]byte) []byte {
	p := bytes.Join(payload, nil)
	b := binary.BigEndian.AppendUint32(nil, uint32(8+len(p)))
	return append(append(b, typ...), p...)
}

// mkOffsets builds an stco (4-byte entries) or co64 (8-byte entries) box.
func mkOffsets(typ string, offs ...uint64) []byte {
	p := binary.BigEndian.AppendUint32(nil, 0)
	p = binary.BigEndian.AppendUint32(p, uint32(len(offs)))
	for _, v := range offs {
		if typ == "co64" {
			p = binary.BigEndian.AppendUint64(p, v)
		} else {
			p = binary.BigEndian.AppendUint32(p, uint32(v))
		}
	}
	return mkBox(typ, p)
}

// mkTrak wraps an offset table in trak/mdia/minf/stbl, with a few copied-through
// siblings in non-standard order.
func mkTrak(table []byte) []byte {
	stbl := mkBox("stbl", table, mkBox("stsd", make([]byte, 24)), mkBox("stsz", make([]byte, 12)))
	minf := mkBox("minf", mkBox("vmhd", make([]byte, 12)), stbl)
	mdia := mkBox("mdia", mkBox("mdhd", make([]byte, 24)), mkBox("hdlr", make([]byte, 25)), minf)
	return mkBox("trak", mkBox("tkhd", make([]byte, 84)), mkBox("edts", make([]byte, 20)), mdia, mkBox("udta", make([]byte, 9)))
}

type tableInfo struct {
	typ  string
	offs []uint64
}

// checkTree verifies every container is exactly filled by its children and
// returns the chunk offset tables in document order.
func checkTree(t *testing.T, data []byte) []tableInfo {
	t.Helper()
	var out []tableInfo
	for len(data) > 0 {
		if len(data) < 8 {
			t.Fatalf("trailing %d bytes", len(data))
		}
		n := int(binary.BigEndian.Uint32(data))
		typ := string(data[4:8])
		if n < 8 || n > len(data) {
			t.Fatalf("%s: size %d does not fit in %d", typ, n, len(data))
		}
		payload := data[8:n]
		switch typ {
		case "moov", "trak", "mdia", "minf", "stbl":
			out = append(out, checkTree(t, payload)...)
		case "stco", "co64":
			w := 4
			if typ == "co64" {
				w = 8
			}
			cnt := int(binary.BigEndian.Uint32(payload[4:8]))
			if len(payload) != 8+cnt*w {
				t.Fatalf("%s: payload %d for %d entries", typ, len(payload), cnt)
			}
			ti := tableInfo{typ: typ}
			for i := 0; i < cnt; i++ {
				if w == 8 {
					ti.offs = append(ti.offs, binary.BigEndian.Uint64(payload[8+i*8:]))
				} else {
					ti.offs = append(ti.offs, uint64(binary.BigEndian.Uint32(payload[8+i*4:])))
				}
			}
			out = append(out, ti)
		}
		data = data[n:]
	}
	return out
}

// sparseFile is a large virtual file made of a few byte ranges over zeros.
type sparseFile map[int64][]byte

func (s sparseFile) ReadAt(p []byte, off int64) (int, error) {
	clear(p)
	for at, b := range s {
		lo, hi := max(off, at), min(off+int64(len(p)), at+int64(len(b)))
		if lo < hi {
			copy(p[lo-off:hi-off], b[lo-at:hi-at])
		}
	}
	return len(p), nil
}

func TestPlanForWidensStco(t *testing.T) {
	const mdatStart = 24
	const mdatLen = 5 << 30 // crosses 4 GiB so shifted offsets overflow 32 bits
	ftyp := mkBox("ftyp", make([]byte, 16))
	mdatHdr := largeBox("mdat", 0)[:16]
	binary.BigEndian.PutUint64(mdatHdr[8:], mdatLen)
	moovStart := int64(mdatStart + mdatLen)

	build := func(v2 uint64) []byte {
		return mkBox("moov",
			mkBox("mvhd", make([]byte, 100)),
			mkTrak(mkOffsets("stco", mdatStart+16, 0xFFFFFFF0)), // overflows at once
			mkTrak(mkOffsets("stco", v2)),                       // overflows only after trak 1 widens
			mkTrak(mkOffsets("co64", 1<<32+5)),                  // already 64-bit
			mkTrak(mkOffsets("stco", mdatStart+16, mdatStart+1000)),
		)
	}
	l0 := uint64(len(build(0)))
	v2 := 0xFFFFFFFF - l0 - 3
	moov := build(v2)
	size := moovStart + int64(len(moov))
	src := sparseFile{0: ftyp, mdatStart: mdatHdr, moovStart: moov}

	p, err := PlanFor(src, size)
	if err != nil {
		t.Fatalf("PlanFor: %v", err)
	}
	wantLen := l0 + 2*4 + 1*4
	if got := p.Size(); got != size+int64(wantLen-l0) {
		t.Fatalf("Size = %d, want %d", got, size+int64(wantLen-l0))
	}
	vr := p.NewReader(src)
	if Detect(vr) != MoovFront {
		t.Fatal("virtual file does not start with moov")
	}
	newMoov := make([]byte, wantLen)
	if _, err := vr.Seek(mdatStart, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(vr, newMoov); err != nil {
		t.Fatal(err)
	}
	if string(newMoov[4:8]) != "moov" || uint64(binary.BigEndian.Uint32(newMoov)) != wantLen {
		t.Fatalf("new moov header %x", newMoov[:8])
	}
	got := checkTree(t, newMoov)
	want := []tableInfo{
		{"co64", []uint64{mdatStart + 16 + wantLen, 0xFFFFFFF0 + wantLen}},
		{"co64", []uint64{v2 + wantLen}},
		{"co64", []uint64{1<<32 + 5 + wantLen}},
		{"stco", []uint64{mdatStart + 16 + wantLen, mdatStart + 1000 + wantLen}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tables, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].typ != want[i].typ || !equalU64(got[i].offs, want[i].offs) {
			t.Errorf("table %d = %s %x, want %s %x", i, got[i].typ, got[i].offs, want[i].typ, want[i].offs)
		}
	}
	// The mdat header lands right after the moov.
	hdr := make([]byte, 16)
	if _, err := io.ReadFull(vr, hdr); err != nil || !bytes.Equal(hdr, mdatHdr) {
		t.Fatalf("mdat header after moov = %x, %v", hdr, err)
	}
}

func equalU64(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPlanForNotApplicable(t *testing.T) {
	ftyp := mkBox("ftyp", make([]byte, 16))
	mdat := mkBox("mdat", make([]byte, 64))
	moov := mkBox("moov", mkBox("mvhd", make([]byte, 100)), mkTrak(mkOffsets("stco", 40)))
	cases := []struct {
		name string
		data []byte
	}{
		{"moov first", concat(ftyp, moov, mdat)},
		{"no moov", concat(ftyp, mdat)},
		{"no mdat", concat(ftyp, moov)},
		{"moof", concat(ftyp, mdat, moov, mkBox("moof", make([]byte, 8)), mkBox("mdat", nil))},
		{"mvex", concat(ftyp, mdat, mkBox("moov", mkBox("mvhd", make([]byte, 100)), mkBox("mvex", make([]byte, 8))))},
		{"two moov", concat(ftyp, mdat, moov, moov)},
		{"saio", concat(ftyp, mdat, mkBox("moov", mkBox("trak", mkBox("mdia", mkBox("minf", mkBox("stbl", mkBox("saio", make([]byte, 12))))))))},
		{"offset inside moov", concat(ftyp, mdat, mkBox("moov", mkTrak(mkOffsets("stco", 24+72+8))))},
		{"offset past eof", concat(ftyp, mdat, mkBox("moov", mkTrak(mkOffsets("stco", 1<<20))))},
		{"stco count too big", concat(ftyp, mdat, mkBox("moov", mkTrak(mkBox("stco", []byte{0, 0, 0, 0, 0, 0, 1, 0}))))},
		{"child overruns parent", concat(ftyp, mdat, mkBox("moov", []byte{0, 0, 0, 99, 't', 'r', 'a', 'k'}))},
		{"top box overruns eof", concat(ftyp, mdat, moov[:len(moov)-1])},
		{"garbage", []byte("definitely not an mp4 file at all")},
		{"empty", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := PlanFor(bytes.NewReader(c.data), int64(len(c.data)))
			if !errors.Is(err, ErrNotApplicable) {
				t.Fatalf("err = %v, want ErrNotApplicable", err)
			}
		})
	}

	t.Run("moov too big", func(t *testing.T) {
		hdr := binary.BigEndian.AppendUint32(nil, maxMoovSize+9)
		hdr = append(hdr, "moov"...)
		prefix := concat(ftyp, mdat)
		size := int64(len(prefix)) + maxMoovSize + 9
		src := sparseFile{0: prefix, int64(len(prefix)): hdr}
		if _, err := PlanFor(src, size); !errors.Is(err, ErrNotApplicable) {
			t.Fatalf("err = %v, want ErrNotApplicable", err)
		}
	})
}

// TestReader checks random spans across segment boundaries, and that the
// relocated offsets (including one into a second mdat after the moov) still
// point at the same bytes.
func TestReader(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	randBytes := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(rng.Uint32())
		}
		return b
	}
	ftyp := mkBox("ftyp", make([]byte, 16))
	mdat := mkBox("mdat", randBytes(1000))
	free := mkBox("free", randBytes(10))
	mdat1At := uint64(len(ftyp) + 8)
	head := concat(ftyp, mdat, free)
	moovLen := len(mkBox("moov", mkTrak(mkOffsets("stco", 0, 0, 0))))
	mdat2At := uint64(len(head) + moovLen + 8)
	offs := []uint64{mdat1At, mdat1At + 500, mdat2At + 100}
	moov := mkBox("moov", mkTrak(mkOffsets("stco", offs...)))
	orig := concat(head, moov, mkBox("mdat", randBytes(300)))

	p, err := PlanFor(bytes.NewReader(orig), int64(len(orig)))
	if err != nil {
		t.Fatalf("PlanFor: %v", err)
	}
	virt, err := io.ReadAll(p.NewReader(bytes.NewReader(orig)))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(virt)) != p.Size() || len(virt) != len(orig) {
		t.Fatalf("materialized %d bytes, Size %d, orig %d", len(virt), p.Size(), len(orig))
	}
	if Detect(bytes.NewReader(virt)) != MoovFront {
		t.Fatal("materialized file does not start with moov")
	}
	ins := int(mdat1At - 8)
	tables := checkTree(t, virt[ins:ins+len(moov)])
	for i, o := range offs {
		n := tables[0].offs[i]
		if !bytes.Equal(orig[o:o+16], virt[n:n+16]) {
			t.Errorf("offset %d: %d -> %d points at different bytes", i, o, n)
		}
	}

	if err := iotest.TestReader(p.NewReader(bytes.NewReader(orig)), virt); err != nil {
		t.Fatal(err)
	}

	r := p.NewReader(bytes.NewReader(orig))
	for i := 0; i < 2000; i++ {
		off := rng.IntN(len(virt) + 8)
		n := rng.IntN(300)
		if _, err := r.Seek(int64(off), io.SeekStart); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(io.LimitReader(r, int64(n)))
		if err != nil {
			t.Fatal(err)
		}
		want := virt[min(off, len(virt)):min(off+n, len(virt))]
		if !bytes.Equal(got, want) {
			t.Fatalf("span [%d,+%d): got %d bytes, mismatch", off, n, len(got))
		}
	}
	if end, err := r.Seek(-5, io.SeekEnd); err != nil || end != p.Size()-5 {
		t.Fatalf("Seek end = %d, %v", end, err)
	}
	if _, err := r.Seek(-1, io.SeekStart); err == nil {
		t.Fatal("negative seek accepted")
	}
}

func requireFFmpeg(t *testing.T) string {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	return ff
}

// videoCodec picks libx264 when the ffmpeg build has it. Probed once: every
// ffmpeg launch costs over a second on Windows.
var videoCodec = sync.OnceValue(func() []string {
	if out, _ := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output(); bytes.Contains(out, []byte("libx264")) {
		return []string{"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p"}
	}
	return []string{"-c:v", "mpeg4"}
})

// ffmpegFixture renders a tiny clip into dir. Without +faststart the mp4
// muxer writes moov at the end, like game recorders.
func ffmpegFixture(t *testing.T, ff, dir, name string, audio []string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	args := []string{"-y", "-v", "error",
		"-f", "lavfi", "-i", "testsrc=duration=3:size=160x120:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=3",
		"-map", "0:v"}
	args = append(args, videoCodec()...)
	args = append(args, audio...)
	args = append(args, "-threads", "2", path)
	if out, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg fixture: %v: %s", err, out)
	}
	return path
}

func framemd5(t *testing.T, ff, path string) string {
	t.Helper()
	out, err := exec.Command(ff, "-v", "error", "-i", path, "-map", "0", "-c", "copy", "-f", "framemd5", "-").Output()
	if err != nil {
		t.Fatalf("framemd5 %s: %v", path, err)
	}
	return string(out)
}

func TestVirtualMatchesOriginal(t *testing.T) {
	ff := requireFFmpeg(t)
	dir := t.TempDir()
	cases := []struct {
		name    string
		audio   []string
		streams int
	}{
		{"h264+aac", []string{"-map", "1:a", "-c:a", "aac", "-b:a", "64k"}, 2},
		{"h264+2xpcm_f32le", []string{"-map", "1:a", "-map", "2:a", "-c:a", "pcm_f32le"}, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := ffmpegFixture(t, ff, dir, c.name+".mp4", c.audio)
			if DetectFile(src) != MoovAtEnd {
				t.Fatal("fixture does not have moov at end")
			}
			f, err := os.Open(src)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			p, err := PlanFor(f, info.Size())
			if err != nil {
				t.Fatalf("PlanFor: %v", err)
			}

			dst := filepath.Join(t.TempDir(), "virtual.mp4")
			out, err := os.Create(dst)
			if err != nil {
				t.Fatal(err)
			}
			n, err := io.Copy(out, p.NewReader(f))
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				t.Fatal(err)
			}
			if n != p.Size() {
				t.Fatalf("wrote %d bytes, Size %d", n, p.Size())
			}
			if DetectFile(dst) != MoovFront {
				t.Fatal("virtual file does not have moov first")
			}

			want, got := framemd5(t, ff, src), framemd5(t, ff, dst)
			if got != want {
				t.Fatalf("framemd5 differs\noriginal:\n%s\nvirtual:\n%s", want, got)
			}
			for s := 0; s < c.streams; s++ {
				if !strings.Contains(want, "\n"+string(rune('0'+s))+", ") {
					t.Fatalf("stream %d missing from framemd5", s)
				}
			}
		})
	}

	t.Run("ffmpeg faststart output refused", func(t *testing.T) {
		// Reuse the first case's fixture unless -run filtered it out.
		src := filepath.Join(dir, cases[0].name+".mp4")
		if _, err := os.Stat(src); err != nil {
			src = ffmpegFixture(t, ff, dir, cases[0].name+".mp4", cases[0].audio)
		}
		fs := filepath.Join(dir, "fs.mp4")
		if out, err := exec.Command(ff, "-y", "-v", "error", "-i", src, "-c", "copy", "-movflags", "+faststart", fs).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v: %s", err, out)
		}
		data, err := os.ReadFile(fs)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := PlanFor(bytes.NewReader(data), int64(len(data))); !errors.Is(err, ErrNotApplicable) {
			t.Fatalf("err = %v, want ErrNotApplicable", err)
		}
	})
}
