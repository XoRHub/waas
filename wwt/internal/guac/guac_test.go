package guac

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

func TestInstructionEncodeDecodeRoundTrip(t *testing.T) {
	inst := Instruction{Opcode: "connect", Args: []string{"VERSION_1_5_0", "host", "", "5901"}}
	encoded := inst.Encode()
	if encoded != "7.connect,13.VERSION_1_5_0,4.host,0.,4.5901;" {
		t.Fatalf("unexpected encoding: %s", encoded)
	}

	decoded, err := ReadInstruction(bufio.NewReader(strings.NewReader(encoded)))
	if err != nil {
		t.Fatalf("ReadInstruction: %v", err)
	}
	if decoded.Opcode != inst.Opcode || len(decoded.Args) != len(inst.Args) {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}
	for i := range inst.Args {
		if decoded.Args[i] != inst.Args[i] {
			t.Fatalf("arg %d mismatch: %q != %q", i, decoded.Args[i], inst.Args[i])
		}
	}
}

func TestInstructionUnicodeLengths(t *testing.T) {
	// Guacamole lengths count code points, not bytes.
	inst := Instruction{Opcode: "name", Args: []string{"pöstgrés"}}
	decoded, err := ReadInstruction(bufio.NewReader(strings.NewReader(inst.Encode())))
	if err != nil {
		t.Fatalf("ReadInstruction: %v", err)
	}
	if decoded.Args[0] != "pöstgrés" {
		t.Fatalf("unicode arg mangled: %q", decoded.Args[0])
	}
}

// fakeGuacd speaks just enough of the server side of the handshake.
func fakeGuacd(t *testing.T, conn net.Conn) {
	t.Helper()
	r := bufio.NewReader(conn)

	sel, err := ReadInstruction(r)
	if err != nil || sel.Opcode != "select" {
		t.Errorf("expected select, got %+v err=%v", sel, err)
		return
	}
	args := Instruction{Opcode: "args", Args: []string{"VERSION_1_5_0", "hostname", "port", "password", "username"}}
	if _, err := conn.Write([]byte(args.Encode())); err != nil {
		t.Errorf("writing args: %v", err)
		return
	}

	var connect *Instruction
	for {
		inst, err := ReadInstruction(r)
		if err != nil {
			t.Errorf("reading handshake instruction: %v", err)
			return
		}
		if inst.Opcode == "connect" {
			connect = inst
			break
		}
	}
	if connect.Args[0] != "VERSION_1_5_0" {
		t.Errorf("expected mirrored version, got %q", connect.Args[0])
	}
	if connect.Args[1] != "ws-marc.waas-workspaces.svc.cluster.local" || connect.Args[2] != "5901" {
		t.Errorf("unexpected hostname/port: %v", connect.Args)
	}
	if connect.Args[3] != "secret" {
		t.Errorf("expected password to be passed through, got %q", connect.Args[3])
	}

	ready := Instruction{Opcode: "ready", Args: []string{"$conn-id-1"}}
	if _, err := conn.Write([]byte(ready.Encode())); err != nil {
		t.Errorf("writing ready: %v", err)
	}
}

func TestHandshake(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go fakeGuacd(t, server)

	id, reader, err := Handshake(client, ConnectionParams{
		Protocol: "vnc",
		Hostname: "ws-marc.waas-workspaces.svc.cluster.local",
		Port:     5901,
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}
	if id != "$conn-id-1" {
		t.Fatalf("unexpected connection id %q", id)
	}
	if reader == nil {
		t.Fatal("expected a reader for the post-handshake stream")
	}
}

func TestParamValueExtraPrecedence(t *testing.T) {
	params := ConnectionParams{
		Protocol: "vnc",
		Hostname: "ws.svc",
		Port:     5901,
		Password: "server-side",
		Extra: map[string]string{
			"color-depth": "16",
			"password":    "user-supplied",
			"hostname":    "evil.example.com",
			"port":        "22",
		},
	}
	if got := paramValue("color-depth", params); got != "16" {
		t.Fatalf("extra param must be forwarded, got %q", got)
	}
	if got := paramValue("password", params); got != "user-supplied" {
		t.Fatalf("extra must win over built-in credentials, got %q", got)
	}
	if got := paramValue("hostname", params); got != "ws.svc" {
		t.Fatalf("hostname is platform-managed, got %q", got)
	}
	if got := paramValue("port", params); got != "5901" {
		t.Fatalf("port is platform-managed, got %q", got)
	}
	if got := paramValue("unknown", params); got != "" {
		t.Fatalf("unknown params stay empty, got %q", got)
	}
	// VNC audio: the PulseAudio server defaults to the workspace (guacd
	// would otherwise look for a PulseAudio local to ITSELF), and an
	// explicit template/user value still wins.
	if got := paramValue("audio-servername", params); got != "ws.svc" {
		t.Fatalf("audio-servername must default to the workspace host, got %q", got)
	}
	params.Extra["audio-servername"] = "sound.svc"
	if got := paramValue("audio-servername", params); got != "sound.svc" {
		t.Fatalf("extra must win over the audio-servername default, got %q", got)
	}
	if got := paramValue("audio-servername", ConnectionParams{Protocol: "rdp", Hostname: "ws.svc"}); got != "" {
		t.Fatalf("audio-servername default is vnc-only, got %q", got)
	}
}

func TestHandshakeGuacdError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		r := bufio.NewReader(server)
		if _, err := ReadInstruction(r); err != nil {
			return
		}
		errInst := Instruction{Opcode: "error", Args: []string{"unsupported protocol", "0x0100"}}
		_, _ = server.Write([]byte(errInst.Encode()))
	}()

	_, _, err := Handshake(client, ConnectionParams{Protocol: "bogus", Hostname: "h", Port: 1})
	if err == nil {
		t.Fatal("expected handshake to fail on guacd error")
	}
}
