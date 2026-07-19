package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUserMarshalDerivesSSOAndHidesSecrets(t *testing.T) {
	sso, err := json.Marshal(User{ID: "u-1", Username: "alice", OIDCSubject: "sub-alice", PasswordHash: "argon2:x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(sso), `"sso":true`) {
		t.Fatalf("OIDC-bound user must serialize sso=true, got %s", sso)
	}
	if strings.Contains(string(sso), "sub-alice") || strings.Contains(string(sso), "argon2") {
		t.Fatalf("subject/hash must never reach the wire, got %s", sso)
	}

	local, err := json.Marshal(User{ID: "u-2", Username: "bob"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(local), `"sso":false`) {
		t.Fatalf("local user must serialize sso=false, got %s", local)
	}
}
