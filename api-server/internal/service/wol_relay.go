package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HTTPWoLRelay drives an external Wake-on-LAN relay over HTTP. A pod on
// the cluster network cannot broadcast a magic packet onto the target's
// physical L2, so the relay (a device or agent on that LAN) does it. The
// api-server POSTs {"mac": "aa:bb:cc:dd:ee:ff"} to RelayURL.
type HTTPWoLRelay struct {
	URL   string
	Token string
	// Client is overridable in tests; defaults to a 10s-timeout client.
	Client *http.Client
}

// NewHTTPWoLRelay builds a relay client, or nil when no URL is set.
func NewHTTPWoLRelay(url, token string) *HTTPWoLRelay {
	if url == "" {
		return nil
	}
	return &HTTPWoLRelay{URL: url, Token: token, Client: &http.Client{Timeout: 10 * time.Second}}
}

// Wake sends the magic-packet request to the relay.
func (r *HTTPWoLRelay) Wake(ctx context.Context, mac string) error {
	body, _ := json.Marshal(map[string]string{"mac": mac})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("relay returned status %d", resp.StatusCode)
	}
	return nil
}
