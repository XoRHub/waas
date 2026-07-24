package controller

// Env parsing tests of the desktop egress configuration: fail-closed
// defaults when the variables are missing, explicit values honored
// (including an explicitly empty blocked list), and unparseable bools or
// CIDRs surfacing as startup errors (main refuses to start on them).

import (
	"os"
	"strings"
	"testing"
)

var desktopEgressEnvVars = []string{
	"WAAS_DESKTOP_EGRESS_ENABLED",
	"WAAS_DESKTOP_EGRESS_ALLOW_INTERNET",
	"WAAS_DESKTOP_EGRESS_BLOCKED_CIDRS",
	"WAAS_DESKTOP_EGRESS_EXTRA_ALLOWED_CIDRS",
}

// clearEgressEnv unsets every WAAS_DESKTOP_EGRESS_* variable for the
// test (t.Setenv registers the restore; Unsetenv then removes it, so
// "missing" is actually missing whatever the outer environment holds).
func clearEgressEnv(t *testing.T) {
	t.Helper()
	for _, k := range desktopEgressEnvVars {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func TestDesktopEgressFromEnvDefaultsAreHardened(t *testing.T) {
	clearEgressEnv(t)
	cfg, err := DesktopEgressFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	// Missing envs must NOT open the hole: enabled, internet minus the
	// default carve-out (IMDS /32 + RFC1918), no extras.
	if !cfg.Enabled || !cfg.AllowInternet {
		t.Fatalf("missing envs must default to the hardened posture, got %+v", cfg)
	}
	if len(cfg.BlockedCIDRs) != 4 || cfg.BlockedCIDRs[0] != "169.254.169.254/32" {
		t.Fatalf("missing blocked list must default to IMDS /32 + RFC1918, got %v", cfg.BlockedCIDRs)
	}
	if len(cfg.ExtraAllowedCIDRs) != 0 {
		t.Fatalf("missing extra list must default to empty, got %v", cfg.ExtraAllowedCIDRs)
	}
}

func TestDesktopEgressFromEnvExplicitValues(t *testing.T) {
	clearEgressEnv(t)
	t.Setenv("WAAS_DESKTOP_EGRESS_ENABLED", "false")
	t.Setenv("WAAS_DESKTOP_EGRESS_ALLOW_INTERNET", "false")
	t.Setenv("WAAS_DESKTOP_EGRESS_BLOCKED_CIDRS", "169.254.0.0/16, 10.0.0.0/8")
	t.Setenv("WAAS_DESKTOP_EGRESS_EXTRA_ALLOWED_CIDRS", "10.0.5.0/24")
	cfg, err := DesktopEgressFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled || cfg.AllowInternet {
		t.Fatalf("explicit false values must be honored, got %+v", cfg)
	}
	if len(cfg.BlockedCIDRs) != 2 || cfg.BlockedCIDRs[0] != "169.254.0.0/16" || cfg.BlockedCIDRs[1] != "10.0.0.0/8" {
		t.Fatalf("blocked CIDRs must mirror the env exactly (whitespace trimmed), got %v", cfg.BlockedCIDRs)
	}
	if len(cfg.ExtraAllowedCIDRs) != 1 || cfg.ExtraAllowedCIDRs[0] != "10.0.5.0/24" {
		t.Fatalf("extra CIDRs must mirror the env, got %v", cfg.ExtraAllowedCIDRs)
	}
}

// TestDesktopEgressFromEnvEmptyBlockedList: Helm renders
// `blockedCIDRs: []` as a present-but-empty env — an explicit choice
// (warned about at startup), not a fallback to the default.
func TestDesktopEgressFromEnvEmptyBlockedList(t *testing.T) {
	clearEgressEnv(t)
	t.Setenv("WAAS_DESKTOP_EGRESS_BLOCKED_CIDRS", "")
	cfg, err := DesktopEgressFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.BlockedCIDRs) != 0 {
		t.Fatalf("an explicitly empty blocked list must stay empty, got %v", cfg.BlockedCIDRs)
	}
}

func TestDesktopEgressFromEnvInvalidCIDRFailsStartup(t *testing.T) {
	for _, envVar := range []string{"WAAS_DESKTOP_EGRESS_BLOCKED_CIDRS", "WAAS_DESKTOP_EGRESS_EXTRA_ALLOWED_CIDRS"} {
		clearEgressEnv(t)
		t.Setenv(envVar, "10.0.0.0/8,not-a-cidr")
		_, err := DesktopEgressFromEnv()
		if err == nil || !strings.Contains(err.Error(), envVar) || !strings.Contains(err.Error(), "not-a-cidr") {
			t.Fatalf("%s with an invalid CIDR must error (naming the var and the CIDR), got %v", envVar, err)
		}
	}
}

// TestDesktopEgressFromEnvNonSubsetBlockedFailsStartup: a blocked entry
// that parses but cannot sit in IPBlock.Except of 0.0.0.0/0 must fail at
// startup, not at reconcile time on every workspace.
func TestDesktopEgressFromEnvNonSubsetBlockedFailsStartup(t *testing.T) {
	for _, cidr := range []string{"::/0", "fd00::/8", "0.0.0.0/0"} {
		clearEgressEnv(t)
		t.Setenv("WAAS_DESKTOP_EGRESS_BLOCKED_CIDRS", "10.0.0.0/8,"+cidr)
		_, err := DesktopEgressFromEnv()
		if err == nil || !strings.Contains(err.Error(), cidr) {
			t.Fatalf("blocked CIDR %q is no strict subset of 0.0.0.0/0 and must error, got %v", cidr, err)
		}
	}
	// The same CIDRs are legal as extra-allowed: they get their own
	// IPBlock rule instead of an except entry.
	clearEgressEnv(t)
	t.Setenv("WAAS_DESKTOP_EGRESS_EXTRA_ALLOWED_CIDRS", "fd00::/8")
	if _, err := DesktopEgressFromEnv(); err != nil {
		t.Fatalf("an IPv6 extra-allowed CIDR is a standalone rule and must be accepted, got %v", err)
	}
}

func TestDesktopEgressFromEnvInvalidBoolFailsStartup(t *testing.T) {
	for _, envVar := range []string{"WAAS_DESKTOP_EGRESS_ENABLED", "WAAS_DESKTOP_EGRESS_ALLOW_INTERNET"} {
		clearEgressEnv(t)
		t.Setenv(envVar, "not-a-bool")
		_, err := DesktopEgressFromEnv()
		if err == nil || !strings.Contains(err.Error(), envVar) {
			t.Fatalf("%s with an invalid boolean must error, got %v", envVar, err)
		}
	}
}
