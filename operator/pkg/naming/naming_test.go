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

func TestValidatePattern(t *testing.T) {
	for _, ok := range []string{"waas-{user}", "waas-{os}-{templateName}", BuiltinNamespacePattern, "waas-{user}-{workspace}"} {
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
