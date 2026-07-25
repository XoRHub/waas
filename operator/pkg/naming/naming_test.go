package naming

import (
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"alice", "alice"},
		{"Alice Dupont", "alice-dupont"},
		{"Zoé Lefèvre", "zoe-lefevre"}, // diacritics folded
		{"jonathan.monnet28@gmail.com", "jonathan-monnet28-gmail-com"},
		{"__CAD -- Station__", "cad-station"}, // runs collapse, ends trimmed
		{"日本語", "x"},                          // nothing survives → fallback
		{"", "x"},
		{"a", "a"},
		{"9lives", "9lives"}, // digit start is DNS-1123-valid
	}
	for _, c := range cases {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("Sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeTruncatesCleanly(t *testing.T) {
	long := strings.Repeat("ab-", 40)
	got := Sanitize(long)
	if len(got) > MaxLabel {
		t.Fatalf("length %d exceeds %d", len(got), MaxLabel)
	}
	if strings.HasSuffix(got, "-") {
		t.Fatalf("truncation must not leave a trailing dash: %q", got)
	}
	if got := SanitizeWithLimit("abcdef", 3); got != "abc" {
		t.Fatalf("limit not applied: %q", got)
	}
	if err := ValidateLabel(Sanitize(long)); err != nil {
		t.Fatalf("sanitized output must validate: %v", err)
	}
}

func TestSuffixIsDeterministicAndDiscriminates(t *testing.T) {
	// Both sides identical on purpose: Suffix must be a pure function.
	if Suffix("Zoé") != Suffix("Zoé") { //nolint:staticcheck // SA4000: deliberate determinism check
		t.Fatal("suffix must be deterministic")
	}
	// The whole point: two raw values that sanitize identically get
	// different suffixes.
	if Sanitize("Zoé") != Sanitize("zoe") {
		t.Fatal("test premise broken")
	}
	if Suffix("Zoé") == Suffix("zoe") {
		t.Fatal("suffix must discriminate raw values")
	}
	if len(Suffix("x")) != 6 { // dash + 5 hex
		t.Fatalf("unexpected suffix shape %q", Suffix("x"))
	}
}

func TestResolveNamespace(t *testing.T) {
	vals := PatternValues{User: "Zoé Lefèvre", Workspace: "CAD Station", TemplateName: "ubuntu-xfce", OS: "linux"}

	got, err := ResolveNamespace("waas-{user}", vals)
	if err != nil || got != "waas-zoe-lefevre" {
		t.Fatalf("got %q, %v", got, err)
	}
	got, err = ResolveNamespace("waas-{user}-{workspace}", PatternValues{User: "alice", Workspace: "CAD Station"})
	if err != nil || got != "waas-alice-cad-station" {
		t.Fatalf("got %q, %v", got, err)
	}
	// New placeholders: template name and OS.
	got, err = ResolveNamespace("waas-{os}-{templateName}", vals)
	if err != nil || got != "waas-linux-ubuntu-xfce" {
		t.Fatalf("got %q, %v", got, err)
	}
	// Empty pattern = no placement.
	if got, err := ResolveNamespace("", vals); err != nil || got != "" {
		t.Fatalf("empty pattern: got %q, %v", got, err)
	}
	// A pattern whose literal part is bogus is rejected, not rewritten.
	if _, err := ResolveNamespace("WAAS-{user}", vals); err == nil {
		t.Fatal("uppercase literal must be rejected")
	}
}

func TestResolveNamespaceRejectsUnknownPlaceholders(t *testing.T) {
	// A typo must FAIL, never resolve to an empty string.
	for _, bad := range []string{"waas-{grup}", "waas-{USER}", "waas-{user", "waas-user}", "waas-{}"} {
		if _, err := ResolveNamespace(bad, PatternValues{User: "alice"}); err == nil {
			t.Errorf("pattern %q must be rejected", bad)
		}
	}
}

func TestResolveNamespaceTruncationIsDeterministicAndCollisionFree(t *testing.T) {
	longA := strings.Repeat("engineering-platform", 5) + "-alpha"
	longB := strings.Repeat("engineering-platform", 5) + "-beta"

	a1, err := ResolveNamespace("waas-{user}", PatternValues{User: longA})
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := ResolveNamespace("waas-{user}", PatternValues{User: longA})
	b, _ := ResolveNamespace("waas-{user}", PatternValues{User: longB})

	if len(a1) > MaxLabel {
		t.Fatalf("resolved namespace too long: %d", len(a1))
	}
	if a1 != a2 {
		t.Fatalf("truncation must be deterministic: %q vs %q", a1, a2)
	}
	// Same long prefix, different tails: the hash keeps them apart.
	if a1 == b {
		t.Fatalf("two distinct long values must not merge after truncation: %q", a1)
	}
	if err := ValidateLabel(a1); err != nil {
		t.Fatalf("truncated result must stay a valid label: %v", err)
	}

	// Multi-placeholder pattern with several long values still fits.
	multi, err := ResolveNamespace("waas-{user}-{templateName}-{workspace}", PatternValues{
		User: longA, TemplateName: strings.Repeat("tpl", 30), Workspace: strings.Repeat("ws", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(multi) > MaxLabel {
		t.Fatalf("multi-token expansion exceeds the label limit: %d", len(multi))
	}
	// Short values stay readable (no gratuitous hash).
	short, _ := ResolveNamespace("waas-{user}", PatternValues{User: "alice"})
	if short != "waas-alice" {
		t.Fatalf("short values must not be hashed, got %q", short)
	}
}

func TestEffectivePattern(t *testing.T) {
	if got := EffectivePattern("waas-{user}", "waas-global"); got != "waas-{user}" {
		t.Fatalf("template pattern must win, got %q", got)
	}
	if got := EffectivePattern("", "waas-global"); got != "waas-global" {
		t.Fatalf("global pattern must apply when the template has none, got %q", got)
	}
	if got := EffectivePattern("", ""); got != BuiltinNamespacePattern {
		t.Fatalf("built-in fallback must apply last, got %q", got)
	}
}

// The built-in default carries a token: it must RESOLVE into a per-user
// namespace everywhere the chain bottoms out, never be handled as a
// literal.
func TestBuiltinDefaultResolvesPerUser(t *testing.T) {
	got, err := ResolveNamespace(EffectivePattern("", ""), PatternValues{User: "Zoé Lefèvre"})
	if err != nil || got != "waas-zoe-lefevre" {
		t.Fatalf("built-in default must resolve to the sanitized user namespace, got %q, %v", got, err)
	}

	// A very long username truncates deterministically into a valid
	// DNS-1123 label, discriminated by the raw-value hash suffix.
	long := strings.Repeat("engineering-platform", 5)
	a, err := ResolveNamespace(EffectivePattern("", ""), PatternValues{User: long})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ResolveNamespace(EffectivePattern("", ""), PatternValues{User: long})
	if a != b {
		t.Fatalf("truncation must be deterministic: %q vs %q", a, b)
	}
	if err := ValidateLabel(a); err != nil {
		t.Fatalf("truncated per-user namespace must stay a valid label: %v", err)
	}
	if !strings.HasSuffix(a, Suffix(long)) {
		t.Fatalf("truncated per-user namespace must carry the deterministic suffix, got %q", a)
	}
}

// PersonalNamespace is what the operator and the webhook use to decide
// "is this namespace the user's own": it must agree with the resolution
// the api-server froze into the spec, INCLUDING above the token budget,
// where a hand-made "waas-"+Sanitize(user) silently diverges.
func TestPersonalNamespaceMatchesResolution(t *testing.T) {
	for _, user := range []string{
		"alice",
		"Zoé Lefèvre",
		strings.Repeat("a", 58),               // exactly the token budget
		strings.Repeat("a", 59),               // one over: truncated + suffixed
		strings.Repeat("engineering-team", 6), // far over
	} {
		resolved, err := ResolveNamespace(BuiltinNamespacePattern, PatternValues{User: user, UserID: "u-1"})
		if err != nil {
			t.Fatalf("resolving %d-char user: %v", len(user), err)
		}
		if got := PersonalNamespace(user, "u-1"); got != resolved {
			t.Errorf("PersonalNamespace(%d chars) = %q, resolution gives %q", len(user), got, resolved)
		}
		if got := PersonalNamespace(user, "u-1"); got == "waas-"+Sanitize(user) && len(Sanitize(user)) > 58 {
			t.Errorf("a truncated username must not resolve to the naive prefix form (%q)", got)
		}
	}
}

// Kubernetes namespace names are DNS-1123, so a Cyrillic, CJK, Greek or
// Arabic username leaves NO character behind. Without the account-id
// fallback every such account resolves to Sanitize's "x" and they all
// share one namespace — the very collision the per-user default exists
// to prevent, at the scale of a whole directory.
func TestErasedUsernameFallsBackOnAccountID(t *testing.T) {
	const idA = "a1b2c3d4-5e6f-7890-abcd-ef1234567890"
	const idB = "f0e9d8c7-6b5a-4321-9876-543210fedcba"

	got, err := ResolveNamespace(BuiltinNamespacePattern, PatternValues{User: "иван", UserID: idA})
	if err != nil {
		t.Fatalf("resolving an erased username with an id: %v", err)
	}
	if got != "waas-a1b2c3d4-ef1234567890" {
		t.Fatalf("expected the first and last id groups, got %q", got)
	}
	if err := ValidateLabel(got); err != nil {
		t.Fatalf("fallback must stay a valid label: %v", err)
	}

	// Deterministic, and distinct accounts never merge.
	again, _ := ResolveNamespace(BuiltinNamespacePattern, PatternValues{User: "иван", UserID: idA})
	other, _ := ResolveNamespace(BuiltinNamespacePattern, PatternValues{User: "王五", UserID: idB})
	if got != again {
		t.Fatalf("fallback must be deterministic: %q vs %q", got, again)
	}
	if got == other {
		t.Fatalf("two accounts must not share the fallback namespace: %q", got)
	}

	// A username that DOES survive normalization never touches the id.
	latin, _ := ResolveNamespace(BuiltinNamespacePattern, PatternValues{User: "alice", UserID: idA})
	if latin != "waas-alice" {
		t.Fatalf("a usable username must not be replaced by the id, got %q", latin)
	}

	// No id to fall back on: fail loudly rather than resolve to "waas-x"
	// and merge this account with every other unrepresentable one.
	if _, err := ResolveNamespace(BuiltinNamespacePattern, PatternValues{User: "иван"}); err == nil {
		t.Fatal("an erased username without an account id must be refused")
	}
}

func TestIdentitySegment(t *testing.T) {
	for in, want := range map[string]string{
		"a1b2c3d4-5e6f-7890-abcd-ef1234567890": "a1b2c3d4-ef1234567890",
		"u-alice":                              "u-alice",
		"single":                               "single",
	} {
		if got := IdentitySegment(in); got != want {
			t.Errorf("IdentitySegment(%q) = %q, want %q", in, got, want)
		}
	}
}

// The webhook and the operator decide placement and ownership with this
// one predicate; a divergence would hand out a quota without an owner.
func TestIsPersonalNamespaceOf(t *testing.T) {
	for name, tc := range map[string]struct {
		username string
		ns       string
		want     bool
	}{
		"own namespace": {"alice", "waas-alice", true},
		// Also the personal namespace of the user "alice-lab": the
		// predicate is a name rule, the caller checks the owner label.
		"derived namespace":    {"alice", "waas-alice-lab", true},
		"other user entirely":  {"alice", "waas-bob", false},
		"shared namespace":     {"alice", "waas-workspaces", false},
		"prefix without dash":  {"alice", "waas-alicia", false},
		"empty username":       {"", "waas-alice", false},
		"empty namespace":      {"alice", "", false},
		"sanitized username":   {"Alice Smith", "waas-alice-smith", true},
		"long username agrees": {strings.Repeat("a", 59), PersonalNamespace(strings.Repeat("a", 59), ""), true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := IsPersonalNamespaceOf(tc.username, "", tc.ns); got != tc.want {
				t.Fatalf("IsPersonalNamespaceOf(%q, %q) = %v, want %v", tc.username, tc.ns, got, tc.want)
			}
		})
	}
}

func TestValidatePattern(t *testing.T) {
	// "waas-workspaces" is no longer the default but stays a legitimate
	// pattern: the explicit shared-namespace opt-in.
	for _, ok := range []string{BuiltinNamespacePattern, "waas-{os}-{templateName}", "waas-workspaces", "waas-{user}-{workspace}"} {
		if err := ValidatePattern(ok); err != nil {
			t.Errorf("ValidatePattern(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"waas-{grup}", "WAAS-{user}", strings.Repeat("x", 60) + "-{user}-{workspace}"} {
		if err := ValidatePattern(bad); err == nil {
			t.Errorf("ValidatePattern(%q) must fail", bad)
		}
	}
}

func TestValidateLabel(t *testing.T) {
	for _, bad := range []string{"", "-lead", "trail-", "UPPER", "dot.dot", strings.Repeat("a", 64)} {
		if err := ValidateLabel(bad); err == nil {
			t.Errorf("ValidateLabel(%q) must fail", bad)
		}
	}
	for _, ok := range []string{"a", "waas-alice", "9lives", strings.Repeat("a", 63)} {
		if err := ValidateLabel(ok); err != nil {
			t.Errorf("ValidateLabel(%q): %v", ok, err)
		}
	}
}
