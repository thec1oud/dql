// Package plugin implements the plugin protocol: length-prefixed protobuf
// messages over a plugin's stdin and stdout, the handshake, and version and
// feature negotiation (design record §2.3, §3.3). Shipped plugins may also run
// in-process through the same Plugin interface.
package plugin

import (
	"encoding/binary"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

// MaxMessageSize bounds one framed message, so a corrupt length prefix fails
// fast instead of allocating without limit.
const MaxMessageSize = 1 << 30

// WriteMessage writes m as a 4-byte big-endian length followed by its
// deterministic binary encoding.
func WriteMessage(w io.Writer, m proto.Message) error {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		return fmt.Errorf("plugin: encoding message: %w", err)
	}
	frame := binary.BigEndian.AppendUint32(make([]byte, 0, 4+len(b)), uint32(len(b)))
	if _, err := w.Write(append(frame, b...)); err != nil {
		return fmt.Errorf("plugin: writing message: %w", err)
	}
	return nil
}

// ReadMessage reads one framed message into m. It returns io.EOF only when the
// stream ends cleanly before a new frame.
func ReadMessage(r io.Reader, m proto.Message) error {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		if err == io.EOF {
			return io.EOF
		}
		return fmt.Errorf("plugin: reading frame length: %w", err)
	}
	n := binary.BigEndian.Uint32(head[:])
	if n > MaxMessageSize {
		return fmt.Errorf("plugin: frame of %d bytes exceeds the %d-byte limit", n, MaxMessageSize)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return fmt.Errorf("plugin: reading frame body: %w", err)
	}
	if err := proto.Unmarshal(b, m); err != nil {
		return fmt.Errorf("plugin: decoding message: %w", err)
	}
	return nil
}
