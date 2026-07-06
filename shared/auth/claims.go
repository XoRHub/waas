// Package auth holds the JWT claim shapes and signing primitives shared by the
// API server (which issues tokens) and the WebSocket proxy (which validates
// them before ever opening a TCP connection to guacd).
package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Role is the platform-level role carried in access tokens.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// Token audiences. The proxy only ever accepts connection tokens, so a stolen
// API access token can never be replayed against guacd.
const (
	AudienceAPI        = "waas-api"
	AudienceConnection = "waas-connection"
)

// AccessClaims is the payload of the API access token.
type AccessClaims struct {
	Role Role `json:"role"`
	jwt.RegisteredClaims
}

// ClipboardGrant is the clipboard permission the API server resolved from
// the user's WorkspacePolicy and stamped into the connection token. The
// proxy enforces it by filtering clipboard instructions — the frontend
// only mirrors it. Zero value = both directions denied (fail closed).
type ClipboardGrant struct {
	// Copy permits remote → local clipboard (copy from the workspace).
	Copy bool `json:"copy"`
	// Paste permits local → remote clipboard (paste into the workspace).
	Paste bool `json:"paste"`
}

// ConnectionClaims is the payload of the short-lived token that authorizes a
// single desktop connection through the WebSocket proxy. It deliberately
// carries no connection secrets (VNC/RDP credentials stay server-side); the
// proxy exchanges the session ID against the API server's internal endpoint.
type ConnectionClaims struct {
	SessionID   string         `json:"sessionId"`
	WorkspaceID string         `json:"workspaceId"`
	Clipboard   ClipboardGrant `json:"clipboard"`
	jwt.RegisteredClaims
}

// NewAccessClaims builds access-token claims for a user.
func NewAccessClaims(issuer, userID string, role Role, ttl time.Duration) AccessClaims {
	return AccessClaims{
		Role:             role,
		RegisteredClaims: registered(issuer, userID, AudienceAPI, ttl),
	}
}

// NewConnectionClaims builds connection-token claims for one session.
func NewConnectionClaims(issuer, userID, sessionID, workspaceID string, clipboard ClipboardGrant, ttl time.Duration) ConnectionClaims {
	return ConnectionClaims{
		SessionID:        sessionID,
		WorkspaceID:      workspaceID,
		Clipboard:        clipboard,
		RegisteredClaims: registered(issuer, userID, AudienceConnection, ttl),
	}
}

func registered(issuer, subject, audience string, ttl time.Duration) jwt.RegisteredClaims {
	now := time.Now().UTC()
	return jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   subject,
		Audience:  jwt.ClaimStrings{audience},
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
}
