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

// The plugin's invocations are tested in package goemit, which runs this
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
	args := []string{"-dialect", "postgres", "-package", "shopdb", "-out", "/tmp/x", "-type", "public.citext=string", "-type", "public.ltree=string"}
	if code := run(args, session(t, hello, ok), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	got := &irv1.PluginMessage{}
	if err := plugin.ReadMessage(&stdout, got); err != nil {
		t.Fatal(err)
	}
	if hs := got.GetHandshake(); hs.GetPlugin().GetName() != "go" || hs.GetPlugin().GetKind() != irv1.PluginKind_PLUGIN_KIND_EMITTER {
		t.Fatalf("handshake: %v", hs)
	}
}

func TestRunFailures(t *testing.T) {
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"-nope"}, 2, "flag provided but not defined"},
		{[]string{"-type", "citext"}, 2, "want namespace.type=GoType"},
		{nil, 1, "dql-plugin-go: EOF"},
	}
	for _, c := range cases {
		var stderr bytes.Buffer
		code := run(c.args, bytes.NewReader(nil), &bytes.Buffer{}, &stderr)
		if code != c.code || !strings.Contains(stderr.String(), c.want) {
			t.Errorf("%v: exit %d, stderr %q", c.args, code, stderr.String())
		}
	}
}

func TestMainExits(t *testing.T) {
	defer func(f func(int), args []string, stdin *os.File) { exit, os.Args, os.Stdin = f, args, stdin }(exit, os.Args, os.Stdin)
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	os.Args, os.Stdin = []string{"dql-plugin-go"}, devnull
	code := -1
	exit = func(c int) { code = c }
	main()
	if code != 1 {
		t.Fatalf("exit %d, want 1 on an empty stdin", code)
	}
}
