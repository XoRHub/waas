package guac

import (
	"strings"
	"testing"
)

func wire(instructions ...Instruction) []byte {
	var b strings.Builder
	for _, inst := range instructions {
		b.WriteString(inst.Encode())
	}
	return []byte(b.String())
}

func TestClipboardCopyBlockedDropsServerStreams(t *testing.T) {
	f := NewClipboardFilter(false, true)
	frame := wire(
		Instruction{Opcode: "sync", Args: []string{"1"}},
		Instruction{Opcode: "clipboard", Args: []string{"3", "text/plain"}},
		Instruction{Opcode: "blob", Args: []string{"3", "c2VjcmV0"}},
		Instruction{Opcode: "png", Args: []string{"0", "0", "0", "0", "iVBOR"}},
		Instruction{Opcode: "end", Args: []string{"3"}},
	)
	out, err := f.FilterToBrowser(frame)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "clipboard") || strings.Contains(got, "c2VjcmV0") {
		t.Fatalf("clipboard data leaked to the browser: %q", got)
	}
	if !strings.Contains(got, "sync") || !strings.Contains(got, "png") {
		t.Fatalf("non-clipboard instructions must pass: %q", got)
	}

	// A later blob on an unrelated stream still passes.
	out, err = f.FilterToBrowser(wire(Instruction{Opcode: "blob", Args: []string{"4", "aW1n"}}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "aW1n") {
		t.Fatal("unrelated streams must not be dropped")
	}
}

func TestClipboardCopyAllowedPassesThrough(t *testing.T) {
	f := NewClipboardFilter(true, true)
	frame := wire(
		Instruction{Opcode: "clipboard", Args: []string{"3", "text/plain"}},
		Instruction{Opcode: "blob", Args: []string{"3", "c2VjcmV0"}},
	)
	out, err := f.FilterToBrowser(frame)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(frame) {
		t.Fatalf("allowed traffic must pass byte-identical")
	}
}

func TestClipboardPasteBlockedAcksAndDrops(t *testing.T) {
	f := NewClipboardFilter(true, false)

	forward, reply, err := f.FilterToGuacd(wire(Instruction{Opcode: "clipboard", Args: []string{"7", "text/plain"}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(forward) != 0 {
		t.Fatalf("blocked paste must not reach guacd: %q", forward)
	}
	if !strings.Contains(string(reply), "ack") || !strings.Contains(string(reply), "771") {
		t.Fatalf("expected an error ack for the refused stream, got %q", reply)
	}

	// Follow-up blob/end of the refused stream are dropped silently;
	// unrelated instructions (mouse) pass.
	forward, _, err = f.FilterToGuacd(wire(
		Instruction{Opcode: "blob", Args: []string{"7", "cGF5bG9hZA=="}},
		Instruction{Opcode: "mouse", Args: []string{"10", "20", "1"}},
		Instruction{Opcode: "end", Args: []string{"7"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	got := string(forward)
	if strings.Contains(got, "cGF5bG9hZA==") || strings.Contains(got, "end") {
		t.Fatalf("refused stream leaked to guacd: %q", got)
	}
	if !strings.Contains(got, "mouse") {
		t.Fatalf("input events must keep flowing: %q", got)
	}
}

func TestClipboardLiveToggleClampedToGrant(t *testing.T) {
	// Grant allows copy; the user toggles it off then on again.
	f := NewClipboardFilter(true, false)

	reply := f.HandleControl([]byte(Instruction{Opcode: "", Args: []string{"waas-clipboard", "copy", "0"}}.Encode()))
	if !strings.Contains(string(reply), "copy") || !strings.Contains(string(reply), "1.0;") {
		t.Fatalf("expected copy-off confirmation, got %q", reply)
	}
	out, _ := f.FilterToBrowser(wire(Instruction{Opcode: "clipboard", Args: []string{"1", "text/plain"}}))
	if strings.Contains(string(out), "clipboard") {
		t.Fatal("copy must be blocked after the toggle")
	}

	reply = f.HandleControl([]byte(Instruction{Opcode: "", Args: []string{"waas-clipboard", "copy", "1"}}.Encode()))
	if !strings.Contains(string(reply), "1.1;") {
		t.Fatalf("expected copy-on confirmation, got %q", reply)
	}

	// Paste is denied by policy: toggling it on must NOT enable it.
	reply = f.HandleControl([]byte(Instruction{Opcode: "", Args: []string{"waas-clipboard", "paste", "1"}}.Encode()))
	if !strings.Contains(string(reply), "paste") || !strings.Contains(string(reply), "1.0;") {
		t.Fatalf("policy-denied paste must stay off, got %q", reply)
	}
	forward, _, _ := f.FilterToGuacd(wire(Instruction{Opcode: "clipboard", Args: []string{"2", "text/plain"}}))
	if len(forward) != 0 {
		t.Fatal("paste must remain blocked despite the toggle")
	}
}

func TestHandleControlIgnoresUnknownMessages(t *testing.T) {
	f := NewClipboardFilter(true, true)
	if reply := f.HandleControl([]byte("0.,4.ping,13.1751791234567;")); reply != nil {
		t.Fatalf("pings are not clipboard controls, got %q", reply)
	}
	if reply := f.HandleControl([]byte("0.,7.unknown;")); reply != nil {
		t.Fatalf("unknown controls must be swallowed, got %q", reply)
	}
}
