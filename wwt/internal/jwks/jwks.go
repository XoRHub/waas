// Package jwks fetches and caches the API server's public signing keys so
// the proxy can validate connection tokens without sharing any secret.
package jwks

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// Client caches the key set and refreshes it when an unknown key ID shows
// up (key rotation) or the cache expires.
type Client struct {
	url        string
	httpClient *http.Client

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

const cacheTTL = 5 * time.Minute

func NewClient(url string) *Client {
	return &Client{
		url:        url,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		keys:       map[string]*rsa.PublicKey{},
	}
}

// Key returns the RSA public key for the given key ID.
func (c *Client) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if key, ok := c.keys[kid]; ok && time.Since(c.fetched) < cacheTTL {
		return key, nil
	}
	if err := c.refreshLocked(ctx); err != nil {
		// Serve a stale key rather than dropping connections on a blip.
		if key, ok := c.keys[kid]; ok {
			return key, nil
		}
		return nil, err
	}
	key, ok := c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("no key with id %q in JWKS", kid)
	}
	return key, nil
}

func (c *Client) refreshLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("building JWKS request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching JWKS: unexpected status %d", resp.StatusCode)
	}

	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decoding JWKS: %w", err)
	}

	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return fmt.Errorf("decoding JWKS modulus: %w", err)
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return fmt.Errorf("decoding JWKS exponent: %w", err)
		}
		keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(n),
			E: int(new(big.Int).SetBytes(e).Int64()),
		}
	}
	c.keys = keys
	c.fetched = time.Now()
	return nil
}
