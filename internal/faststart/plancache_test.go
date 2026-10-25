package faststart

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

// countingReader counts ReadAt calls so tests can tell a cache hit from a
// rebuild.
type countingReader struct {
	r     io.ReaderAt
	calls atomic.Int64
}

func (c *countingReader) ReadAt(p []byte, off int64) (int, error) {
	c.calls.Add(1)
	return c.r.ReadAt(p, off)
}

// moovAtEndFile is ftyp + mdat + moov with one stco pointing into mdat.
func moovAtEndFile() []byte {
	ftyp := mkBox("ftyp", []byte("isom0000"))
	mdat := mkBox("mdat", bytes.Repeat([]byte{7}, 64))
	chunk := uint64(len(ftyp) + 8)
	return append(append(ftyp, mdat...), mkBox("moov", mkTrak(mkOffsets("stco", chunk)))...)
}

func TestPlanCacheHitRefusalAndEviction(t *testing.T) {
	file := moovAtEndFile()
	src := &countingReader{r: bytes.NewReader(file)}
	c := &PlanCache{}
	key := PlanKey{ID: 1, Size: int64(len(file)), Mtime: 1}

	p1, err := c.Get(key, src)
	if err != nil || p1 == nil {
		t.Fatalf("first Get = %v, %v; want a plan", p1, err)
	}
	reads := src.calls.Load()
	p2, err := c.Get(key, src)
	if err != nil || p2 != p1 || src.calls.Load() != reads {
		t.Fatalf("second Get rebuilt the plan (same=%v, reads %d then %d)", p2 == p1, reads, src.calls.Load())
	}

	// A new mtime is a new version of the file: rebuilt.
	if p3, _ := c.Get(PlanKey{ID: 1, Size: key.Size, Mtime: 2}, src); p3 == p1 {
		t.Fatal("new mtime reused the old plan")
	}

	// moov already first: nil plan, cached as a refusal.
	front := append(mkBox("ftyp", []byte("isom0000")), mkBox("moov", mkTrak(mkOffsets("stco", 0)))...)
	front = append(front, mkBox("mdat", []byte{1, 2, 3})...)
	fsrc := &countingReader{r: bytes.NewReader(front)}
	fkey := PlanKey{ID: 2, Size: int64(len(front))}
	if p, err := c.Get(fkey, fsrc); p != nil || err != nil {
		t.Fatalf("moov-front Get = %v, %v; want nil, nil", p, err)
	}
	freads := fsrc.calls.Load()
	if _, _ = c.Get(fkey, fsrc); fsrc.calls.Load() != freads {
		t.Fatal("refusal was not cached")
	}

	// Read errors are returned and not cached.
	bad := &countingReader{r: failingReader{}}
	if _, err := c.Get(PlanKey{ID: 3, Size: 100}, bad); err == nil || errors.Is(err, ErrNotApplicable) {
		t.Fatalf("read error = %v; want a real error", err)
	}
	if _, _ = c.Get(PlanKey{ID: 3, Size: 100}, bad); bad.calls.Load() < 2 {
		t.Fatal("read error was cached")
	}

	// A tiny budget keeps only the newest entry.
	small := &PlanCache{MaxBytes: 1}
	for id := int64(1); id <= 3; id++ {
		if _, err := small.Get(PlanKey{ID: id, Size: key.Size}, bytes.NewReader(file)); err != nil {
			t.Fatal(err)
		}
	}
	if small.ll.Len() != 1 {
		t.Fatalf("cache holds %d entries over budget, want 1", small.ll.Len())
	}
}

type failingReader struct{}

func (failingReader) ReadAt([]byte, int64) (int, error) { return 0, errors.New("nas gone") }

// Concurrent opens of the same clip build the plan once.
func TestPlanCacheDedupesConcurrentBuilds(t *testing.T) {
	file := moovAtEndFile()
	gate := make(chan struct{})
	src := &countingReader{r: gatedReader{r: bytes.NewReader(file), gate: gate}}
	c := &PlanCache{}
	key := PlanKey{ID: 1, Size: int64(len(file))}

	var wg sync.WaitGroup
	plans := make([]*Plan, 8)
	for i := range plans {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plans[i], _ = c.Get(key, src)
		}()
	}
	close(gate)
	wg.Wait()
	for i, p := range plans {
		if p == nil || p != plans[0] {
			t.Fatalf("goroutine %d got plan %p, want shared %p", i, p, plans[0])
		}
	}
	solo := &countingReader{r: bytes.NewReader(file)}
	if _, err := (&PlanCache{}).Get(key, solo); err != nil {
		t.Fatal(err)
	}
	if got, want := src.calls.Load(), solo.calls.Load(); got != want {
		t.Fatalf("8 concurrent Gets made %d reads, one build makes %d", got, want)
	}
}

type gatedReader struct {
	r    io.ReaderAt
	gate chan struct{}
}

func (g gatedReader) ReadAt(p []byte, off int64) (int, error) {
	<-g.gate
	return g.r.ReadAt(p, off)
}
