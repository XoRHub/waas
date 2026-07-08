package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// selfSignedCert writes a throwaway localhost certificate pair and
// returns the file paths.
func selfSignedCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "wwt-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "tls.crt")
	keyFile = filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certFile,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func newTestServer(t *testing.T) (*http.Server, net.Listener, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { srv.Close() })
	return srv, ln, ln.Addr().String()
}

// The WWT_TLS_* path was never exercised: serve must actually terminate
// TLS when both files are set.
func TestServeTLS(t *testing.T) {
	certFile, keyFile := selfSignedCert(t)
	srv, ln, addr := newTestServer(t)
	go func() { _ = serve(srv, ln, certFile, keyFile) }()

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, // #nosec G402 — self-signed test cert
		Timeout:   5 * time.Second,
	}
	resp, err := client.Get(fmt.Sprintf("https://%s/healthz", addr))
	if err != nil {
		t.Fatalf("TLS request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.TLS == nil {
		t.Fatalf("expected a 200 over TLS, got %d (tls=%v)", resp.StatusCode, resp.TLS != nil)
	}

	// Plain HTTP against the TLS listener must NOT be served: Go's TLS
	// server answers a plaintext 400 ("plain HTTP request was sent to
	// HTTPS port") — anything but a 200 proves the branch picked TLS.
	plain := &http.Client{Timeout: 2 * time.Second}
	if resp, err := plain.Get(fmt.Sprintf("http://%s/healthz", addr)); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("plain HTTP must not be served when TLS is configured")
		}
	}
}

func TestServePlainWhenTLSUnset(t *testing.T) {
	srv, ln, addr := newTestServer(t)
	go func() { _ = serve(srv, ln, "", "") }()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/healthz", addr))
	if err != nil {
		t.Fatalf("plain request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// An unreadable certificate pair must fail loudly, never fall back to
// plain HTTP.
func TestServeFailsLoudlyOnBadCert(t *testing.T) {
	srv, ln, _ := newTestServer(t)
	errCh := make(chan error, 1)
	go func() { errCh <- serve(srv, ln, "/nonexistent.crt", "/nonexistent.key") }()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected a certificate loading error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve must return promptly on an unreadable certificate")
	}
}
