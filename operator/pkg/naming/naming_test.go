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
	if Suffix("Zoé") != Suffix("Zoé") {
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
	got, err := ResolveNamespace("waas-{user}", "Zoé Lefèvre", "ignored")
	if err != nil || got != "waas-zoe-lefevre" {
		t.Fatalf("got %q, %v", got, err)
	}
	got, err = ResolveNamespace("waas-{user}-{workspace}", "alice", "CAD Station")
	if err != nil || got != "waas-alice-cad-station" {
		t.Fatalf("got %q, %v", got, err)
	}
	// Empty pattern = no placement.
	if got, err := ResolveNamespace("", "alice", ""); err != nil || got != "" {
		t.Fatalf("empty pattern: got %q, %v", got, err)
	}
	// Long username must still fit 63 chars.
	got, err = ResolveNamespace("waas-{user}", strings.Repeat("verylonguser", 10), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > MaxLabel {
		t.Fatalf("resolved namespace too long: %d", len(got))
	}
	// A pattern whose literal part is bogus is rejected, not rewritten.
	if _, err := ResolveNamespace("WAAS-{user}", "alice", ""); err == nil {
		t.Fatal("uppercase literal must be rejected")
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
