package faststart

// Virtual faststart: instead of remuxing a moov-at-end MP4 into the cache, the
// moov is rebuilt in memory with its chunk offsets shifted, and the file is
// served as a splice of the original bytes around it:
//
//	[0, firstMdat) + newMoov + [firstMdat, moovStart) + [moovEnd, EOF)
//
// Nothing is written to disk and the source is only ever read.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
)

// ErrNotApplicable means the file is not a plain moov-after-mdat MP4 that can
// be relocated (fragmented, no moov or mdat, moov already first, moov too big,
// or malformed boxes). The caller serves the original or falls back to Cache.
var ErrNotApplicable = errors.New("faststart: not a relocatable moov-at-end mp4")

// maxMoovSize bounds the moov read into memory. A multi-hour recording with
// five tracks stays in the low megabytes; anything past this is suspect.
const maxMoovSize = 64 << 20

// maxBoxDepth bounds recursion into nested moov containers.
const maxBoxDepth = 16

// moovContainers are the boxes whose children are parsed; every other box is
// copied through byte for byte (edts, udta, meta, stsd, ...).
var moovContainers = map[string]bool{"trak": true, "mdia": true, "minf": true, "stbl": true}

// Plan is the relocated layout of one file. It holds no reference to the
// source, so it can be cached per clip and shared by concurrent requests.
type Plan struct {
	segs []segment
	moov []byte
	size int64
}

// segment maps [off, off+n) of the virtual file to the source at src, or to
// Plan.moov when src < 0.
type segment struct {
	off, n, src int64
}

// Size is the length of the virtual file.
func (p *Plan) Size() int64 { return p.size }

// PlanFor parses the top-level boxes of the size-byte file behind r, reads its
// moov and builds the relocated layout. Format problems return
// ErrNotApplicable; read failures are returned as is.
func PlanFor(r io.ReaderAt, size int64) (*Plan, error) {
	l, err := scanTop(r, size)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, l.moovEnd-l.moovStart)
	if _, err := readFullAt(r, buf, l.moovStart); err != nil {
		return nil, fmt.Errorf("faststart: read moov: %w", err)
	}
	hl := 8
	if binary.BigEndian.Uint32(buf) == 1 {
		hl = 16
	}
	kids, err := parseBoxes(buf[hl:], "moov", 0)
	if err != nil {
		return nil, err
	}
	root := &node{typ: "moov", container: true, children: kids}

	var tables []*node
	root.walk(func(b *node) {
		if b.offsets != nil {
			tables = append(tables, b)
		}
	})
	// An offset past EOF or inside the old moov is malformed. Bounding by the
	// file size also keeps every shift below overflow.
	for _, b := range tables {
		for _, v := range b.offsets {
			if v > uint64(size) || !l.movable(v) {
				return nil, ErrNotApplicable
			}
		}
	}

	// Fixpoint: the shift is the new moov size, which grows by 4 bytes per
	// entry for each stco widened to co64, which may push another stco over
	// 32 bits. Widening is sticky and the moov only grows, so this stops after
	// at most len(tables) rounds.
	var moovLen int64
	for {
		moovLen = root.size()
		grew := false
		for _, b := range tables {
			if b.wide {
				continue
			}
			for _, v := range b.offsets {
				if l.shift(v, moovLen) > math.MaxUint32 {
					b.wide = true
					grew = true
					break
				}
			}
		}
		if !grew {
			break
		}
	}

	moov := root.encode(make([]byte, 0, moovLen), &l, moovLen)
	if int64(len(moov)) != moovLen {
		return nil, fmt.Errorf("faststart: moov encoded to %d bytes, planned %d", len(moov), moovLen)
	}

	p := &Plan{moov: moov}
	p.add(0, l.firstMdat)
	p.add(-1, moovLen)
	p.add(l.firstMdat, l.moovStart-l.firstMdat)
	p.add(l.moovEnd, size-l.moovEnd)
	return p, nil
}

func (p *Plan) add(src, n int64) {
	if n <= 0 {
		return
	}
	p.segs = append(p.segs, segment{off: p.size, n: n, src: src})
	p.size += n
}

// layout is where the relevant top-level boxes sit in the source.
type layout struct {
	firstMdat, moovStart, moovEnd int64
}

// movable reports whether a chunk offset points outside the old moov.
func (l *layout) movable(v uint64) bool {
	return v < uint64(l.moovStart) || v >= uint64(l.moovEnd)
}

// shift maps a source chunk offset to its position in the virtual file given
// the new moov length. Only offsets at or after the first mdat move; data
// after the old moov (a second mdat) moves by the size difference.
func (l *layout) shift(v uint64, moovLen int64) uint64 {
	switch {
	case v < uint64(l.firstMdat):
		return v
	case v < uint64(l.moovStart):
		return v + uint64(moovLen)
	}
	return v - uint64(l.moovEnd-l.moovStart) + uint64(moovLen)
}

// scanTop walks the top-level box headers and locates the first mdat and the
// moov, refusing anything that is not a plain moov-after-mdat file.
func scanTop(r io.ReaderAt, size int64) (layout, error) {
	l := layout{firstMdat: -1, moovStart: -1}
	var hdr [16]byte
	var off int64
	for i := 0; off < size; i++ {
		if i >= 1<<16 || size-off < 8 {
			return l, ErrNotApplicable
		}
		if _, err := readFullAt(r, hdr[:8], off); err != nil {
			return l, fmt.Errorf("faststart: read box header: %w", err)
		}
		n, hl := int64(binary.BigEndian.Uint32(hdr[:4])), int64(8)
		switch n {
		case 0:
			n = size - off
		case 1:
			if size-off < 16 {
				return l, ErrNotApplicable
			}
			if _, err := readFullAt(r, hdr[8:16], off+8); err != nil {
				return l, fmt.Errorf("faststart: read box header: %w", err)
			}
			large := binary.BigEndian.Uint64(hdr[8:16])
			if large > math.MaxInt64 {
				return l, ErrNotApplicable
			}
			n, hl = int64(large), 16
		}
		if n < hl || n > size-off {
			return l, ErrNotApplicable
		}
		switch string(hdr[4:8]) {
		case "mdat":
			if l.firstMdat < 0 {
				l.firstMdat = off
			}
		case "moov":
			if l.moovStart >= 0 {
				return l, ErrNotApplicable
			}
			l.moovStart, l.moovEnd = off, off+n
		case "moof", "mfra":
			return l, ErrNotApplicable
		}
		off += n
	}
	if l.firstMdat < 0 || l.moovStart < l.firstMdat || l.moovEnd-l.moovStart > maxMoovSize {
		return l, ErrNotApplicable
	}
	return l, nil
}

// node is one box of the parsed moov tree. Containers are rebuilt from their
// children, chunk offset tables from offsets, everything else from raw.
type node struct {
	typ       string
	raw       []byte
	container bool
	children  []*node
	// Chunk offset table (stco or co64 under stbl): offsets is non-nil, wide
	// means it is written as co64.
	offsets  []uint64
	verFlags [4]byte
	wide     bool
}

// parseBoxes splits a container payload into child boxes. A size-0 child runs
// to the end of its parent.
func parseBoxes(data []byte, parent string, depth int) ([]*node, error) {
	if depth > maxBoxDepth {
		return nil, ErrNotApplicable
	}
	var out []*node
	for len(data) > 0 {
		if len(data) < 8 {
			return nil, ErrNotApplicable
		}
		n, hl := uint64(binary.BigEndian.Uint32(data[:4])), uint64(8)
		switch n {
		case 0:
			n = uint64(len(data))
		case 1:
			if len(data) < 16 {
				return nil, ErrNotApplicable
			}
			n, hl = binary.BigEndian.Uint64(data[8:16]), 16
		}
		if n < hl || n > uint64(len(data)) {
			return nil, ErrNotApplicable
		}
		b := &node{typ: string(data[4:8]), raw: data[:n]}
		payload := data[hl:n]
		switch {
		case parent == "moov" && b.typ == "mvex":
			return nil, ErrNotApplicable
		case parent == "stbl" && b.typ == "saio":
			// Absolute sample auxiliary info offsets (encrypted content): not
			// rewritten, so refuse rather than serve a broken file.
			return nil, ErrNotApplicable
		case moovContainers[b.typ]:
			kids, err := parseBoxes(payload, b.typ, depth+1)
			if err != nil {
				return nil, err
			}
			b.raw, b.container, b.children = nil, true, kids
		case parent == "stbl" && (b.typ == "stco" || b.typ == "co64"):
			if err := b.parseOffsets(payload); err != nil {
				return nil, err
			}
		}
		out = append(out, b)
		data = data[n:]
	}
	return out, nil
}

func (b *node) parseOffsets(payload []byte) error {
	if len(payload) < 8 {
		return ErrNotApplicable
	}
	width := uint64(4)
	if b.typ == "co64" {
		width, b.wide = 8, true
	}
	count := uint64(binary.BigEndian.Uint32(payload[4:8]))
	if count*width > uint64(len(payload)-8) {
		return ErrNotApplicable
	}
	copy(b.verFlags[:], payload[:4])
	b.offsets = make([]uint64, count)
	entries := payload[8:]
	for i := range b.offsets {
		if width == 8 {
			b.offsets[i] = binary.BigEndian.Uint64(entries[i*8:])
		} else {
			b.offsets[i] = uint64(binary.BigEndian.Uint32(entries[i*4:]))
		}
	}
	b.raw = nil
	return nil
}

func (b *node) walk(fn func(*node)) {
	fn(b)
	for _, c := range b.children {
		c.walk(fn)
	}
}

// size is the encoded length. Rebuilt boxes always use an 8-byte header: the
// whole moov is under 4 GiB.
func (b *node) size() int64 {
	switch {
	case b.container:
		n := int64(8)
		for _, c := range b.children {
			n += c.size()
		}
		return n
	case b.offsets != nil:
		width := int64(4)
		if b.wide {
			width = 8
		}
		return 16 + int64(len(b.offsets))*width
	}
	return int64(len(b.raw))
}

// encode appends the box to dst with every chunk offset shifted for a new moov
// of moovLen bytes.
func (b *node) encode(dst []byte, l *layout, moovLen int64) []byte {
	switch {
	case b.container:
		dst = binary.BigEndian.AppendUint32(dst, uint32(b.size()))
		dst = append(dst, b.typ...)
		for _, c := range b.children {
			dst = c.encode(dst, l, moovLen)
		}
	case b.offsets != nil:
		typ := "stco"
		if b.wide {
			typ = "co64"
		}
		dst = binary.BigEndian.AppendUint32(dst, uint32(b.size()))
		dst = append(dst, typ...)
		dst = append(dst, b.verFlags[:]...)
		dst = binary.BigEndian.AppendUint32(dst, uint32(len(b.offsets)))
		for _, v := range b.offsets {
			nv := l.shift(v, moovLen)
			if b.wide {
				dst = binary.BigEndian.AppendUint64(dst, nv)
			} else {
				dst = binary.BigEndian.AppendUint32(dst, uint32(nv))
			}
		}
	default:
		dst = append(dst, b.raw...)
	}
	return dst
}

// NewReader returns a ReadSeeker (also an io.ReaderAt) over the virtual file,
// reading original bytes from r. It only holds a position, so creating one per
// request is free.
func (p *Plan) NewReader(r io.ReaderAt) io.ReadSeeker {
	return &virtualReader{p: p, r: r}
}

type virtualReader struct {
	p   *Plan
	r   io.ReaderAt
	pos int64
}

func (v *virtualReader) Read(b []byte) (int, error) {
	if v.pos >= v.p.size {
		return 0, io.EOF
	}
	n, err := v.ReadAt(b, v.pos)
	v.pos += int64(n)
	if err == io.EOF && n > 0 {
		err = nil
	}
	return n, err
}

func (v *virtualReader) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("faststart: negative offset")
	}
	segs := v.p.segs
	n := 0
	for n < len(b) {
		pos := off + int64(n)
		if pos >= v.p.size {
			return n, io.EOF
		}
		i := sort.Search(len(segs), func(i int) bool { return segs[i].off+segs[i].n > pos })
		s := segs[i]
		within := pos - s.off
		want := min(int64(len(b)-n), s.n-within)
		dst := b[n : n+int(want)]
		if s.src < 0 {
			copy(dst, v.p.moov[within:])
		} else if m, err := readFullAt(v.r, dst, s.src+within); err != nil {
			return n + m, err
		}
		n += int(want)
	}
	return n, nil
}

func (v *virtualReader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = v.pos + offset
	case io.SeekEnd:
		abs = v.p.size + offset
	default:
		return 0, errors.New("faststart: invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("faststart: negative position")
	}
	v.pos = abs
	return abs, nil
}

// readFullAt fills b from r at off. A short read (source shrank under us)
// reports io.ErrUnexpectedEOF.
func readFullAt(r io.ReaderAt, b []byte, off int64) (int, error) {
	n, err := r.ReadAt(b, off)
	if n == len(b) {
		return n, nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}
