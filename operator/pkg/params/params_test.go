package params

import (
	"strings"
	"testing"
)

func TestRegistryIsCoherent(t *testing.T) {
	knownCategories := map[Category]bool{}
	for _, c := range AllCategories() {
		knownCategories[c] = true
	}
	seen := map[string]map[string]bool{}
	for _, p := range registry {
		if p.Name == "" || p.Tier == "" || p.Kind == "" || p.Description == "" {
			t.Fatalf("incomplete registry entry: %+v", p)
		}
		if strings.HasPrefix(p.Name, CategorySelectorPrefix) {
			t.Fatalf("parameter %q collides with the userParams cat: selector syntax", p.Name)
		}
		if p.Category == "" {
			t.Fatalf("parameter %q has no category: every param must belong to a form section", p.Name)
		}
		if !knownCategories[p.Category] {
			t.Fatalf("parameter %q: category %q is not in AllCategories()", p.Name, p.Category)
		}
		if len(p.Protocols) == 0 {
			t.Fatalf("parameter %q declares no protocol", p.Name)
		}
		if p.Kind == KindEnum && len(p.Enum) == 0 {
			t.Fatalf("enum parameter %q has no values", p.Name)
		}
		if p.Kind == KindEnum && p.Default != "" {
			ok := false
			for _, e := range p.Enum {
				if e == p.Default {
					ok = true
				}
			}
			if !ok {
				t.Fatalf("parameter %q: default %q not in enum %v", p.Name, p.Default, p.Enum)
			}
		}
		for _, proto := range p.Protocols {
			if seen[proto] == nil {
				seen[proto] = map[string]bool{}
			}
			if seen[proto][p.Name] {
				t.Fatalf("parameter %q registered twice for %s", p.Name, proto)
			}
			seen[proto][p.Name] = true
		}
	}
}

func TestValidateTemplateParams(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		params   map[string]string
		wantErr  string
	}{
		{"valid vnc", "vnc", map[string]string{"color-depth": "16", "read-only": "false"}, ""},
		{"unknown param", "vnc", map[string]string{"made-up": "1"}, "not a registered"},
		{"wrong protocol", "vnc", map[string]string{"font-size": "14"}, "not a registered"},
		{"platform param", "vnc", map[string]string{"dest-host": "evil"}, "platform-owned"},
		{"credential in CR", "ssh", map[string]string{"password": "hunter2"}, "platform-owned"},
		{"bad enum", "vnc", map[string]string{"color-depth": "15"}, "must be one of"},
		{"bad bool", "vnc", map[string]string{"read-only": "yes"}, "must be true or false"},
		{"int bounds", "ssh", map[string]string{"font-size": "500"}, "must be <="},
		{"valid ssh", "ssh", map[string]string{"font-size": "14", "color-scheme": "green-black"}, ""},
		{"valid rdp advanced", "rdp", map[string]string{"security": "nla", "ignore-cert": "true"}, ""},
	}
	for _, tc := range cases {
		err := ValidateTemplateParams(tc.protocol, tc.params)
		if tc.wantErr == "" && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: expected error containing %q, got %v", tc.name, tc.wantErr, err)
		}
	}
}

func TestValidateUserParamNames(t *testing.T) {
	if err := ValidateUserParamNames("vnc", []string{"color-depth", "read-only"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ValidateUserParamNames("vnc", []string{"password"}); err == nil {
		t.Fatal("delegating a platform param must fail")
	}
	if err := ValidateUserParamNames("vnc", []string{"nope"}); err == nil {
		t.Fatal("delegating an unknown param must fail")
	}
	// cat: selectors: a known category is a valid entry (NOT an unknown
	// param name), an unknown category is rejected with a clear message.
	if err := ValidateUserParamNames("vnc", []string{"cat:audio", "color-depth"}); err != nil {
		t.Fatalf("cat: selector of a known category rejected: %v", err)
	}
	err := ValidateUserParamNames("vnc", []string{"cat:bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown category") {
		t.Fatalf("cat:bogus must be rejected as an unknown category, got %v", err)
	}
}

func TestValidateUserOverrides(t *testing.T) {
	allow := []string{"color-depth"}
	if err := ValidateUserOverrides("vnc", map[string]string{"color-depth": "8"}, allow, false); err != nil {
		t.Fatalf("allowed override rejected: %v", err)
	}
	if err := ValidateUserOverrides("vnc", map[string]string{"read-only": "true"}, allow, false); err == nil {
		t.Fatal("override outside the allow-list must fail for non-admins")
	}
	if err := ValidateUserOverrides("vnc", map[string]string{"read-only": "true"}, allow, true); err != nil {
		t.Fatalf("admin bypass must allow non-platform params: %v", err)
	}
	if err := ValidateUserOverrides("vnc", map[string]string{"dest-host": "evil"}, allow, true); err == nil {
		t.Fatal("platform params must be rejected even for admins")
	}
	if err := ValidateUserOverrides("vnc", map[string]string{"color-depth": "batman"}, allow, false); err == nil {
		t.Fatal("bad values must be rejected even when the name is allowed")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ResolveUserParamNames expands the userParams entries (names and cat:
// selectors) into the flat allow-list feeding ValidateUserOverrides.
func TestResolveUserParamNames(t *testing.T) {
	// Nominal: a selector expands to every non-platform name of the
	// category for the protocol, in registry order.
	if got := ResolveUserParamNames("vnc", []string{"cat:audio"}); !equalStrings(got, []string{"enable-audio", "audio-servername"}) {
		t.Fatalf("cat:audio on vnc = %v", got)
	}
	// Plain names pass through verbatim, order preserved and deduped.
	if got := ResolveUserParamNames("vnc", []string{"color-depth", "cursor", "color-depth"}); !equalStrings(got, []string{"color-depth", "cursor"}) {
		t.Fatalf("plain names = %v", got)
	}
	// cat:X + individual name of the same category: purely additive —
	// strictly the same result as cat:X alone, no error, no priority.
	withDup := ResolveUserParamNames("vnc", []string{"cat:audio", "audio-servername"})
	alone := ResolveUserParamNames("vnc", []string{"cat:audio"})
	if !equalStrings(withDup, alone) {
		t.Fatalf("cat:audio + audio-servername = %v, want the same as cat:audio alone (%v)", withDup, alone)
	}
	// Unknown category: contributes nothing (validation rejects it
	// upstream; resolution stays total).
	if got := ResolveUserParamNames("vnc", []string{"cat:bogus"}); len(got) != 0 {
		t.Fatalf("cat:bogus must resolve to nothing, got %v", got)
	}
	// A category holding only TierPlatform entries for the protocol
	// (session on vnc: recording-* are platform-owned) resolves to an
	// empty grant, not an error — fail-closed and forward-compatible.
	if got := ResolveUserParamNames("vnc", []string{"cat:session"}); len(got) != 0 {
		t.Fatalf("cat:session on vnc must resolve to nothing (platform-only), got %v", got)
	}
	// The selector never leaks platform names of a mixed category.
	for _, name := range ResolveUserParamNames("vnc", []string{"cat:connection"}) {
		if p := Lookup("vnc", name); p == nil || p.Tier == TierPlatform {
			t.Fatalf("cat:connection leaked %q", name)
		}
	}
	if out := ResolveUserParamNames("vnc", nil); len(out) != 0 {
		t.Fatalf("nil entries = %v, want empty", out)
	}
}

// The connect gate consumes the resolved list: names delegated through a
// cat: selector pass, everything else stays locked.
func TestValidateUserOverridesWithCategorySelector(t *testing.T) {
	allow := ResolveUserParamNames("vnc", []string{"cat:audio", "color-depth"})

	if err := ValidateUserOverrides("vnc", map[string]string{"enable-audio": "true"}, allow, false); err != nil {
		t.Fatalf("name delegated via cat:audio rejected: %v", err)
	}
	if err := ValidateUserOverrides("vnc", map[string]string{"color-depth": "16"}, allow, false); err != nil {
		t.Fatalf("plain name next to a selector rejected: %v", err)
	}
	if err := ValidateUserOverrides("vnc", map[string]string{"read-only": "true"}, allow, false); err == nil {
		t.Fatal("name outside the resolved list must fail for non-admins")
	}
	// A platform name smuggled as a raw entry still dies on the registry
	// gate, even for admins (the tier ban is unconditional).
	if err := ValidateUserOverrides("vnc", map[string]string{"dest-host": "evil"}, ResolveUserParamNames("vnc", []string{"dest-host"}), true); err == nil {
		t.Fatal("a platform param smuggled through userParams must be rejected even for admins")
	}
	if err := ValidateUserOverrides("vnc", map[string]string{"enable-audio": "yes"}, allow, false); err == nil {
		t.Fatal("bad values must be rejected even when the name is delegated")
	}
}

func TestForProtocolOrdersByCategoryThenTier(t *testing.T) {
	catRank := map[Category]int{}
	for i, c := range AllCategories() {
		catRank[c] = i
	}
	tierRank := map[Tier]int{TierUI: 0, TierAdvanced: 1, TierPlatform: 2}
	for _, proto := range []string{"vnc", "rdp", "ssh"} {
		list := ForProtocol(proto)
		if len(list) == 0 {
			t.Fatalf("%s must have registered parameters", proto)
		}
		for i := 1; i < len(list); i++ {
			prev, cur := list[i-1], list[i]
			if catRank[cur.Category] < catRank[prev.Category] {
				t.Fatalf("%s: category ordering broken at %s (%s after %s)", proto, cur.Name, cur.Category, prev.Category)
			}
			if cur.Category == prev.Category && tierRank[cur.Tier] < tierRank[prev.Tier] {
				t.Fatalf("%s: tier ordering broken inside category %s at %s (%s after %s)", proto, cur.Category, cur.Name, cur.Tier, prev.Tier)
			}
		}
	}
}
