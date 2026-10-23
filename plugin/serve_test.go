package plugin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/plugin/echo"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func echoPlugin(features ...string) Plugin {
	return echo.New(irv1.PluginKind_PLUGIN_KIND_EMITTER, "1.0.0", 1, features...)
}

func helloMsg() *irv1.CoreMessage {
	return &irv1.CoreMessage{Kind: &irv1.CoreMessage_Hello{Hello: hello()}}
}

func verdictMsg(accepted bool) *irv1.CoreMessage {
	return &irv1.CoreMessage{Kind: &irv1.CoreMessage_HandshakeResult{
		HandshakeResult: &irv1.HandshakeResult{Accepted: accepted, Reason: "lacks required features"}}}
}

func invokeMsg(b *irv1.Bundle) *irv1.CoreMessage {
	return &irv1.CoreMessage{Kind: &irv1.CoreMessage_Invoke{Invoke: &irv1.Invoke{Bundle: b}}}
}

func shutdownMsg() *irv1.CoreMessage {
	return &irv1.CoreMessage{Kind: &irv1.CoreMessage_Shutdown{Shutdown: &irv1.Shutdown{}}}
}

func TestServeSession(t *testing.T) {
	b := &irv1.Bundle{Version: v1(), RequiredFeatures: []string{"opaque-types"}}
	in := frames(t, helloMsg(), verdictMsg(true), invokeMsg(b), invokeMsg(nil), shutdownMsg(), invokeMsg(b))
	var out bytes.Buffer
	if err := Serve(context.Background(), echoPlugin(), bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	var got []*irv1.PluginMessage
	for {
		m := &irv1.PluginMessage{}
		if err := ReadMessage(&out, m); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		got = append(got, m)
	}
	if len(got) != 3 {
		t.Fatalf("want handshake and two answers before shutdown, got %d messages", len(got))
	}
	if got[0].GetHandshake().GetPlugin().GetName() != "echo" {
		t.Error("first message is not the handshake")
	}
	if !proto.Equal(got[1].GetResult().GetBundle(), b) {
		t.Error("bundle was not echoed")
	}
	if !strings.Contains(got[2].GetError().GetMessage(), "no bundle") {
		t.Error("invoke failure was not reported as a plugin error")
	}
}

func TestServeEndsAtEOF(t *testing.T) {
	in := frames(t, helloMsg(), verdictMsg(true))
	if err := Serve(context.Background(), echoPlugin(), bytes.NewReader(in), io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestServeRefused(t *testing.T) {
	in := frames(t, helloMsg(), verdictMsg(false))
	var refused *RefusedError
	err := Serve(context.Background(), echoPlugin(), bytes.NewReader(in), io.Discard)
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "lacks required features") {
		t.Fatal(err)
	}
}

func TestServeProtocolErrors(t *testing.T) {
	truncated := append(frames(t, helloMsg(), verdictMsg(true)), 0, 0)
	cases := map[string][]byte{
		"EOF":                       nil,
		"expected hello":            frames(t, shutdownMsg()),
		"expected handshake result": frames(t, helloMsg(), shutdownMsg()),
		"frame length":              truncated,
		"unexpected":                frames(t, helloMsg(), verdictMsg(true), helloMsg()),
	}
	for want, in := range cases {
		err := Serve(context.Background(), echoPlugin(), bytes.NewReader(in), io.Discard)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
	if err := Serve(context.Background(), echoPlugin(), bytes.NewReader(frames(t, helloMsg())), io.Discard); err != io.EOF {
		t.Errorf("no verdict: got %v", err)
	}
}

func TestServeWriteErrors(t *testing.T) {
	in := frames(t, helloMsg(), verdictMsg(true), invokeMsg(&irv1.Bundle{}))
	for n := range 2 {
		err := Serve(context.Background(), echoPlugin(), bytes.NewReader(in), &failAfter{n: n})
		if !errors.Is(err, errWrite) {
			t.Errorf("failing write %d: got %v", n+1, err)
		}
	}
}
