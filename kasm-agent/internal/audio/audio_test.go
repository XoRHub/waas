package audio

import (
	"context"
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

	"github.com/xorhub/waas/shared/kasmagent"
)

// newServer serves the audio routes with parec replaced by `yes` (an
// endless "y\n" stream) and a FIFO of its own in a temp dir. killed is
// closed when the handler tears the parec stand-in down.
func newServer(t *testing.T) (srv *httptest.Server, dir string, killed chan struct{}) {
	t.Helper()
	dir = t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, filepath.Base(kasmagent.PulseMicFIFO)), 0o600); err != nil {
		t.Fatal(err)
	}
	killed = make(chan struct{})
	h := &Handler{Dir: dir, Exec: func(ctx context.Context, name string, _ ...string) *exec.Cmd {
		if name != "parec" {
			t.Errorf("unexpected command %q", name)
		}
		cmd := exec.CommandContext(ctx, "yes")
		cmd.Cancel = func() error {
			close(killed)
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
	ws, _, err := dial(t, srv, kasmagent.PathAudio)
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
	fifo, err := os.OpenFile(filepath.Join(dir, filepath.Base(kasmagent.PulseMicFIFO)), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fifo.Close()

	ws, _, err := dial(t, srv, kasmagent.PathMic)
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

	if _, resp, err := dial(t, srv, kasmagent.PathMic); err == nil || resp == nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second microphone connection must be refused with 409, got err=%v resp=%v", err, resp)
	}
	ws.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ws2, _, err := dial(t, srv, kasmagent.PathMic)
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
	if _, resp, err := dial(t, srv, kasmagent.PathMic); err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got err=%v resp=%v", err, resp)
	}
}

// The supervisor writes the daemon script and restarts the daemon when
// it exits.
func TestPulseRestartsTheDaemon(t *testing.T) {
	dir := t.TempDir()
	var starts atomic.Int32
	p := &Pulse{Dir: dir, Backoff: time.Millisecond, Exec: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		starts.Add(1)
		if name != "pulseaudio" || args[len(args)-1] != filepath.Join(dir, "default.pa") {
			t.Errorf("unexpected command %q %v", name, args)
		}
		return exec.CommandContext(ctx, "true")
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	p.Run(ctx)
	if starts.Load() < 2 {
		t.Fatalf("expected the daemon to be restarted, got %d start(s)", starts.Load())
	}
	script, err := os.ReadFile(filepath.Join(dir, "default.pa"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"module-native-protocol-unix socket=" + filepath.Join(dir, "native") + " auth-anonymous=1",
		"module-null-sink sink_name=waas_out format=s16le rate=48000 channels=2",
		"module-pipe-source source_name=waas_mic file=" + filepath.Join(dir, "mic.fifo") + " format=s16le rate=48000 channels=1",
		"set-default-sink waas_out", "set-default-source waas_mic",
	} {
		if !strings.Contains(string(script), want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
}
