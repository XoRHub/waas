// Package guac implements the client side of the Guacamole protocol
// handshake with guacd. After the handshake the proxy pipes raw bytes; only
// the handshake needs actual protocol awareness.
package guac

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// Instruction is one Guacamole protocol instruction: an opcode followed by
// arguments, wire-encoded as `LEN.VALUE,LEN.VALUE,…;`.
type Instruction struct {
	Opcode string
	Args   []string
}

// Encode renders the instruction in wire format.
func (i Instruction) Encode() string {
	var b strings.Builder
	writeElement(&b, i.Opcode)
	for _, arg := range i.Args {
		b.WriteByte(',')
		writeElement(&b, arg)
	}
	b.WriteByte(';')
	return b.String()
}

func writeElement(b *strings.Builder, value string) {
	// Lengths count Unicode code points, not bytes.
	fmt.Fprintf(b, "%d.%s", len([]rune(value)), value)
}

// ReadInstruction parses the next instruction from the stream.
func ReadInstruction(r *bufio.Reader) (*Instruction, error) {
	var elements []string
	for {
		lengthStr, err := r.ReadString('.')
		if err != nil {
			return nil, fmt.Errorf("reading element length: %w", err)
		}
		length, err := strconv.Atoi(strings.TrimSuffix(lengthStr, "."))
		if err != nil {
			return nil, fmt.Errorf("parsing element length %q: %w", lengthStr, err)
		}

		value := make([]rune, 0, length)
		for len(value) < length {
			ch, _, err := r.ReadRune()
			if err != nil {
				return nil, fmt.Errorf("reading element value: %w", err)
			}
			value = append(value, ch)
		}
		elements = append(elements, string(value))

		sep, err := r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("reading element separator: %w", err)
		}
		switch sep {
		case ',':
			// next element follows
		case ';':
			return &Instruction{Opcode: elements[0], Args: elements[1:]}, nil
		default:
			return nil, fmt.Errorf("unexpected separator %q", sep)
		}
	}
}
