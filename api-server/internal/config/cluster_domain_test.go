package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeResolvConf(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing resolv.conf fixture: %v", err)
	}
	return path
}

func TestDiscoverClusterDomain(t *testing.T) {
	// Standard kubelet-injected search line.
	kubelet := "nameserver 10.43.0.10\n" +
		"search waas.svc.cluster.local svc.cluster.local cluster.local\n" +
		"options ndots:5\n"
	if got := discoverClusterDomain(writeResolvConf(t, kubelet)); got != "cluster.local" {
		t.Fatalf("kubelet search line: want cluster.local, got %q", got)
	}

	// Custom cluster domain.
	custom := "search waas.svc.corp.k8s svc.corp.k8s corp.k8s\nnameserver 10.43.0.10\n"
	if got := discoverClusterDomain(writeResolvConf(t, custom)); got != "corp.k8s" {
		t.Fatalf("custom domain: want corp.k8s, got %q", got)
	}

	// Missing file falls back, never errors.
	if got := discoverClusterDomain(filepath.Join(t.TempDir(), "absent")); got != "cluster.local" {
		t.Fatalf("missing file: want cluster.local fallback, got %q", got)
	}

	// A resolv.conf without a svc. entry (laptop, dev mode) falls back too.
	laptop := "search home.lan example.com\nnameserver 192.168.1.1\n"
	if got := discoverClusterDomain(writeResolvConf(t, laptop)); got != "cluster.local" {
		t.Fatalf("no svc. entry: want cluster.local fallback, got %q", got)
	}
}
