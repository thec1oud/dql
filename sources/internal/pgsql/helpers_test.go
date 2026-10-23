package pgsql

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// assertJSON fails unless got equals the message that want, in protobuf
// JSON as the design record writes it, describes.
func assertJSON(t *testing.T, label string, got proto.Message, want string) {
	t.Helper()
	exp := got.ProtoReflect().New().Interface()
	if err := protojson.Unmarshal([]byte(want), exp); err != nil {
		t.Fatalf("%s: bad expectation %s: %v", label, want, err)
	}
	if !proto.Equal(got, exp) {
		g, _ := protojson.Marshal(got)
		t.Errorf("%s:\n got  %s\n want %s", label, g, want)
	}
}

func mustUnmarshal(js string, m proto.Message) {
	if err := protojson.Unmarshal([]byte(js), m); err != nil {
		panic(err)
	}
}

// quote returns s as a JSON string.
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
