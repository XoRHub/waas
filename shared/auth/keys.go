package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("invalid token")

// Signer issues RS256-signed tokens and exposes the matching public JWKS.
type Signer struct {
	key   *rsa.PrivateKey
	keyID string
}

// NewSigner wraps an existing RSA private key.
func NewSigner(key *rsa.PrivateKey) *Signer {
	return &Signer{key: key, keyID: keyID(&key.PublicKey)}
}

// GenerateSigner creates a fresh 2048-bit key (dev / first boot).
func GenerateSigner() (*Signer, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generating rsa key: %w", err)
	}
	return NewSigner(key), nil
}

// ParseSignerPEM loads a PKCS#1 or PKCS#8 PEM-encoded private key.
func ParseSignerPEM(pemBytes []byte) (*Signer, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("parsing signing key: no PEM block found")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return NewSigner(key), nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing signing key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("parsing signing key: not an RSA key")
	}
	return NewSigner(key), nil
}

// MarshalPEM serializes the private key (PKCS#8) for persistence in a Secret.
func (s *Signer) MarshalPEM() ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(s.key)
	if err != nil {
		return nil, fmt.Errorf("marshaling signing key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// Sign produces a compact RS256 JWT for the given claims.
func (s *Signer) Sign(claims jwt.Claims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = s.keyID
	signed, err := token.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}
	return signed, nil
}

// Public returns the verification key.
func (s *Signer) Public() *rsa.PublicKey { return &s.key.PublicKey }

// KeyID returns the stable identifier published in the JWKS.
func (s *Signer) KeyID() string { return s.keyID }

// JWKS is the JSON Web Key Set document served at /.well-known/jwks.json.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWK is a single RSA public key in JWK form.
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKS returns the public key set for token verifiers (wwt, third parties).
func (s *Signer) JWKS() JWKS {
	pub := s.Public()
	return JWKS{Keys: []JWK{{
		Kty: "RSA",
		Use: "sig",
		Alg: "RS256",
		Kid: s.keyID,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}}
}

// VerifyConnectionToken parses and validates a connection token against the
// given public key, enforcing the RS256 algorithm and connection audience.
func VerifyConnectionToken(tokenString, issuer string, key *rsa.PublicKey) (*ConnectionClaims, error) {
	claims := &ConnectionClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims,
		func(t *jwt.Token) (any, error) { return key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(AudienceConnection),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidToken, err)
	}
	return claims, nil
}

// VerifyAccessToken parses and validates an API access token.
func VerifyAccessToken(tokenString, issuer string, key *rsa.PublicKey) (*AccessClaims, error) {
	claims := &AccessClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims,
		func(t *jwt.Token) (any, error) { return key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(AudienceAPI),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidToken, err)
	}
	return claims, nil
}

func keyID(pub *rsa.PublicKey) string {
	sum := sha256.Sum256(pub.N.Bytes())
	return hex.EncodeToString(sum[:8])
}
