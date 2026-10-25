package faststart

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func box(typ string, payloadLen int) []byte {
	b := make([]byte, 8+payloadLen)
	binary.BigEndian.PutUint32(b[:4], uint32(8+payloadLen))
	copy(b[4:8], typ)
	return b
}

func largeBox(typ string, payloadLen int) []byte {
	b := make([]byte, 16+payloadLen)
	binary.BigEndian.PutUint32(b[:4], 1)
	copy(b[4:8], typ)
	binary.BigEndian.PutUint64(b[8:16], uint64(16+payloadLen))
	return b
}

func zeroBox(typ string, payloadLen int) []byte {
	b := make([]byte, 8+payloadLen)
	copy(b[4:8], typ)
	return b
}

func concat(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

func TestDetect(t *testing.T) {
	ftyp := box("ftyp", 16)
	moov := box("moov", 100)
	mdat := box("mdat", 100)

	cases := []struct {
		name string
		data []byte
		want int
	}{
		{"moov before mdat", concat(ftyp, moov, mdat), MoovFront},
		{"mdat before moov", concat(ftyp, mdat, moov), MoovAtEnd},
		{"free boxes skipped", concat(ftyp, box("free", 4), box("wide", 0), mdat, moov), MoovAtEnd},
		{"largesize mdat", concat(ftyp, largeBox("mdat", 32), moov), MoovAtEnd},
		{"size-zero mdat to EOF", concat(ftyp, moov, zeroBox("mdat", 100)), MoovFront},
		{"size-zero free stops walk", concat(ftyp, zeroBox("free", 4), moov), MoovFront},
		{"garbage", []byte("this is definitely not an mp4 file"), MoovFront},
		{"empty", nil, MoovFront},
		{"truncated header", []byte{0, 0, 0, 20, 'f', 't'}, MoovFront},
		{"truncated after ftyp", concat(ftyp, []byte{0, 0}), MoovFront},
		{"undersized box", concat(ftyp, []byte{0, 0, 0, 4, 'f', 'r', 'e', 'e'}, mdat), MoovFront},
		{"non-ascii box type", concat(ftyp, []byte{0, 0, 0, 12, 0xff, 0x01, 0x02, 0x03, 0, 0, 0, 0}), MoovFront},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Detect(bytes.NewReader(c.data)); got != c.want {
				t.Errorf("Detect() = %d, want %d", got, c.want)
			}
		})
	}
}
