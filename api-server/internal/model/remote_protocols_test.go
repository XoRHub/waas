package model

import "testing"

func TestEffectiveProtocolsSynthesizesLegacyEntry(t *testing.T) {
	legacy := &RemoteWorkspace{
		Protocol: "vnc",
		Port:     5901,
		Params:   map[string]string{"color-depth": "24"},
	}
	protos := legacy.EffectiveProtocols()
	if len(protos) != 1 {
		t.Fatalf("protocols = %+v", protos)
	}
	p := protos[0]
	if p.Name != "vnc" || p.Port != 5901 || !p.Default || p.Params["color-depth"] != "24" {
		t.Fatalf("legacy entry = %+v", p)
	}
}

func TestEffectiveProtocolsPrefersDeclaredList(t *testing.T) {
	rw := &RemoteWorkspace{
		Protocol:  "vnc",
		Protocols: []RemoteProtocol{{Name: "rdp", Port: 3389}, {Name: "ssh", Port: 22}},
	}
	protos := rw.EffectiveProtocols()
	if len(protos) != 2 || protos[0].Name != "rdp" {
		t.Fatalf("protocols = %+v", protos)
	}
}

func TestDefaultProtocolFallsBackToFirst(t *testing.T) {
	rw := &RemoteWorkspace{
		Protocols: []RemoteProtocol{
			{Name: "rdp", Port: 3389},
			{Name: "ssh", Port: 22, Default: true},
		},
	}
	if got := rw.DefaultProtocol(); got.Name != "ssh" {
		t.Fatalf("default = %+v", got)
	}

	// No entry flagged: the first one applies.
	rw.Protocols[1].Default = false
	if got := rw.DefaultProtocol(); got.Name != "rdp" {
		t.Fatalf("fallback default = %+v", got)
	}
}

func TestProtocolNamed(t *testing.T) {
	rw := &RemoteWorkspace{
		Protocols: []RemoteProtocol{{Name: "rdp", Port: 3389}, {Name: "ssh", Port: 22}},
	}
	if p := rw.ProtocolNamed("ssh"); p == nil || p.Port != 22 {
		t.Fatalf("ProtocolNamed(ssh) = %+v", p)
	}
	if p := rw.ProtocolNamed("vnc"); p != nil {
		t.Fatalf("unknown protocol must be nil, got %+v", p)
	}
}
