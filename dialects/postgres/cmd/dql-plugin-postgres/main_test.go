package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// The plugin's invocations are tested in package postgres, which runs this
// command as an executable.

func session(t *testing.T, msgs ...proto.Message) *bytes.Reader {
	var buf bytes.Buffer
	for _, m := range msgs {
		if err := plugin.WriteMessage(&buf, m); err != nil {
			t.Fatal(err)
		}
	}
	return bytes.NewReader(buf.Bytes())
}

func TestRun(t *testing.T) {
	hello := &irv1.CoreMessage{Kind: &irv1.CoreMessage_Hello{Hello: &irv1.CoreHello{}}}
	ok := &irv1.CoreMessage{Kind: &irv1.CoreMessage_HandshakeResult{HandshakeResult: &irv1.HandshakeResult{Accepted: true}}}
	var stdout, stderr bytes.Buffer
	if code := run(session(t, hello, ok), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	got := &irv1.PluginMessage{}
	if err := plugin.ReadMessage(&stdout, got); err != nil {
		t.Fatal(err)
	}
	if hs := got.GetHandshake(); hs.GetPlugin().GetName() != "postgres" || hs.GetPlugin().GetKind() != irv1.PluginKind_PLUGIN_KIND_DIALECT {
		t.Fatalf("handshake: %v", hs)
	}
}

func TestRunFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(bytes.NewReader(nil), &bytes.Buffer{}, &stderr); code != 1 || !strings.Contains(stderr.String(), "dql-plugin-postgres: EOF") {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
}

func TestMainExits(t *testing.T) {
	defer func(f func(int), stdin *os.File) { exit, os.Stdin = f, stdin }(exit, os.Stdin)
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	os.Stdin = devnull
	code := -1
	exit = func(c int) { code = c }
	main()
	if code != 1 {
		t.Fatalf("exit %d, want 1 on an empty stdin", code)
	}
}
