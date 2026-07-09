package guac

// Each blocked clipboard STREAM counts once per direction — the dropped
// blob/end continuations of the same stream must not inflate the counter.

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/xorhub/waas/wwt/internal/metrics"
)

func TestClipboardBlockCountsOncePerStream(t *testing.T) {
	copyCounter := metrics.ClipboardBlocked.WithLabelValues("copy")
	pasteCounter := metrics.ClipboardBlocked.WithLabelValues("paste")
	beforeCopy := testutil.ToFloat64(copyCounter)
	beforePaste := testutil.ToFloat64(pasteCounter)

	f := NewClipboardFilter(false, false)
	if _, err := f.FilterToBrowser(wire(
		Instruction{Opcode: "clipboard", Args: []string{"3", "text/plain"}},
		Instruction{Opcode: "blob", Args: []string{"3", "c2VjcmV0"}},
		Instruction{Opcode: "end", Args: []string{"3"}},
	)); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(copyCounter); got != beforeCopy+1 {
		t.Fatalf("ClipboardBlocked{copy} = %v, want %v", got, beforeCopy+1)
	}

	if _, _, err := f.FilterToGuacd(wire(
		Instruction{Opcode: "clipboard", Args: []string{"7", "text/plain"}},
	)); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(pasteCounter); got != beforePaste+1 {
		t.Fatalf("ClipboardBlocked{paste} = %v, want %v", got, beforePaste+1)
	}
}

func TestClipboardAllowedDoesNotCount(t *testing.T) {
	copyCounter := metrics.ClipboardBlocked.WithLabelValues("copy")
	before := testutil.ToFloat64(copyCounter)

	f := NewClipboardFilter(true, true)
	if _, err := f.FilterToBrowser(wire(
		Instruction{Opcode: "clipboard", Args: []string{"3", "text/plain"}},
	)); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(copyCounter); got != before {
		t.Fatalf("ClipboardBlocked{copy} moved on allowed traffic: %v -> %v", before, got)
	}
}
