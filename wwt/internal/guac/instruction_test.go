package guac

import (
	"bufio"
	"fmt"
	"strings"
	"testing"
)

// TestReadInstructionRejectsIllegalLength verifies the length guard: an
// attacker-controlled element length must never panic make() (negative) or
// force a huge allocation (unbounded). ReadInstruction must return an error
// instead — the point of the test is the absence of a panic.
func TestReadInstructionRejectsIllegalLength(t *testing.T) {
	cases := []struct {
		name string
		wire string
	}{
		{"negative one", "-1.x,"},
		{"negative multi digit", "-5.abc;"},
		{"just above cap", fmt.Sprintf("%d.x,", MaxPendingBytes+1)},
		{"multi gigabyte", "2000000000.x,"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A panic would abort this subtest, so reaching the assertion
			// at all already proves make() was never called with a bad cap.
			_, err := ReadInstruction(bufio.NewReader(strings.NewReader(tc.wire)))
			if err == nil {
				t.Fatalf("expected an error for %q, got nil", tc.wire)
			}
		})
	}
}

// TestReadInstructionAcceptsLegalLengths is the regression guard: every valid
// length (including a 0-length empty element and multi-byte runes) must still
// parse exactly as before the length guard was added.
func TestReadInstructionAcceptsLegalLengths(t *testing.T) {
	cases := []struct {
		name string
		inst Instruction
	}{
		{"empty element", Instruction{Opcode: "", Args: []string{"ping"}}},
		{"plain", Instruction{Opcode: "connect", Args: []string{"host", "5901"}}},
		{"multi byte runes", Instruction{Opcode: "name", Args: []string{"pöstgrés"}}},
		{"at cap boundary", Instruction{Opcode: "blob", Args: []string{"0", strings.Repeat("a", 8)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := tc.inst.Encode()
			decoded, err := ReadInstruction(bufio.NewReader(strings.NewReader(encoded)))
			if err != nil {
				t.Fatalf("ReadInstruction(%q): %v", encoded, err)
			}
			if decoded.Encode() != encoded {
				t.Fatalf("round trip mismatch: got %q want %q", decoded.Encode(), encoded)
			}
		})
	}
}
