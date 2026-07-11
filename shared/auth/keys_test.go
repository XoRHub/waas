package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestSignAndVerifyRoundTrip(t *testing.T) {
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}

	token, err := signer.Sign(NewConnectionClaims("waas", "user-1", "sess-1", "ws-1", ClipboardGrant{Copy: true, Paste: true}, time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	claims, err := VerifyConnectionToken(token, "waas", signer.Public())
	if err != nil {
		t.Fatalf("VerifyConnectionToken: %v", err)
	}
	if claims.SessionID != "sess-1" || claims.WorkspaceID != "ws-1" || claims.Subject != "user-1" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestVerifyRejectsWrongAudience(t *testing.T) {
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}

	// An API access token must never be accepted as a connection token.
	token, err := signer.Sign(NewAccessClaims("waas", "user-1", RoleUser, time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := VerifyConnectionToken(token, "waas", signer.Public()); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}

	token, err := signer.Sign(NewConnectionClaims("waas", "u", "s", "w", ClipboardGrant{}, -time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := VerifyConnectionToken(token, "waas", signer.Public()); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestVerifyAccessTokenRoundTripAndRejections(t *testing.T) {
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}

	token, err := signer.Sign(NewAccessClaims("waas", "user-1", RoleAdmin, time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, err := VerifyAccessToken(token, "waas", signer.Public())
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if claims.Subject != "user-1" || claims.Role != RoleAdmin {
		t.Fatalf("unexpected claims: %+v", claims)
	}

	// A connection token must never be accepted as an API token
	// (audience), and the issuer must match.
	connToken, err := signer.Sign(NewConnectionClaims("waas", "u", "s", "w", ClipboardGrant{}, time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := VerifyAccessToken(connToken, "waas", signer.Public()); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong audience: expected ErrInvalidToken, got %v", err)
	}
	if _, err := VerifyAccessToken(token, "evil-issuer", signer.Public()); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong issuer: expected ErrInvalidToken, got %v", err)
	}
}

// Forged tokens: algorithm confusion (none, HS256 keyed with public
// material) and a signature from another key must all fail with
// ErrInvalidToken on BOTH verifiers.
func TestVerifyRejectsForgedTokens(t *testing.T) {
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	other, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}

	connClaims := NewConnectionClaims("waas", "u", "s", "w", ClipboardGrant{}, time.Minute)
	accessClaims := NewAccessClaims("waas", "u", RoleUser, time.Minute)

	noneConn, err := jwt.NewWithClaims(jwt.SigningMethodNone, connClaims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("signing none token: %v", err)
	}
	hsConn, err := jwt.NewWithClaims(jwt.SigningMethodHS256, connClaims).SignedString([]byte("guessable"))
	if err != nil {
		t.Fatalf("signing hs256 token: %v", err)
	}
	otherConn, err := other.Sign(connClaims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	otherAccess, err := other.Sign(accessClaims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Claims without exp: WithExpirationRequired must reject them even
	// with a valid signature.
	noExp, err := signer.Sign(&ConnectionClaims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer: "waas", Subject: "u", Audience: jwt.ClaimStrings{AudienceConnection},
	}})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	for name, token := range map[string]string{
		"alg none":          noneConn,
		"alg hs256":         hsConn,
		"wrong signing key": otherConn,
		"missing exp":       noExp,
	} {
		if _, err := VerifyConnectionToken(token, "waas", signer.Public()); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s: expected ErrInvalidToken, got %v", name, err)
		}
	}
	if _, err := VerifyAccessToken(otherAccess, "waas", signer.Public()); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("access token, wrong signing key: expected ErrInvalidToken, got %v", err)
	}
}

func TestParseSignerPEMErrors(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating ec key: %v", err)
	}
	ecDER, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatalf("marshaling ec key: %v", err)
	}

	for name, pemBytes := range map[string][]byte{
		"no PEM block": []byte("not a pem"),
		"garbage DER":  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("junk")}),
		"non-RSA key":  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}),
	} {
		if _, err := ParseSignerPEM(pemBytes); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// The published JWKS must decode back to the signer's own public key —
// it is the only channel wwt has to verify connection tokens.
func TestJWKSPublishesTheVerificationKey(t *testing.T) {
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	set := signer.JWKS()
	if len(set.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(set.Keys))
	}
	k := set.Keys[0]
	if k.Kty != "RSA" || k.Alg != "RS256" || k.Use != "sig" || k.Kid != signer.KeyID() {
		t.Fatalf("unexpected JWK metadata: %+v", k)
	}
	n, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		t.Fatalf("decoding modulus: %v", err)
	}
	e, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		t.Fatalf("decoding exponent: %v", err)
	}
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	if pub.N.Cmp(signer.Public().N) != 0 || pub.E != signer.Public().E {
		t.Fatal("JWKS key does not match the signer's public key")
	}
}

func TestPEMRoundTrip(t *testing.T) {
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	pemBytes, err := signer.MarshalPEM()
	if err != nil {
		t.Fatalf("MarshalPEM: %v", err)
	}
	loaded, err := ParseSignerPEM(pemBytes)
	if err != nil {
		t.Fatalf("ParseSignerPEM: %v", err)
	}
	if loaded.KeyID() != signer.KeyID() {
		t.Fatalf("key id changed across PEM round trip")
	}
}
