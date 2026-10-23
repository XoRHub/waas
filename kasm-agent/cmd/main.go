// kasm-agent is the WaaS sidecar of kasmvnc workspaces: it serves the
// session user's home directory (list / download / upload) and the
// session's audio (playback and microphone WebSockets, backed by its own
// PulseAudio daemon) to wwt, which gates every request on the connection
// token's grants. It is a separate container the session's occupant
// cannot modify — the governed path for normal use, where everything
// started inside the desktop container is soft against its own occupant
// (docs/kasmvnc.md).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/xorhub/waas/kasm-agent/internal/audio"
	"github.com/xorhub/waas/kasm-agent/internal/basicauth"
	"github.com/xorhub/waas/kasm-agent/internal/files"
	"github.com/xorhub/waas/shared/kasmagent"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	// HOME is the directory served (the desktop's home mount, set by the
	// operator); VNC_PW the one credential accepted. Fail closed on either.
	home := os.Getenv("HOME")
	password := os.Getenv(kasmagent.EnvPassword)
	if home == "" || password == "" {
		slog.Error("HOME and " + kasmagent.EnvPassword + " are required")
		os.Exit(1)
	}
	maxUpload := kasmagent.DefaultMaxUploadBytes
	if v := os.Getenv(kasmagent.EnvMaxUploadBytes); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			slog.Error("invalid "+kasmagent.EnvMaxUploadBytes, "value", v)
			os.Exit(1)
		}
		maxUpload = n
	}

	// os.Root confines every path to the home: "..", absolute paths and
	// symlinks pointing outside cannot escape it.
	root, err := os.OpenRoot(home)
	if err != nil {
		slog.Error("opening home", "home", home, "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	files.Register(mux, root, maxUpload)
	audio.Register(mux, &audio.Handler{Dir: kasmagent.PulseDir, Exec: exec.CommandContext, WriteTimeout: 5 * time.Second})
	// The daemon is supervised for the sidecar's whole life; the routes
	// answer 503 while it is down.
	go (&audio.Pulse{Dir: kasmagent.PulseDir, Exec: exec.CommandContext, Backoff: time.Second}).Run(context.Background())

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", kasmagent.Port),
		Handler:           basicauth.Require(password, mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("kasm-agent listening", "addr", srv.Addr, "home", home, "maxUploadBytes", maxUpload)
	err = srv.ListenAndServe()
	slog.Error("kasm-agent exited", "error", err)
	os.Exit(1)
}
