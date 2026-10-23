// Package bundle encodes and decodes IR bundles in their two canonical forms:
// binary protobuf on the wire, and protobuf JSON for golden files, debugging
// and committed IR (design record §3.1). Both encodings are byte-for-byte
// deterministic (§3.2).
package bundle

import (
	"bytes"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Version is the IR version this core reads and writes.
func Version() *irv1.IrVersion {
	return &irv1.IrVersion{Major: proto.Uint32(1), Minor: proto.Uint32(0), Patch: proto.Uint32(0)}
}

// MarshalBinary encodes m as deterministic binary protobuf.
func MarshalBinary(m proto.Message) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(m)
}

// UnmarshalBinary decodes binary protobuf into m.
func UnmarshalBinary(b []byte, m proto.Message) error {
	return proto.Unmarshal(b, m)
}

// indent normalizes JSON layout; a variable so tests can make it fail.
var indent = json.Indent

// MarshalJSON encodes m as canonical protobuf JSON: two-space indentation and
// a trailing newline. protojson deliberately varies its whitespace between
// builds, so its output is re-indented to a fixed layout.
func MarshalJSON(m proto.Message) ([]byte, error) {
	raw, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := indent(&buf, raw, "", "  "); err != nil {
		return nil, fmt.Errorf("bundle: normalizing JSON: %w", err)
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// UnmarshalJSON decodes protobuf JSON into m. Unknown fields are an error.
func UnmarshalJSON(b []byte, m proto.Message) error {
	return protojson.Unmarshal(b, m)
}
