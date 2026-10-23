package echo

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestEcho(t *testing.T) {
	p := New(irv1.PluginKind_PLUGIN_KIND_SOURCE, "1.0.0", 1, "weak-types")
	hs := p.Handshake()
	if hs.GetPlugin().GetName() != "echo" || hs.GetPlugin().GetKind() != irv1.PluginKind_PLUGIN_KIND_SOURCE ||
		hs.GetIrVersions().GetMin() != "1.0.0" || hs.GetIrVersions().GetMaxMajor() != 1 || hs.GetFeatures()[0] != "weak-types" {
		t.Fatalf("handshake: %v", hs)
	}
	b := &irv1.Bundle{RequiredFeatures: []string{"weak-types"}}
	res, err := p.Invoke(context.Background(), &irv1.Invoke{Bundle: b})
	if err != nil || res.GetBundle() != b {
		t.Fatal(res, err)
	}
	if len(res.GetDiagnostics()) != 0 {
		t.Errorf("diagnostics without settings: %v", res.GetDiagnostics())
	}
	settings, _ := structpb.NewStruct(map[string]any{"plugin": "echo", "out": "gen"})
	res, err = p.Invoke(context.Background(), &irv1.Invoke{Bundle: b, Settings: settings})
	if ds := res.GetDiagnostics(); err != nil || len(ds) != 1 || ds[0].GetCode() != "echo-settings" ||
		ds[0].GetMessage() != "map[out:gen plugin:echo]" {
		t.Errorf("settings: %v %v", ds, err)
	}
	if _, err := p.Invoke(context.Background(), &irv1.Invoke{}); err == nil {
		t.Fatal("want an error without a bundle")
	}
}
