// Package naming normalizes identity-derived strings (Authentik
// usernames, workspace display names) into DNS-1123 labels, and resolves
// the placement namespace pattern. It is the single normalization used by
// the api-server (resolution at creation), the admission webhook
// (enforcement) and the operator (second line): all three MUST agree, so
// none of them may normalize on its own.
//
// Normalization rules (documented contract):
//  1. Unicode NFKD decomposition, combining marks dropped (é → e, ü → u).
//  2. Lowercase.
//  3. Every run of characters outside [a-z0-9] becomes a single "-".
//  4. Leading/trailing "-" are trimmed.
//  5. Empty result falls back to "x".
//  6. Truncated to the requested budget (≤ 63, the DNS-1123 label max),
//     never ending on a "-".
//
// Normalization is lossy ("Zoé" and "zoe" collide): callers that need
// uniqueness append Suffix() of the raw value, or check collisions
// explicitly at creation time.
package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// MaxLabel is the DNS-1123 label length limit.
const MaxLabel = 63

// TokenUser and TokenWorkspace are the placeholders a template placement
// pattern may use.
const (
	TokenUser      = "{user}"
	TokenWorkspace = "{workspace}"
)

// Sanitize normalizes s into a DNS-1123 label of at most MaxLabel runes.
func Sanitize(s string) string { return SanitizeWithLimit(s, MaxLabel) }

// SanitizeWithLimit normalizes s into a DNS-1123 label of at most limit
// characters (callers reserve room for prefixes/suffixes this way).
func SanitizeWithLimit(s string, limit int) string {
	if limit <= 0 || limit > MaxLabel {
		limit = MaxLabel
	}
	var b strings.Builder
	pendingDash := false
	for _, r := range norm.NFKD.String(s) {
		if unicode.Is(unicode.Mn, r) { // combining mark from the decomposition
			continue
		}
		r = unicode.ToLower(r)
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingDash = false
			b.WriteRune(r)
		default:
			pendingDash = true
		}
	}
	out := b.String()
	if len(out) > limit {
		out = strings.TrimRight(out[:limit], "-")
	}
	if out == "" {
		return "x"
	}
	return out
}

// Suffix returns a short deterministic discriminator ("-xxxxx", 5 hex
// chars of sha256) of the RAW pre-normalization value, used to break
// collisions between distinct inputs that normalize identically.
func Suffix(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "-" + hex.EncodeToString(sum[:])[:5]
}

// ResolveNamespace expands a placement namespace pattern. Tokens: {user}
// (sanitized username) and {workspace} (sanitized workspace display
// name). The literal parts of the pattern are the admin's to get right —
// they are validated, not rewritten.
func ResolveNamespace(pattern, username, workspaceDisplayName string) (string, error) {
	if pattern == "" {
		return "", nil
	}
	// Budget the tokens so the expansion always fits: split the remaining
	// space evenly across the tokens present.
	tokens := strings.Count(pattern, TokenUser) + strings.Count(pattern, TokenWorkspace)
	literal := len(pattern) -
		strings.Count(pattern, TokenUser)*len(TokenUser) -
		strings.Count(pattern, TokenWorkspace)*len(TokenWorkspace)
	budget := MaxLabel
	if tokens > 0 {
		budget = (MaxLabel - literal) / tokens
	}
	if budget < 1 {
		return "", fmt.Errorf("placement namespace pattern %q leaves no room for its tokens", pattern)
	}

	out := strings.ReplaceAll(pattern, TokenUser, SanitizeWithLimit(username, budget))
	out = strings.ReplaceAll(out, TokenWorkspace, SanitizeWithLimit(workspaceDisplayName, budget))
	if err := ValidateLabel(out); err != nil {
		return "", fmt.Errorf("placement namespace pattern %q resolves to an invalid namespace name: %w", pattern, err)
	}
	return out, nil
}

// ValidateLabel checks that s already is a DNS-1123 label (it does not
// normalize — use it on resolved/explicit values).
func ValidateLabel(s string) error {
	if s == "" {
		return fmt.Errorf("empty name")
	}
	if len(s) > MaxLabel {
		return fmt.Errorf("%q is longer than %d characters", s, MaxLabel)
	}
	for i, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (r == '-' && i > 0 && i < len(s)-1)
		if !ok {
			return fmt.Errorf("%q is not a valid DNS-1123 label (lowercase alphanumerics and inner dashes only)", s)
		}
	}
	return nil
}
