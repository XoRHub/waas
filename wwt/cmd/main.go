package main

import (
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/xorhub/waas/wwt/internal/jwks"
	"github.com/xorhub/waas/wwt/internal/kasm"
	"github.com/xorhub/waas/wwt/internal/proxy"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	listenAddr := envOr("WWT_LISTEN_ADDR", ":8081")
	guacdAddr := envOr("WWT_GUACD_ADDR", "waas-guacd:4822")
	apiURL := envOr("WWT_API_URL", "http://waas-api-server:8080")
	jwksURL := envOr("WWT_JWKS_URL", apiURL+"/.well-known/jwks.json")
	issuer := envOr("WWT_JWT_ISSUER", "waas")
	internalToken := os.Getenv("WWT_INTERNAL_TOKEN")
	tlsCert := os.Getenv("WWT_TLS_CERT_FILE")
	tlsKey := os.Getenv("WWT_TLS_KEY_FILE")

	if internalToken == "" {
		slog.Error("WWT_INTERNAL_TOKEN is required")
		os.Exit(1)
	}

	keys := jwks.NewClient(jwksURL)
	apiClient := proxy.NewHTTPAPIClient(apiURL, internalToken)
	handler := proxy.NewHandler(issuer, keys, apiClient, guacdAddr)

	mux := http.NewServeMux()
	mux.Handle("/ws", handler)
	// KasmVNC sessions: the whole client web app (assets + /websockify)
	// is reverse-proxied under /kasm/{sessionID}/ — see internal/kasm.
	mux.Handle("/kasm/", kasm.NewHandler(issuer, keys, apiClient))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	// Prometheus scrape endpoint, opt-in and cluster-internal only: the
	// ingress/httproute allow-lists never expose it (do not add it there).
	metricsEnabled := os.Getenv("WWT_METRICS_ENABLED") == "true"
	if metricsEnabled {
		mux.Handle("/metrics", promhttp.Handler())
	}

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		slog.Error("wwt cannot listen", "addr", listenAddr, "error", err)
		os.Exit(1)
	}
	slog.Info("wwt listening", "addr", listenAddr, "guacd", guacdAddr, "tls", tlsCert != "", "metrics", metricsEnabled)
	err = serve(srv, ln, tlsCert, tlsKey)
	slog.Error("wwt exited", "error", err)
	os.Exit(1)
}

// serve runs the server on ln, with TLS when BOTH cert and key are set
// (an unreadable pair fails loudly instead of silently serving plain
// HTTP). Split from main so the branch is testable.
func serve(srv *http.Server, ln net.Listener, certFile, keyFile string) error {
	if certFile != "" && keyFile != "" {
		return srv.ServeTLS(ln, certFile, keyFile)
	}
	return srv.Serve(ln)
}

// envOr is knowingly duplicated with api-server's config (6 lines across
// a Go module boundary — a shared package would cost more than the copy).
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
