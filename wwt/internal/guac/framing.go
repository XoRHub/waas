package guac

import (
	"bytes"
	"fmt"
	"unicode/utf8"
)

// MaxPendingBytes bounds the bytes a Framer may hold while waiting for an
// instruction to complete. libguac caps instructions at 32 KiB; anything
// approaching this limit is a corrupted stream, not a slow one.
const MaxPendingBytes = 1 << 20

// Framer splits the raw guacd byte stream into WebSocket-safe frames.
//
// guacamole-common-js parses every WebSocket message as a standalone
// sequence of COMPLETE instructions: a message that ends mid-instruction
// closes the tunnel ("Incomplete instruction") or corrupts the element
// stream. guacd itself gives no such guarantee on TCP — reads split
// wherever the kernel likes — so the proxy must re-frame on instruction
// boundaries before forwarding.
type Framer struct {
	buf   []byte // accumulated stream bytes
	start int    // offset of the first byte not yet emitted as a frame
}

// Push appends raw stream bytes and returns every complete instruction
// accumulated so far as one frame (always ending on ';'), or nil while no
// instruction is complete yet. The returned slice is owned by the Framer and
// stays valid only until the following Push call.
func (f *Framer) Push(data []byte) ([]byte, error) {
	// Drop the already-emitted prefix by reusing buf instead of allocating a
	// fresh remainder on every frame. When reads land on instruction
	// boundaries (the common case) start == len(buf) and this is a zero-copy
	// reset; otherwise the short incomplete tail is memmoved to the front.
	if f.start > 0 {
		f.buf = f.buf[:copy(f.buf, f.buf[f.start:])]
		f.start = 0
	}
	f.buf = append(f.buf, data...)

	n, err := completePrefix(f.buf)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		if len(f.buf) > MaxPendingBytes {
			return nil, fmt.Errorf("no instruction boundary within %d bytes: corrupted guacd stream", len(f.buf))
		}
		return nil, nil
	}
	frame := f.buf[:n]
	f.start = n
	return frame, nil
}

// completePrefix returns the byte length of the longest prefix of b made of
// whole instructions (`LEN.VALUE,LEN.VALUE,…;` — lengths count code points,
// not bytes). A truncated tail is not an error: it just isn't counted yet.
func completePrefix(b []byte) (int, error) {
	p, last := 0, 0
	for p < len(b) {
		// Element length.
		length, digits := 0, 0
		for p < len(b) && b[p] >= '0' && b[p] <= '9' {
			length = length*10 + int(b[p]-'0')
			digits++
			p++
			if length > MaxPendingBytes {
				return 0, fmt.Errorf("element length %d exceeds any legal instruction", length)
			}
		}
		if p == len(b) {
			return last, nil // length digits may continue in the next read
		}
		if digits == 0 || b[p] != '.' {
			return 0, fmt.Errorf("malformed instruction stream: expected length prefix at byte %d", p)
		}
		p++

		// Element value: skip `length` code points. Guacd payloads (base64
		// image blobs) are overwhelmingly ASCII, so peel those off one byte
		// at a time and only fall back to rune decoding for multi-byte runes.
		for i := 0; i < length; i++ {
			if p >= len(b) {
				return last, nil // value continues in the next read
			}
			if b[p] < utf8.RuneSelf {
				p++
				continue
			}
			if !utf8.FullRune(b[p:]) {
				return last, nil // last rune split across reads
			}
			_, size := utf8.DecodeRune(b[p:])
			p += size
		}

		if p >= len(b) {
			return last, nil // separator not received yet
		}
		switch b[p] {
		case ',':
			p++
		case ';':
			p++
			last = p
		default:
			return 0, fmt.Errorf("malformed instruction stream: expected separator at byte %d, got %q", p, b[p])
		}
	}
	return last, nil
}

// internalPingPrefix matches a tunnel-internal ping: an instruction whose
// opcode is the zero-length string and whose first argument is "ping".
var internalPingPrefix = []byte("0.,4.ping,")

// IsInternalPing reports whether a browser WebSocket message is the JS
// tunnel's connection-stability ping. Those target the tunnel endpoint, not
// guacd: the endpoint must answer with an identical ping and never forward
// them (guacd has no notion of the zero-length internal opcode).
func IsInternalPing(msg []byte) bool {
	return bytes.HasPrefix(msg, internalPingPrefix)
}

// IsInternalMessage reports whether a browser WebSocket message carries the
// tunnel-internal zero-length opcode (ping or any future internal traffic).
func IsInternalMessage(msg []byte) bool {
	return bytes.HasPrefix(msg, []byte("0.,")) || bytes.HasPrefix(msg, []byte("0.;"))
}
