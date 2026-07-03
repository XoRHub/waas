package auth

import (
	"errors"
	"testing"
	"time"
)

func TestSignAndVerifyRoundTrip(t *testing.T) {
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}

	token, err := signer.Sign(NewConnectionClaims("waas", "user-1", "sess-1", "ws-1", time.Minute))
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

	token, err := signer.Sign(NewConnectionClaims("waas", "u", "s", "w", -time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := VerifyConnectionToken(token, "waas", signer.Public()); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
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
