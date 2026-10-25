package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// column returns the column of the running example's table.
func column(b *irv1.Bundle, table, name string) *irv1.Column {
	for _, t := range b.GetCatalogs()[0].GetNamespaces()[0].GetTables() {
		for _, c := range t.GetColumns() {
			if t.GetName().GetStored() == table && c.GetName().GetStored() == name {
				return c
			}
		}
	}
	return nil
}

func operatorNames(c *irv1.Column) []string {
	var out []string
	for _, o := range c.GetOperators() {
		out = append(out, o.GetName())
	}
	return out
}

// dql generate adds the operator catalog and the Postgres capability
// profile to the bundle, and each column lists its operators (§9).
func TestGenerateResolvesOperators(t *testing.T) {
	code, stderr, b := generateShapes(t, "operators:\n  allow-approximate: [ilike]\n")
	if code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	if len(b.GetOperators()) != 30 || b.GetCapabilities().GetDialect() != irv1.Dialect_DIALECT_POSTGRES {
		t.Fatalf("operators %d, capabilities %v", len(b.GetOperators()), b.GetCapabilities().GetDialect())
	}
	for _, c := range []struct {
		table, column string
		has, lacks    []string
	}{
		{"users", "email", []string{"eq", "ilike", "like"}, []string{"add", "is_null"}},
		{"users", "display_name", []string{"ilike", "is_null"}, nil},
		{"posts", "labels", []string{"array_contains", "is_null"}, []string{"like"}},
		{"posts", "status", []string{"lt", "in"}, []string{"like", "is_null"}},
		{"users", "version", []string{"add", "between"}, []string{"like"}},
	} {
		got := operatorNames(column(b, c.table, c.column))
		for _, h := range c.has {
			if !slices.Contains(got, h) {
				t.Errorf("%s.%s lacks %s: %v", c.table, c.column, h, got)
			}
		}
		for _, l := range c.lacks {
			if slices.Contains(got, l) {
				t.Errorf("%s.%s has %s: %v", c.table, c.column, l, got)
			}
		}
	}
}

func TestGenerateOperatorFailures(t *testing.T) {
	code, stderr, b := generateShapes(t, "operators:\n  allow-approximate: [fuzzy]\n")
	if code != 1 || b != nil || !strings.Contains(stderr, `error operator-unknown: operators: allow-approximate names "fuzzy"`) {
		t.Errorf("unknown operator: %d %q", code, stderr)
	}

	defer func(d map[irv1.Dialect]func() plugin.Plugin) { dialects = d }(dialects)
	for _, c := range []struct {
		name   string
		plugin plugin.Plugin
		want   string
	}{
		{"none", nil, "dql generate: no dialect plugin serves DIALECT_POSTGRES\n"},
		{"wrong kind", fakeDialect{kind: irv1.PluginKind_PLUGIN_KIND_SOURCE}, "dql generate: dialect fake: "},
		{"invoke fails", fakeDialect{kind: irv1.PluginKind_PLUGIN_KIND_DIALECT, err: errors.New("boom")}, "dql generate: dialect fake: boom\n"},
		{"error diagnostic", fakeDialect{kind: irv1.PluginKind_PLUGIN_KIND_DIALECT,
			ds: []*irv1.Diagnostic{diagnostics.Errorf("dialect-broken", nil, "broken")}}, "error dialect-broken: broken"},
		{"unprefixed operator", fakeDialect{kind: irv1.PluginKind_PLUGIN_KIND_DIALECT,
			ops: []*irv1.Operator{{Name: "jsonb_has_key"}}}, `error operator-unprefixed: plugin fake contributes operator "jsonb_has_key"`},
	} {
		dialects = map[irv1.Dialect]func() plugin.Plugin{}
		if c.plugin != nil {
			dialects[irv1.Dialect_DIALECT_POSTGRES] = func() plugin.Plugin { return c.plugin }
		}
		if code, stderr, b := generateShapes(t, ""); code != 1 || b != nil || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: %d %q", c.name, code, stderr)
		}
	}
}

type fakeDialect struct {
	kind irv1.PluginKind
	err  error
	ds   []*irv1.Diagnostic
	ops  []*irv1.Operator
	// profile is the capability profile it publishes.
	profile *irv1.CapabilityProfile
}

func (f fakeDialect) Handshake() *irv1.PluginHandshake {
	return &irv1.PluginHandshake{
		Plugin:     &irv1.PluginInfo{Name: "fake", Kind: f.kind, Version: "1.0.0"},
		IrVersions: &irv1.IrVersionRange{Min: "1.0.0", MaxMajor: 1},
	}
}

func (f fakeDialect) Invoke(context.Context, *irv1.Invoke) (*irv1.InvokeResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &irv1.InvokeResult{Bundle: &irv1.Bundle{Operators: f.ops, Capabilities: f.profile}, Diagnostics: f.ds}, nil
}

// A dialect plugin's own operators join the catalog under its name, and
// columns get them where its profile supports them (§9.3).
func TestGenerateDialectOperators(t *testing.T) {
	defer func(d map[irv1.Dialect]func() plugin.Plugin) { dialects = d }(dialects)
	text := &irv1.OperandType{Kind: &irv1.OperandType_Type{Type: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}}}
	dialects = map[irv1.Dialect]func() plugin.Plugin{irv1.Dialect_DIALECT_POSTGRES: func() plugin.Plugin {
		return fakeDialect{kind: irv1.PluginKind_PLUGIN_KIND_DIALECT,
			ops: []*irv1.Operator{{Name: "fake.sounds_like", Signature: &irv1.Signature{Args: []*irv1.OperandType{text, text}}}},
			profile: &irv1.CapabilityProfile{Dialect: irv1.Dialect_DIALECT_POSTGRES,
				Operators: []*irv1.OperatorSupport{{Name: "fake.sounds_like", Fidelity: irv1.Fidelity_FIDELITY_EXACT}}}}
	}}
	code, stderr, b := generateShapes(t, "")
	if code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	if got := operatorNames(column(b, "users", "display_name")); !slices.Equal(got, []string{"fake.sounds_like"}) {
		t.Errorf("display_name operators %v", got)
	}
	i := slices.IndexFunc(b.GetOperators(), func(o *irv1.Operator) bool { return o.GetName() == "fake.sounds_like" })
	if i < 0 || b.GetOperators()[i].GetOrigin().GetPlugin() != "fake" {
		t.Errorf("catalog %v", b.GetOperators())
	}
}
