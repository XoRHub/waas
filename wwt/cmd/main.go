package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/xorhub/waas/wwt/internal/jwks"
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

	handler := proxy.NewHandler(issuer, jwks.NewClient(jwksURL),
		proxy.NewHTTPAPIClient(apiURL, internalToken), guacdAddr)

	mux := http.NewServeMux()
	mux.Handle("/ws", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	slog.Info("wwt listening", "addr", listenAddr, "guacd", guacdAddr, "tls", tlsCert != "")
	var err error
	if tlsCert != "" && tlsKey != "" {
		err = srv.ListenAndServeTLS(tlsCert, tlsKey)
	} else {
		err = srv.ListenAndServe()
	}
	slog.Error("wwt exited", "error", err)
	os.Exit(1)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
