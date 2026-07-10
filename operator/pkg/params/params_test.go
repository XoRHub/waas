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
