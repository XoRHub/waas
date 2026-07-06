package guac

import (
	"bytes"
	"strings"
	"testing"
)

// frameCheck asserts a frame is a sequence of complete instructions.
func frameCheck(t *testing.T, frame []byte) {
	t.Helper()
	n, err := completePrefix(frame)
	if err != nil {
		t.Fatalf("frame is not parseable: %v (frame=%q)", err, frame)
	}
	if n != len(frame) {
		t.Fatalf("frame does not end on an instruction boundary: %d/%d bytes (frame=%q)", n, len(frame), frame)
	}
}

func TestFramerReassemblesArbitrarySplits(t *testing.T) {
	instructions := []Instruction{
		{Opcode: "sync", Args: []string{"12345"}},
		{Opcode: "png", Args: []string{"0", "3", "0", "0", strings.Repeat("iVBORw0KGgo", 500)}},
		{Opcode: "clipboard", Args: []string{"1", "héhé — cœur 💙"}}, // multi-byte runes
		{Opcode: "name", Args: []string{""}},                          // empty element
		{Opcode: "nop", Args: nil},
	}
	var stream bytes.Buffer
	for _, inst := range instructions {
		stream.WriteString(inst.Encode())
	}
	raw := stream.Bytes()

	// Feed the stream in every chunk size from 1 byte to whole-stream and
	// verify each emitted frame ends on a boundary and the concatenation
	// reproduces the stream byte for byte.
	for _, chunk := range []int{1, 2, 3, 7, 64, 1024, len(raw)} {
		var framer Framer
		var out bytes.Buffer
		for i := 0; i < len(raw); i += chunk {
			end := min(i+chunk, len(raw))
			frame, err := framer.Push(raw[i:end])
			if err != nil {
				t.Fatalf("chunk=%d: push failed: %v", chunk, err)
			}
			if len(frame) > 0 {
				frameCheck(t, frame)
				out.Write(frame)
			}
		}
		if !bytes.Equal(out.Bytes(), raw) {
			t.Fatalf("chunk=%d: reassembled stream differs from input", chunk)
		}
	}
}

func TestFramerBatchesWhatIsAvailable(t *testing.T) {
	var framer Framer
	two := []byte("4.sync,5.12345;3.nop;")
	frame, err := framer.Push(two)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if !bytes.Equal(frame, two) {
		t.Fatalf("expected both instructions in one frame, got %q", frame)
	}
}

func TestFramerHoldsPartialTail(t *testing.T) {
	var framer Framer
	frame, err := framer.Push([]byte("4.sync,5.12345;4.si"))
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if string(frame) != "4.sync,5.12345;" {
		t.Fatalf("expected only the complete instruction, got %q", frame)
	}
	frame, err = framer.Push([]byte("ze,1.1;"))
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if string(frame) != "4.size,1.1;" {
		t.Fatalf("expected completed tail instruction, got %q", frame)
	}
}

func TestFramerRejectsGarbage(t *testing.T) {
	var framer Framer
	if _, err := framer.Push([]byte("not-a-guacamole-stream")); err == nil {
		t.Fatal("expected a protocol error on garbage input")
	}
}

func TestFramerLengthCountsRunesNotBytes(t *testing.T) {
	// "é" is 1 code point / 2 bytes: the length prefix says 1.
	raw := []byte("4.name,1.é;")
	var framer Framer
	// Split inside the multi-byte rune.
	cut := bytes.IndexRune(raw, 'é') + 1
	frame, err := framer.Push(raw[:cut])
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if frame != nil {
		t.Fatalf("expected no frame while a rune is split, got %q", frame)
	}
	frame, err = framer.Push(raw[cut:])
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if !bytes.Equal(frame, raw) {
		t.Fatalf("expected the full instruction, got %q", frame)
	}
}

func TestInternalMessageDetection(t *testing.T) {
	ping := []byte("0.,4.ping,13.1751791234567;")
	if !IsInternalMessage(ping) || !IsInternalPing(ping) {
		t.Fatal("tunnel ping must be detected as internal ping")
	}
	if IsInternalMessage([]byte("3.key,5.65307,1.1;")) {
		t.Fatal("regular instructions must not be flagged internal")
	}
	other := []byte("0.,4.pong;")
	if !IsInternalMessage(other) || IsInternalPing(other) {
		t.Fatal("non-ping internal messages must be internal but not pings")
	}
}
