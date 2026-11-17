package audio

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/xorhub/waas/shared/kasmvncsidecar"
)

// newServer serves the audio routes with parec replaced by `yes` (an
// endless "y\n" stream), a FIFO of its own in a temp dir and a listener
// on the daemon's socket path. killed receives each time the handler
// tears a parec stand-in down.
func newServer(t *testing.T) (srv *httptest.Server, dir string, killed chan struct{}) {
	t.Helper()
	dir = t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, filepath.Base(kasmvncsidecar.PulseMicFIFO)), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(dir, filepath.Base(kasmvncsidecar.PulseSocket)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	killed = make(chan struct{}, maxStreams+1)
	// A short write timeout: TestAudioDropsAStalledClient waits it out,
	// and every other test reads well within it.
	h := &Handler{Dir: dir, WriteTimeout: time.Second, Exec: func(ctx context.Context, name string, _ ...string) *exec.Cmd {
		if name != "parec" {
			t.Errorf("unexpected command %q", name)
		}
		cmd := exec.CommandContext(ctx, "yes")
		cmd.Cancel = func() error {
			select {
			case killed <- struct{}{}:
			default:
			}
			return cmd.Process.Kill()
		}
		return cmd
	}}
	mux := http.NewServeMux()
	Register(mux, h)
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, dir, killed
}

func dial(t *testing.T, srv *httptest.Server, path string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	return websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+path, nil)
}

// /audio frames parec's stream into 20 ms binary messages and kills
// parec once the socket is gone.
func TestAudioFramesAndTearsDown(t *testing.T) {
	srv, _, killed := newServer(t)
	ws, _, err := dial(t, srv, kasmvncsidecar.PathAudio)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		mt, msg, err := ws.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if mt != websocket.BinaryMessage || len(msg) != frameBytes || msg[0] != 'y' {
			t.Fatalf("expected a %d-byte binary frame of the stream, got type %d, %d bytes", frameBytes, mt, len(msg))
		}
	}
	ws.Close()
	select {
	case <-killed:
	case <-time.After(3 * time.Second):
		t.Fatal("parec must be killed once the socket closes")
	}
}

// /mic writes the frames into the FIFO, one writer at a time: a second
// connection is refused with 409 until the first is gone.
func TestMicWritesTheFIFOSingleWriter(t *testing.T) {
	srv, dir, _ := newServer(t)
	// The daemon's role: hold the FIFO open for reading.
	fifo, err := os.OpenFile(filepath.Join(dir, filepath.Base(kasmvncsidecar.PulseMicFIFO)), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fifo.Close()

	ws, _, err := dial(t, srv, kasmvncsidecar.PathMic)
	if err != nil {
		t.Fatal(err)
	}
	frame := []byte(strings.Repeat("ab", 960))
	if err := ws.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte("ignored")); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 0, 2*len(frame))
	buf := make([]byte, 4096)
	for deadline := time.Now().Add(3 * time.Second); len(got) < 2*len(frame) && time.Now().Before(deadline); {
		n, err := fifo.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if string(got) != string(frame)+string(frame) {
		t.Fatalf("expected both binary frames in the FIFO, got %d bytes", len(got))
	}

	if _, resp, err := dial(t, srv, kasmvncsidecar.PathMic); err == nil || resp == nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second microphone connection must be refused with 409, got err=%v resp=%v", err, resp)
	}
	ws.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ws2, _, err := dial(t, srv, kasmvncsidecar.PathMic)
		if err == nil {
			ws2.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the microphone must be free again once the first connection is closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Without the daemon (no FIFO reader) /mic answers 503 instead of
// blocking on the open.
func TestMicAnswers503WithoutTheDaemon(t *testing.T) {
	srv, _, _ := newServer(t)
	if _, resp, err := dial(t, srv, kasmvncsidecar.PathMic); err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got err=%v resp=%v", err, resp)
	}
}

// The supervisor restarts the daemon when it exits, clears a stale FIFO
// first, and passes the whole configuration on the command line with
// module loading and exit locked: nothing under the emptyDir (which the
// desktop can write) is read as a script.
func TestPulseRestartsTheLockedDaemon(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "mic.fifo")
	if err := os.WriteFile(fifo, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var starts atomic.Int32
	var args []string
	p := &Pulse{Dir: dir, Backoff: time.Millisecond, Exec: func(ctx context.Context, name string, a ...string) *exec.Cmd {
		if starts.Add(1) == 1 {
			args = a
			if _, err := os.Stat(fifo); !os.IsNotExist(err) {
				t.Errorf("a stale FIFO must be removed before the daemon starts, stat: %v", err)
			}
		}
		if name != "pulseaudio" {
			t.Errorf("unexpected command %q", name)
		}
		return exec.CommandContext(ctx, "true")
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	p.Run(ctx)
	if starts.Load() < 2 {
		t.Fatalf("expected the daemon to be restarted, got %d start(s)", starts.Load())
	}
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"--disallow-module-loading", "--disallow-exit", "-n",
		"module-native-protocol-unix socket=" + filepath.Join(dir, "native") + " auth-anonymous=1",
		"module-null-sink sink_name=waas_out format=s16le rate=48000 channels=2",
		"module-pipe-source source_name=waas_mic file=" + fifo + " format=s16le rate=48000 channels=1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("daemon args lack %q: %q", want, args)
		}
	}
	for _, a := range args {
		if a == "-F" || a == "--file" || strings.HasPrefix(a, "--file=") {
			t.Errorf("the daemon must not read a script: %q", args)
		}
	}
}

// Without the daemon's socket /audio answers 503 before upgrading.
func TestAudioAnswers503WithoutTheDaemon(t *testing.T) {
	srv, dir, _ := newServer(t)
	if err := os.Remove(filepath.Join(dir, "native")); err != nil {
		t.Fatal(err)
	}
	if _, resp, err := dial(t, srv, kasmvncsidecar.PathAudio); err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got err=%v resp=%v", err, resp)
	}
}

// /audio holds at most maxStreams connections: one more is 429, and a
// slot frees once a connection is gone.
func TestAudioCapsTheStreams(t *testing.T) {
	srv, _, killed := newServer(t)
	conns := make([]*websocket.Conn, maxStreams)
	for i := range conns {
		ws, _, err := dial(t, srv, kasmvncsidecar.PathAudio)
		if err != nil {
			t.Fatal(err)
		}
		defer ws.Close()
		conns[i] = ws
	}
	if _, resp, err := dial(t, srv, kasmvncsidecar.PathAudio); err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 past %d streams, got err=%v resp=%v", maxStreams, err, resp)
	}
	conns[0].Close()
	<-killed
	deadline := time.Now().Add(3 * time.Second)
	for {
		ws, _, err := dial(t, srv, kasmvncsidecar.PathAudio)
		if err == nil {
			ws.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a slot must free once a stream is gone: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A client that stops reading is dropped after WriteTimeout, and its
// parec with it.
func TestAudioDropsAStalledClient(t *testing.T) {
	srv, _, killed := newServer(t)
	ws, _, err := dial(t, srv, kasmvncsidecar.PathAudio)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	select {
	case <-killed:
	case <-time.After(10 * time.Second):
		t.Fatal("a client that stopped reading must be dropped")
	}
}

// /mic skips frames of odd length and closes the socket on a frame past
// the read limit.
func TestMicRejectsOddAndOversizedFrames(t *testing.T) {
	srv, dir, _ := newServer(t)
	fifo, err := os.OpenFile(filepath.Join(dir, filepath.Base(kasmvncsidecar.PulseMicFIFO)), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fifo.Close()
	ws, _, err := dial(t, srv, kasmvncsidecar.PathMic)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	for _, frame := range []string{"odd", "even"} {
		if err := ws.WriteMessage(websocket.BinaryMessage, []byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	buf := make([]byte, 16)
	var got []byte
	for deadline := time.Now().Add(3 * time.Second); len(got) < 4 && time.Now().Before(deadline); {
		n, _ := fifo.Read(buf)
		got = append(got, buf[:max(n, 0)]...)
		time.Sleep(5 * time.Millisecond)
	}
	if string(got) != "even" {
		t.Fatalf("expected only the even frame in the FIFO, got %q", got)
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, make([]byte, micReadLimit+2)); err != nil {
		t.Fatal(err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := ws.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("an oversized frame must close the socket (1009), got %v", err)
	}
}
