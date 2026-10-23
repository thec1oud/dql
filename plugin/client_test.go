package plugin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

var emitter = irv1.PluginKind_PLUGIN_KIND_EMITTER

func testBundle() *irv1.Bundle {
	return &irv1.Bundle{Version: v1(), Dialect: irv1.Dialect_DIALECT_POSTGRES}
}

// M0 gate: the echo plugin passes the handshake in-process and as an executable.
func TestEchoBothPaths(t *testing.T) {
	ctx := context.Background()
	inproc, err := InProcess(echoPlugin("opaque-types"), hello("opaque-types"), emitter)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := Start(ctx, echoBin, []string{"-features", "opaque-types"}, hello("opaque-types"), emitter)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]Client{"in-process": inproc, "executable": executable} {
		if c.Handshake().GetPlugin().GetName() != "echo" {
			t.Errorf("%s: handshake not kept", name)
		}
		res, err := c.Invoke(ctx, testBundle())
		if err != nil || !proto.Equal(res.GetBundle(), testBundle()) {
			t.Errorf("%s: echo failed: %v", name, err)
		}
		if _, err := c.Invoke(ctx, nil); err == nil || !strings.Contains(err.Error(), "no bundle") {
			t.Errorf("%s: want plugin error, got %v", name, err)
		}
		if err := c.Close(); err != nil {
			t.Errorf("%s: close: %v", name, err)
		}
	}
}

// M0 gate: a plugin with an incompatible version or a missing required
// feature is refused, on both paths.
func TestRefusalBothPaths(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		min   string
		hello *irv1.CoreHello
		want  string
	}{
		{"version", "1.1.0", hello(), "reads IR 1.1.0"},
		{"feature", "1.0.0", hello("cross-catalog"), "lacks required features: cross-catalog"},
	}
	for _, c := range cases {
		_, err := InProcess(echoWith(c.min), c.hello, emitter)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("in-process %s: got %v", c.name, err)
		}
		_, err = Start(ctx, echoBin, []string{"-min", c.min}, c.hello, emitter)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "refused by the core") {
			t.Errorf("executable %s: got %v", c.name, err)
		}
	}
}

func TestStartMissingExecutable(t *testing.T) {
	_, err := Start(context.Background(), "/nonexistent/dql-plugin-x", nil, hello(), emitter)
	if err == nil || !strings.Contains(err.Error(), "starting") {
		t.Fatal(err)
	}
}

func TestStartPluginExitsEarly(t *testing.T) {
	_, err := Start(context.Background(), "true", nil, hello(), emitter)
	if !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

// started returns a process client around a finished command, with fakes for
// its pipes.
func started(t *testing.T, w io.WriteCloser, r io.Reader) *process {
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return &process{cmd: cmd, w: w, r: r, hs: handshake(emitter, "1.0.0", 1)}
}

func TestProcessHandshakeErrors(t *testing.T) {
	result := &irv1.PluginMessage{Kind: &irv1.PluginMessage_Result{Result: &irv1.InvokeResult{}}}
	hs := &irv1.PluginMessage{Kind: &irv1.PluginMessage_Handshake{Handshake: handshake(emitter, "1.0.0", 1)}}
	if err := started(t, &failAfter{}, nil).handshake(hello(), emitter); !errors.Is(err, errWrite) {
		t.Errorf("hello write: %v", err)
	}
	if err := started(t, &failAfter{n: 1}, bytes.NewReader(frames(t, result))).handshake(hello(), emitter); err == nil ||
		!strings.Contains(err.Error(), "expected a handshake") {
		t.Errorf("not a handshake: %v", err)
	}
	if err := started(t, &failAfter{n: 1}, bytes.NewReader(frames(t, hs))).handshake(hello(), emitter); !errors.Is(err, errWrite) {
		t.Errorf("verdict write: %v", err)
	}
}

func TestProcessInvokeErrors(t *testing.T) {
	ctx := context.Background()
	hs := &irv1.PluginMessage{Kind: &irv1.PluginMessage_Handshake{Handshake: handshake(emitter, "1.0.0", 1)}}
	if _, err := started(t, &failAfter{}, nil).Invoke(ctx, testBundle()); !errors.Is(err, errWrite) {
		t.Errorf("write: %v", err)
	}
	if _, err := started(t, &failAfter{n: 1}, bytes.NewReader(nil)).Invoke(ctx, testBundle()); !errors.Is(err, io.EOF) {
		t.Errorf("read: %v", err)
	}
	if _, err := started(t, &failAfter{n: 1}, bytes.NewReader(frames(t, hs))).Invoke(ctx, testBundle()); err == nil ||
		!strings.Contains(err.Error(), "expected a result") {
		t.Errorf("unexpected kind: %v", err)
	}
}

func TestProcessCloseReportsWaitFailure(t *testing.T) {
	c := &process{cmd: exec.Command("true"), w: &failAfter{n: 1}, hs: handshake(emitter, "1.0.0", 1)}
	if err := c.Close(); err == nil || !strings.Contains(err.Error(), "not started") {
		t.Fatal(err)
	}
}
