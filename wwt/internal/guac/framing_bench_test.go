package guac

import (
	"bytes"
	"strings"
	"testing"
)

// benchBlob is a large base64 payload (ASCII), the size of a desktop tile.
var benchBlob = strings.Repeat("iVBORw0KGgoAAAANSUhEUgAA", 1000) // ~24 KB

// benchStream builds a guacd-like stream: many img/blob instructions whose
// values are large base64 payloads (ASCII), interleaved with small control
// instructions — the shape of a live desktop video feed.
func benchStream() []byte {
	var s bytes.Buffer
	for i := 0; i < 200; i++ {
		s.WriteString(Instruction{Opcode: "sync", Args: []string{"12345"}}.Encode())
		s.WriteString(Instruction{Opcode: "blob", Args: []string{"0", benchBlob}}.Encode())
	}
	return s.Bytes()
}

// BenchmarkFramer feeds the stream in 32 KiB reads (the proxy's read buffer
// size), so instruction boundaries land arbitrarily inside reads.
func BenchmarkFramer(b *testing.B) {
	raw := benchStream()
	const read = 32 * 1024
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		var f Framer
		for i := 0; i < len(raw); i += read {
			end := i + read
			if end > len(raw) {
				end = len(raw)
			}
			if _, err := f.Push(raw[i:end]); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkFilterToBrowserCopyBlocked filters one display frame (an image
// stream) with copy blocked — the path every guacd frame takes then.
func BenchmarkFilterToBrowserCopyBlocked(b *testing.B) {
	frame := wire(
		Instruction{Opcode: "img", Args: []string{"1", "14", "0", "image/png", "0", "0"}},
		Instruction{Opcode: "blob", Args: []string{"1", benchBlob}},
		Instruction{Opcode: "end", Args: []string{"1"}},
		Instruction{Opcode: "sync", Args: []string{"12345"}},
	)
	f := NewClipboardFilter(false, true)
	b.SetBytes(int64(len(frame)))
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if _, err := f.FilterToBrowser(frame); err != nil {
			b.Fatal(err)
		}
	}
}
