package plugin

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestFrameRoundTrip(t *testing.T) {
	in := &irv1.CoreMessage{Kind: &irv1.CoreMessage_Hello{Hello: hello("weak-types")}}
	r := bytes.NewReader(frames(t, in, in))
	for range 2 {
		out := &irv1.CoreMessage{}
		if err := ReadMessage(r, out); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(in, out) {
			t.Fatal("round trip changed the message")
		}
	}
	if err := ReadMessage(r, &irv1.CoreMessage{}); err != io.EOF {
		t.Fatalf("want io.EOF at end of stream, got %v", err)
	}
}

func TestWriteMessageErrors(t *testing.T) {
	bad := &irv1.PluginError{Message: "\xff"}
	if err := WriteMessage(&bytes.Buffer{}, bad); err == nil || !strings.Contains(err.Error(), "encoding") {
		t.Fatal(err)
	}
	if err := WriteMessage(&failAfter{}, &irv1.Shutdown{}); !errors.Is(err, errWrite) {
		t.Fatal(err)
	}
}

func TestReadMessageErrors(t *testing.T) {
	huge := binary.BigEndian.AppendUint32(nil, MaxMessageSize+1)
	cases := map[string][]byte{
		"frame length": {0, 0},
		"exceeds":      huge,
		"frame body":   {0, 0, 0, 5, 1},
		"decoding":     {0, 0, 0, 1, 0xff},
	}
	for want, in := range cases {
		err := ReadMessage(bytes.NewReader(in), &irv1.CoreMessage{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}
