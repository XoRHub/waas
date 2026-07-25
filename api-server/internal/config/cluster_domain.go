package config

import (
	"os"
	"strings"
)

// defaultClusterDomain is the kubeadm default, used whenever discovery
// finds nothing better.
const defaultClusterDomain = "cluster.local"

// DiscoverClusterDomain returns the cluster's DNS domain as seen from
// this pod. cluster.local is the default of kubeadm and nearly every
// distribution, but it is configurable (kubelet clusterDomain + the
// CoreDNS Corefile), so it is discovered rather than assumed: kubelet
// injects a "search <ns>.svc.<domain> svc.<domain> <domain>" line into
// the pod's /etc/resolv.conf, and the first entry starting with "svc."
// carries the domain. Never fatal: an unreadable file or one without
// such an entry (dev mode on a laptop) falls back to cluster.local.
// WAAS_CLUSTER_DOMAIN overrides both (see Load).
func DiscoverClusterDomain() string {
	return discoverClusterDomain("/etc/resolv.conf")
}

func discoverClusterDomain(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultClusterDomain
	}
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "search" {
			continue
		}
		for _, entry := range fields[1:] {
			if domain, ok := strings.CutPrefix(entry, "svc."); ok && domain != "" {
				return domain
			}
		}
	}
	return defaultClusterDomain
}
