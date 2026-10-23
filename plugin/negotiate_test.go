package plugin

import (
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func handshake(kind irv1.PluginKind, min string, maxMajor uint32, features ...string) *irv1.PluginHandshake {
	return &irv1.PluginHandshake{
		Plugin:     &irv1.PluginInfo{Name: "go", Kind: kind, Version: "1.4.0"},
		IrVersions: &irv1.IrVersionRange{Min: min, MaxMajor: maxMajor},
		Features:   features,
	}
}

func TestNegotiate(t *testing.T) {
	emitter := irv1.PluginKind_PLUGIN_KIND_EMITTER
	v := func(major, minor, patch uint32) *irv1.CoreHello {
		return &irv1.CoreHello{IrVersion: &irv1.IrVersion{Major: &major, Minor: &minor, Patch: &patch}}
	}
	cases := []struct {
		name  string
		hello *irv1.CoreHello
		hs    *irv1.PluginHandshake
		want  string // empty: accepted
	}{
		{"accepted", hello("opaque-types"), handshake(emitter, "1.0.0", 1, "weak-types", "opaque-types"), ""},
		{"later minor", v(1, 3, 0), handshake(emitter, "1.2.0", 1), ""},
		{"no plugin", hello(), &irv1.PluginHandshake{}, "names no plugin"},
		{"wrong kind", hello(), handshake(irv1.PluginKind_PLUGIN_KIND_SOURCE, "1.0.0", 1), "is a PLUGIN_KIND_SOURCE plugin"},
		{"short version", hello(), handshake(emitter, "1.0", 1), "invalid IR version"},
		{"bad version", hello(), handshake(emitter, "1.x.0", 1), "invalid IR version"},
		{"below min", v(1, 1, 9), handshake(emitter, "1.2.0", 1), "reads IR 1.2.0 up to major 1"},
		{"above max major", v(2, 0, 0), handshake(emitter, "1.0.0", 1), "bundle is IR 2.0.0"},
		{"missing features", hello("weak-types", "cross-catalog"), handshake(emitter, "1.0.0", 1, "weak-types"), "lacks required features: cross-catalog"},
	}
	for _, c := range cases {
		err := Negotiate(c.hello, c.hs, emitter)
		if c.want == "" && err != nil {
			t.Errorf("%s: unexpected refusal: %v", c.name, err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}
