package jwks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xorhub/waas/shared/auth"
)

// jwksServer serves the given signer's JWKS document and counts hits;
// failing switches it to a 500 without dropping the previously served
// keys (the stale-cache scenario).
type jwksServer struct {
	*httptest.Server
	hits    atomic.Int64
	failing atomic.Bool
	signer  *auth.Signer
}

func newJWKSServer(t *testing.T) *jwksServer {
	t.Helper()
	signer, err := auth.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	s := &jwksServer{signer: signer}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.hits.Add(1)
		if s.failing.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(s.signer.JWKS())
	}))
	t.Cleanup(s.Close)
	return s
}

func TestKeyFetchesAndCaches(t *testing.T) {
	srv := newJWKSServer(t)
	c := NewClient(srv.URL)

	key, err := c.Key(context.Background(), srv.signer.KeyID())
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if key.N.Cmp(srv.signer.Public().N) != 0 {
		t.Fatal("returned key does not match the published one")
	}
	// Within the TTL the cached key is served without a new request.
	if _, err := c.Key(context.Background(), srv.signer.KeyID()); err != nil {
		t.Fatalf("Key (cached): %v", err)
	}
	if got := srv.hits.Load(); got != 1 {
		t.Fatalf("expected 1 fetch, the second call must hit the cache; got %d", got)
	}
}

func TestKeyRefreshesOnUnknownKid(t *testing.T) {
	srv := newJWKSServer(t)
	c := NewClient(srv.URL)

	if _, err := c.Key(context.Background(), srv.signer.KeyID()); err != nil {
		t.Fatalf("Key: %v", err)
	}

	// Key rotation: the server now publishes a new key; asking for its
	// kid must trigger a refresh even though the cache is fresh.
	rotated, err := auth.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	srv.signer = rotated
	key, err := c.Key(context.Background(), rotated.KeyID())
	if err != nil {
		t.Fatalf("Key after rotation: %v", err)
	}
	if key.N.Cmp(rotated.Public().N) != 0 {
		t.Fatal("rotation did not surface the new key")
	}
	if got := srv.hits.Load(); got != 2 {
		t.Fatalf("expected exactly 2 fetches, got %d", got)
	}
}

// A refresh failure must serve the stale key rather than dropping live
// connections on a blip — deliberate behaviour, easy to lose in a
// refactor.
func TestKeyServesStaleOnRefreshFailure(t *testing.T) {
	srv := newJWKSServer(t)
	c := NewClient(srv.URL)

	kid := srv.signer.KeyID()
	if _, err := c.Key(context.Background(), kid); err != nil {
		t.Fatalf("Key: %v", err)
	}

	srv.failing.Store(true)
	c.mu.Lock()
	c.fetched = time.Now().Add(-2 * cacheTTL) // expire the cache
	c.mu.Unlock()

	key, err := c.Key(context.Background(), kid)
	if err != nil {
		t.Fatalf("expected the stale key on refresh failure, got error: %v", err)
	}
	if key.N.Cmp(srv.signer.Public().N) != 0 {
		t.Fatal("stale key does not match")
	}

	// Same failure with an EMPTY cache: nothing to fall back to, the
	// error must propagate.
	empty := NewClient(srv.URL)
	if _, err := empty.Key(context.Background(), kid); err == nil {
		t.Fatal("expected an error with a failing server and an empty cache")
	}
}

func TestKeyUnknownKidInDocument(t *testing.T) {
	srv := newJWKSServer(t)
	c := NewClient(srv.URL)

	if _, err := c.Key(context.Background(), "no-such-kid"); err == nil {
		t.Fatal("expected an error for a kid absent from the JWKS document")
	}
}
