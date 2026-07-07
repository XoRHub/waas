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
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// MaxLabel is the DNS-1123 label length limit.
const MaxLabel = 63

// Placement pattern placeholders. Every value is sanitized before use;
// see Placeholders() for the documented list (sources, semantics).
const (
	TokenUser         = "{user}"
	TokenWorkspace    = "{workspace}"
	TokenTemplateName = "{templateName}"
	TokenOS           = "{os}"
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

// BuiltinNamespacePattern is the last resort of the precedence chain
// (template pattern > operator env pattern > this): a single shared
// workloads namespace. Deliberately a plain literal — predictable, and
// admins opt into per-user/template isolation with an explicit pattern.
const BuiltinNamespacePattern = "waas-workspace"

// Placeholder documents one pattern token: THE source the UI contextual
// help and the docs render — never a hand-maintained copy.
type Placeholder struct {
	Token       string `json:"token"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

// Placeholders lists every token a namespace pattern may use, with its
// value source. Every value is sanitized (NFKD, lowercase, DNS-1123)
// before entering a name; none can be absent at creation time.
func Placeholders() []Placeholder {
	return []Placeholder{
		{Token: TokenUser, Source: "identity (Authentik username)", Description: "owner of the workspace"},
		{Token: TokenWorkspace, Source: "workspace displayName", Description: "the workspace being created"},
		{Token: TokenTemplateName, Source: "WorkspaceTemplate metadata.name", Description: "template the workspace is stamped from"},
		{Token: TokenOS, Source: "WorkspaceTemplate spec.os", Description: "linux or windows — the actual provisioning path"},
	}
}

// PatternValues carries the resolved raw values, one per placeholder.
type PatternValues struct {
	User         string
	Workspace    string
	TemplateName string
	OS           string
}

func (v PatternValues) byToken() map[string]string {
	return map[string]string{
		TokenUser:         v.User,
		TokenWorkspace:    v.Workspace,
		TokenTemplateName: v.TemplateName,
		TokenOS:           v.OS,
	}
}

// tokenRE matches {anything} so unknown placeholders are REJECTED, never
// silently resolved to an empty string (a typo like {grup} must fail).
var tokenRE = regexp.MustCompile(`\{[^{}]*\}`)

// EffectivePattern applies the precedence chain: the template's pattern
// when set, else the operator-wide pattern (env), else the built-in.
// Changing the global pattern only affects NEW workspaces: the resolved
// value is frozen into spec.targetNamespace at creation.
func EffectivePattern(templatePattern, globalPattern string) string {
	if templatePattern != "" {
		return templatePattern
	}
	if globalPattern != "" {
		return globalPattern
	}
	return BuiltinNamespacePattern
}

// ResolveNamespace expands a placement namespace pattern. Every token
// value is sanitized; a value that must be TRUNCATED to fit its budget
// gets a deterministic short hash of the raw value appended, so two long
// distinct values can never silently merge into the same namespace.
// The literal parts of the pattern are the admin's to get right — they
// are validated, not rewritten. Empty pattern = no placement (legacy).
func ResolveNamespace(pattern string, values PatternValues) (string, error) {
	if pattern == "" {
		return "", nil
	}
	known := values.byToken()

	// Strict token scan: reject unknown placeholders and stray braces.
	tokens := tokenRE.FindAllString(pattern, -1)
	literalLen := len(pattern)
	for _, tok := range tokens {
		if _, ok := known[tok]; !ok {
			return "", fmt.Errorf("unknown placeholder %s (known: %s)", tok, knownTokens())
		}
		literalLen -= len(tok)
	}
	if rest := tokenRE.ReplaceAllString(pattern, ""); strings.ContainsAny(rest, "{}") {
		return "", fmt.Errorf("malformed pattern %q: unbalanced braces", pattern)
	}

	// Budget the tokens so the expansion always fits: split the remaining
	// space evenly across the token occurrences.
	budget := MaxLabel
	if len(tokens) > 0 {
		budget = (MaxLabel - literalLen) / len(tokens)
	}
	if budget < 1 {
		return "", fmt.Errorf("placement namespace pattern %q leaves no room for its tokens", pattern)
	}

	out := tokenRE.ReplaceAllStringFunc(pattern, func(tok string) string {
		return fitSegment(known[tok], budget)
	})
	if err := ValidateLabel(out); err != nil {
		return "", fmt.Errorf("placement namespace pattern %q resolves to an invalid namespace name: %w", pattern, err)
	}
	return out, nil
}

// fitSegment sanitizes one placeholder value into its budget. Values
// that fit stay readable; values that would be truncated carry Suffix()
// of the RAW value (deterministic: the same input always lands in the
// same namespace) so truncation cannot cause silent collisions.
func fitSegment(raw string, budget int) string {
	sanitized := Sanitize(raw)
	if len(sanitized) <= budget {
		return sanitized
	}
	suffix := Suffix(raw) // "-xxxxx"
	head := budget - len(suffix)
	if head < 1 {
		head = 1
	}
	return SanitizeWithLimit(raw, head) + suffix
}

// ValidatePattern checks a pattern statically (unknown tokens, room for
// expansion, literal validity) by resolving it with sample values. Used
// by the template webhook, the api-server template editor and the
// operator's startup check of the global env pattern.
func ValidatePattern(pattern string) error {
	_, err := ResolveNamespace(pattern, PatternValues{
		User: "sample-user", Workspace: "sample-workspace",
		TemplateName: "sample-template", OS: "linux",
	})
	return err
}

func knownTokens() string {
	names := make([]string, 0, len(Placeholders()))
	for _, p := range Placeholders() {
		names = append(names, p.Token)
	}
	return strings.Join(names, " ")
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
