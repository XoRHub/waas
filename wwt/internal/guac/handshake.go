package guac

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ConnectionParams describes the desktop guacd should connect to, plus the
// client display characteristics forwarded from the browser.
type ConnectionParams struct {
	Protocol string // "vnc", "rdp" or "ssh"
	Hostname string
	Port     int32
	Username string
	Password string

	// Extra are additional guacd connection parameters resolved by the
	// API server (template params merged with vetted user overrides).
	// They win over the built-in defaults but never over the
	// platform-managed hostname/port.
	Extra map[string]string

	Width  int
	Height int
	DPI    int
}

// Handshake drives the client side of the guacd handshake:
// select → args → size/audio/video/image → connect → ready.
// It returns the guacd connection ID and the buffered reader to keep using
// for the guacd→client direction (it may hold bytes past the handshake).
func Handshake(rw io.ReadWriter, params ConnectionParams) (string, *bufio.Reader, error) {
	r := bufio.NewReader(rw)

	if err := send(rw, Instruction{Opcode: "select", Args: []string{params.Protocol}}); err != nil {
		return "", nil, err
	}

	args, err := ReadInstruction(r)
	if err != nil {
		return "", nil, fmt.Errorf("reading args from guacd: %w", err)
	}
	if args.Opcode != "args" {
		return "", nil, fmt.Errorf("expected args instruction, got %q", args.Opcode)
	}

	width, height, dpi := params.Width, params.Height, params.DPI
	if width <= 0 {
		width = 1920
	}
	if height <= 0 {
		height = 1080
	}
	if dpi <= 0 {
		dpi = 96
	}
	for _, inst := range []Instruction{
		{Opcode: "size", Args: []string{strconv.Itoa(width), strconv.Itoa(height), strconv.Itoa(dpi)}},
		{Opcode: "audio", Args: []string{"audio/L16"}},
		{Opcode: "video", Args: []string{}},
		{Opcode: "image", Args: []string{"image/png", "image/jpeg", "image/webp"}},
	} {
		if err := send(rw, inst); err != nil {
			return "", nil, err
		}
	}

	// The connect values must line up with the parameter names guacd
	// announced. Since protocol 1.1.0 the first announced name is the
	// version, which we mirror back.
	values := make([]string, len(args.Args))
	for i, name := range args.Args {
		values[i] = paramValue(name, params)
	}
	if err := send(rw, Instruction{Opcode: "connect", Args: values}); err != nil {
		return "", nil, err
	}

	ready, err := ReadInstruction(r)
	if err != nil {
		return "", nil, fmt.Errorf("reading ready from guacd: %w", err)
	}
	if ready.Opcode == "error" {
		return "", nil, fmt.Errorf("guacd refused the connection: %s", strings.Join(ready.Args, " "))
	}
	if ready.Opcode != "ready" || len(ready.Args) == 0 {
		return "", nil, fmt.Errorf("expected ready instruction, got %q", ready.Opcode)
	}
	return ready.Args[0], r, nil
}

func paramValue(name string, params ConnectionParams) string {
	switch {
	case strings.HasPrefix(name, "VERSION_"):
		return name
	// hostname/port are platform-managed: guacd must always dial the
	// workspace service, whatever extra params say.
	case name == "hostname":
		return params.Hostname
	case name == "port":
		return strconv.Itoa(int(params.Port))
	}
	if v, ok := params.Extra[name]; ok {
		return v
	}
	switch {
	case name == "username":
		return params.Username
	case name == "password":
		return params.Password
	case name == "ignore-cert" && params.Protocol == "rdp":
		return "true"
	default:
		return ""
	}
}

func send(w io.Writer, inst Instruction) error {
	if _, err := io.WriteString(w, inst.Encode()); err != nil {
		return fmt.Errorf("sending %s to guacd: %w", inst.Opcode, err)
	}
	return nil
}
