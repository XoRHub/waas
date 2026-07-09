package guac

import (
	"bufio"
	"bytes"
	"strings"
	"sync"

	"github.com/xorhub/waas/wwt/internal/metrics"
)

// ClipboardFilter enforces the connection token's clipboard grant on the
// live instruction stream, in both directions:
//
//	copy  = remote → local  ("clipboard" streams sent by guacd)
//	paste = local  → remote ("clipboard" streams sent by the browser)
//
// The grant is the hard bound from the user's WorkspacePolicy; on top of
// it the user may toggle either direction off and on again mid-session
// (the overlay's live toggles) — but never beyond the grant.
//
// Blocking a direction means dropping the "clipboard" instruction AND the
// "blob"/"end" instructions of that stream (client and server allocate
// stream indices independently, so the two directions track their own
// index sets). A blocked paste stream is answered with an error "ack" so
// the browser-side writer aborts instead of waiting forever.
type ClipboardFilter struct {
	mu sync.Mutex

	grantCopy, grantPaste bool
	wantCopy, wantPaste   bool

	blockedServer map[string]bool // guacd→browser stream indices being dropped
	blockedClient map[string]bool // browser→guacd stream indices being dropped
}

// NewClipboardFilter builds a filter from the token grant; user toggles
// start at the grant.
func NewClipboardFilter(allowCopy, allowPaste bool) *ClipboardFilter {
	return &ClipboardFilter{
		grantCopy: allowCopy, grantPaste: allowPaste,
		wantCopy: allowCopy, wantPaste: allowPaste,
		blockedServer: map[string]bool{},
		blockedClient: map[string]bool{},
	}
}

// SetCopy applies a user toggle, clamped to the grant, and reports the
// effective state.
func (f *ClipboardFilter) SetCopy(enabled bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wantCopy = enabled && f.grantCopy
	return f.wantCopy
}

// SetPaste applies a user toggle, clamped to the grant, and reports the
// effective state.
func (f *ClipboardFilter) SetPaste(enabled bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wantPaste = enabled && f.grantPaste
	return f.wantPaste
}

func (f *ClipboardFilter) copyBlocked() bool  { return !f.wantCopy }
func (f *ClipboardFilter) pasteBlocked() bool { return !f.wantPaste }

// FilterToBrowser rewrites one complete frame (guacd → browser), dropping
// blocked clipboard streams. Fast path: when copy is allowed and no
// stream is mid-drop, the frame passes through untouched.
func (f *ClipboardFilter) FilterToBrowser(frame []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.copyBlocked() && len(f.blockedServer) == 0 {
		return frame, nil
	}
	return f.rewrite(frame, "server")
}

// FilterToGuacd inspects one browser message (already known not to be
// tunnel-internal). It returns the bytes to forward to guacd (nil = drop)
// and an optional reply for the browser (the error ack of a refused
// clipboard stream).
func (f *ClipboardFilter) FilterToGuacd(message []byte) (forward, reply []byte, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.pasteBlocked() && len(f.blockedClient) == 0 {
		return message, nil, nil
	}
	kept, replies, err := f.rewriteWithReplies(message, "client")
	if err != nil {
		return nil, nil, err
	}
	return kept, replies, nil
}

// rewrite drops blocked-stream instructions from a frame (one direction).
func (f *ClipboardFilter) rewrite(frame []byte, side string) ([]byte, error) {
	kept, _, err := f.rewriteWithReplies(frame, side)
	return kept, err
}

// rewriteWithReplies walks the instructions of a complete frame, keeping
// or dropping them, and synthesizes error acks for refused client
// clipboard streams. Frames arrive pre-validated by the Framer, so a
// parse error here is a real protocol violation.
func (f *ClipboardFilter) rewriteWithReplies(frame []byte, side string) (kept []byte, replies []byte, err error) {
	blocked := f.blockedServer
	directionBlocked := f.copyBlocked()
	if side == "client" {
		blocked = f.blockedClient
		directionBlocked = f.pasteBlocked()
	}

	var out strings.Builder
	var reply strings.Builder
	r := bufio.NewReader(bytes.NewReader(frame))
	for {
		if _, peekErr := r.Peek(1); peekErr != nil {
			break // end of frame
		}
		inst, err := ReadInstruction(r)
		if err != nil {
			return nil, nil, err
		}
		switch inst.Opcode {
		case "clipboard":
			if directionBlocked && len(inst.Args) > 0 {
				blocked[inst.Args[0]] = true
				// One increment per blocked STREAM (this instruction opens
				// it); the dropped blob/end continuations don't recount.
				direction := "copy"
				if side == "client" {
					direction = "paste"
				}
				metrics.ClipboardBlocked.WithLabelValues(direction).Inc()
				if side == "client" {
					// 0x0303 CLIENT_FORBIDDEN: the writer aborts cleanly
					// instead of waiting for an ack that will never come.
					reply.WriteString(Instruction{Opcode: "ack",
						Args: []string{inst.Args[0], "clipboard disabled by policy", "771"}}.Encode())
				}
				continue
			}
		case "blob":
			if len(inst.Args) > 0 && blocked[inst.Args[0]] {
				continue
			}
		case "end":
			if len(inst.Args) > 0 && blocked[inst.Args[0]] {
				delete(blocked, inst.Args[0])
				continue
			}
		case "ack":
			// guacd acks a client stream we already refused ourselves:
			// swallow it (the browser saw our error ack).
			if side == "server" && len(inst.Args) > 0 && f.blockedClient[inst.Args[0]] {
				continue
			}
		}
		out.WriteString(inst.Encode())
	}
	if reply.Len() == 0 {
		return []byte(out.String()), nil, nil
	}
	return []byte(out.String()), []byte(reply.String()), nil
}

// ControlMessage is a WaaS tunnel-internal control sent by the frontend
// overlay (zero-length opcode, first arg "waas-clipboard").
const controlClipboard = "waas-clipboard"

// HandleControl processes a tunnel-internal message. It returns the reply
// to send back to the browser ("" = no reply, message unrecognized).
// Recognized controls:
//
//	0.,14.waas-clipboard,4.copy,1.<0|1>;   toggle remote→local
//	0.,14.waas-clipboard,5.paste,1.<0|1>;  toggle local→remote
//
// The reply mirrors the EFFECTIVE state (clamped to the policy grant), so
// the overlay always displays what is actually enforced.
func (f *ClipboardFilter) HandleControl(message []byte) []byte {
	inst, err := ReadInstruction(bufio.NewReader(bytes.NewReader(message)))
	if err != nil || inst.Opcode != "" || len(inst.Args) < 3 || inst.Args[0] != controlClipboard {
		return nil
	}
	enable := inst.Args[2] == "1"
	var effective bool
	switch inst.Args[1] {
	case "copy":
		effective = f.SetCopy(enable)
	case "paste":
		effective = f.SetPaste(enable)
	default:
		return nil
	}
	state := "0"
	if effective {
		state = "1"
	}
	return []byte(Instruction{Opcode: "", Args: []string{controlClipboard, inst.Args[1], state}}.Encode())
}
