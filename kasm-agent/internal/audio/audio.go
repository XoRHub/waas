// Package audio serves the session's sound to the portal and the
// portal's microphone to the session, through one PulseAudio daemon the
// sidecar supervises itself (the kasmweb images' own audio services are
// closed platform binaries the operator keeps off — docs/kasmvnc.md).
//
// Topology: the daemon owns a null sink (the desktop's default output —
// its monitor is what /audio streams) and a pipe source (the desktop's
// default input — /mic writes into its FIFO). Desktop clients reach the
// daemon over the native socket in the emptyDir both containers mount.
// Auth is the caller's (basicauth, in main).
package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"github.com/xorhub/waas/shared/kasmagent"
)

// Exec builds one child process; the default is exec.CommandContext, a
// test injects a stand-in.
type Exec func(ctx context.Context, name string, args ...string) *exec.Cmd

// Pulse supervises the PulseAudio daemon: one child, restarted with
// backoff when it dies, for the sidecar's whole life.
type Pulse struct {
	// Dir is kasmagent.PulseDir: the socket, FIFO, runtime and state
	// paths are all under it (the emptyDir is root-owned and world-writable, and
	// PulseAudio insists on owning its runtime directory, so it gets a
	// subdirectory it creates itself).
	Dir  string
	Exec Exec
	// Backoff is the first restart delay; it doubles up to 32x.
	Backoff time.Duration
}

// args is the whole daemon configuration, on the command line rather
// than in a script: the emptyDir is writable by the desktop (same uid),
// so a script there would be the occupant's to rewrite before the next
// restart. Nothing else is loaded (-n) and nothing can be later — module
// loading and exit are refused to every client of the anonymous socket,
// which leaves the occupant only their own streams to re-route or mute.
// No cookie dance (same uid on both sides, auth-anonymous); no
// set-default-*: the only sink and the only non-monitor source are what
// PulseAudio picks as defaults anyway.
func (p *Pulse) args() []string {
	return []string{
		"--daemonize=no", "--exit-idle-time=-1", "--use-pid-file=no",
		// No rtkit, no D-Bus in the sidecar: don't try — and don't log
		// every failed D-Bus lookup at warning level either.
		"--realtime=no", "--high-priority=no",
		"--log-target=stderr", "--log-level=error",
		"--disallow-module-loading", "--disallow-exit", "-n",
		"-L", fmt.Sprintf("module-native-protocol-unix socket=%s auth-anonymous=1 auth-cookie-enabled=0", socketPath(p.Dir)),
		"-L", fmt.Sprintf("module-null-sink sink_name=%s format=s16le rate=%d channels=%d",
			kasmagent.PulseSink, kasmagent.AudioRate, kasmagent.AudioChannels),
		"-L", fmt.Sprintf("module-pipe-source source_name=%s file=%s format=s16le rate=%d channels=%d",
			kasmagent.PulseSource, fifoPath(p.Dir), kasmagent.AudioRate, kasmagent.MicChannels),
	}
}

// Run keeps the daemon up until ctx ends.
func (p *Pulse) Run(ctx context.Context) {
	delay := p.Backoff
	for ctx.Err() == nil {
		started := time.Now()
		// module-pipe-source refuses an existing FIFO, and a daemon that
		// crashed never unlinked its own.
		_ = os.Remove(fifoPath(p.Dir))
		cmd := p.Exec(ctx, "pulseaudio", p.args()...)
		cmd.Env = append(os.Environ(),
			"PULSE_RUNTIME_PATH="+filepath.Join(p.Dir, "runtime"),
			"PULSE_STATE_PATH="+filepath.Join(p.Dir, "state"))
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		err := cmd.Run()
		if ctx.Err() != nil {
			return
		}
		slog.Error("pulseaudio exited", "error", err, "uptime", time.Since(started).Round(time.Millisecond), "restartIn", delay)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
		// A daemon that ran a while earned a fresh backoff.
		if time.Since(started) > 32*p.Backoff {
			delay = p.Backoff
		} else {
			delay = min(2*delay, 32*p.Backoff)
		}
	}
}

// Handler serves the two WebSocket routes.
type Handler struct {
	Dir  string
	Exec Exec
	// WriteTimeout drops an /audio client that stopped reading instead of
	// leaving its parec blocked on a full socket forever.
	WriteTimeout time.Duration
	// micMu is the single-writer rule on the FIFO: a second /mic
	// connection is refused (409) while one is open. Simplest rule that
	// keeps one stream's frames from interleaving with another's; the
	// portal only ever opens one.
	micMu sync.Mutex
	// streams counts the open /audio connections (maxStreams).
	streams atomic.Int32
}

// Register mounts /audio and /mic on mux.
func Register(mux *http.ServeMux, h *Handler) {
	mux.HandleFunc("GET "+kasmagent.PathAudio, h.audio)
	mux.HandleFunc("GET "+kasmagent.PathMic, h.mic)
}

// upgrader: no origin check here — wwt is the only caller, after its
// own JWT and policy gates, and it is wwt that refuses a cross-origin
// upgrade (the browser's Origin is relayed as is).
var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

const (
	frameBytes = kasmagent.AudioRate * kasmagent.AudioFrameMillis / 1000 * kasmagent.AudioChannels * 2
	// maxStreams bounds the parec processes one sidecar runs: the portal
	// opens one per tab, a handful of tabs is plenty.
	maxStreams = 4
	// micReadLimit: a 20 ms mono frame is 1920 B; anything far bigger is
	// not the portal, and staying under PIPE_BUF (4096 on Linux) keeps
	// every FIFO write all-or-nothing.
	micReadLimit = 4096
)

// audio streams the null sink's monitor: one parec per connection,
// killed when the socket closes (or the daemon dies, which ends parec and
// so the socket — the portal shows "closed" and the user toggles again).
func (h *Handler) audio(w http.ResponseWriter, r *http.Request) {
	if h.streams.Add(1) > maxStreams {
		h.streams.Add(-1)
		http.Error(w, "too many audio streams are open", http.StatusTooManyRequests)
		return
	}
	defer h.streams.Add(-1)
	// parec would start, fail to connect and end a socket already
	// upgraded; dialing first turns a down daemon into a plain 503.
	c, err := net.Dial("unix", socketPath(h.Dir))
	if err != nil {
		slog.Error("reaching pulseaudio", "error", err)
		http.Error(w, "audio is unavailable", http.StatusServiceUnavailable)
		return
	}
	_ = c.Close()
	ctx, cancel := context.WithCancel(r.Context())
	cmd := h.Exec(ctx, "parec",
		"--device="+kasmagent.PulseSink+".monitor", "--raw", "--format=s16le",
		"--rate="+strconv.Itoa(kasmagent.AudioRate), "--channels="+strconv.Itoa(kasmagent.AudioChannels),
		"--latency-msec="+strconv.Itoa(kasmagent.AudioFrameMillis))
	cmd.Env = h.clientEnv()
	cmd.Stderr = os.Stderr
	// Kill leaves the pipe to an orphan open at worst; don't wait on it.
	cmd.WaitDelay = time.Second
	out, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		cancel()
		slog.Error("starting parec", "error", err)
		http.Error(w, "audio is unavailable", http.StatusServiceUnavailable)
		return
	}
	// Runs after the socket's own deferred Close: kill, then reap.
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already answered
	}
	defer ws.Close()
	// The browser sends nothing; reading is how its close is noticed.
	go func() {
		for {
			if _, _, err := ws.NextReader(); err != nil {
				cancel()
				return
			}
		}
	}()
	buf := make([]byte, frameBytes)
	for {
		if _, err := io.ReadFull(out, buf); err != nil {
			return
		}
		_ = ws.SetWriteDeadline(time.Now().Add(h.WriteTimeout))
		if err := ws.WriteMessage(websocket.BinaryMessage, buf); err != nil {
			return
		}
	}
}

// mic writes the browser's frames into the pipe source's FIFO.
func (h *Handler) mic(w http.ResponseWriter, r *http.Request) {
	if !h.micMu.TryLock() {
		http.Error(w, "the microphone is already in use by another connection", http.StatusConflict)
		return
	}
	defer h.micMu.Unlock()
	// O_NONBLOCK: the daemon holds the FIFO open for reading and writing,
	// so the open succeeds at once while it is up — and fails with ENXIO
	// instead of blocking forever while it is not. Go registers the
	// descriptor with its poller, so the write deadline below works.
	f, err := os.OpenFile(fifoPath(h.Dir), os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		slog.Error("opening the microphone FIFO", "error", err)
		http.Error(w, "the microphone is unavailable", http.StatusServiceUnavailable)
		return
	}
	defer f.Close() //nolint:errcheck // every write already reported its own error
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(micReadLimit)
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		// An odd length would shift every later sample by one byte.
		if mt != websocket.BinaryMessage || len(data)%2 != 0 {
			continue
		}
		// The daemon drains the FIFO at real time: a browser running ahead
		// of it fills the pipe, and its frame is dropped rather than the
		// socket stalled. Frames are under PIPE_BUF (micReadLimit), so the
		// write is all-or-nothing: no half frame ever skews the stream.
		_ = f.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		if _, err := f.Write(data); err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			slog.Error("writing the microphone FIFO", "error", err)
			return
		}
	}
}

// clientEnv points parec at the sidecar's own daemon and keeps its
// client cookie out of the session home.
func (h *Handler) clientEnv() []string {
	return append(os.Environ(),
		kasmagent.EnvPulseServer+"=unix:"+socketPath(h.Dir),
		"PULSE_COOKIE="+filepath.Join(h.Dir, "client-cookie"))
}

// The socket and FIFO names are the contract's (kasmagent.PulseSocket,
// kasmagent.PulseMicFIFO), under whatever directory the test chose.
func socketPath(dir string) string { return filepath.Join(dir, filepath.Base(kasmagent.PulseSocket)) }
func fifoPath(dir string) string   { return filepath.Join(dir, filepath.Base(kasmagent.PulseMicFIFO)) }
