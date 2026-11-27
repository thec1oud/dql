package postgres_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/operators"
	"github.com/thec1oud/dql/dialects/postgres"
	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestHandshake(t *testing.T) {
	hs := postgres.New().Handshake()
	if p := hs.GetPlugin(); p.GetName() != "postgres" || p.GetKind() != irv1.PluginKind_PLUGIN_KIND_DIALECT || p.GetVersion() != postgres.Version {
		t.Errorf("handshake %v", hs)
	}
}

// The profile names every operator of the core's catalog, sorted, and only
// those (§9.2).
func TestProfileCoversTheCatalog(t *testing.T) {
	var want, got []string
	for _, o := range operators.Catalog() {
		want = append(want, o.GetName())
	}
	p := postgres.Profile()
	for _, s := range p.GetOperators() {
		got = append(got, s.GetName())
		if s.GetFidelity() != irv1.Fidelity_FIDELITY_EXACT {
			t.Errorf("%s is %v, want exact", s.GetName(), s.GetFidelity())
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("operators\n got %v\nwant %v", got, want)
	}
	if p.GetDialect() != irv1.Dialect_DIALECT_POSTGRES || p.GetCrossCatalogReferences() {
		t.Errorf("profile %v", p)
	}
}

// A text column under a nondeterministic collation gets no pattern
// operators; one under a deterministic collation gets them all.
func TestTextOperatorsNeedADeterministicCollation(t *testing.T) {
	column := func(d irv1.Tristate) *irv1.Column {
		return &irv1.Column{Type: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}, Collation: &irv1.Collation{Deterministic: d}}
	}
	det, nondet := column(irv1.Tristate_TRISTATE_TRUE), column(irv1.Tristate_TRISTATE_FALSE)
	b := &irv1.Bundle{Operators: operators.Catalog(), Capabilities: postgres.Profile(), Catalogs: []*irv1.Catalog{{
		Namespaces: []*irv1.Namespace{{Tables: []*irv1.Table{{Columns: []*irv1.Column{det, nondet}}}}},
	}}}
	if ds := operators.Resolve(b, operators.Options{}); ds != nil {
		t.Fatal(ds)
	}
	names := func(c *irv1.Column) string {
		var out []string
		for _, o := range c.GetOperators() {
			out = append(out, o.GetName())
		}
		return strings.Join(out, " ")
	}
	if got := names(det); got != "any between contains eq ge gt ilike in is_distinct_from le like lt ne regex_match starts_with" {
		t.Errorf("deterministic: %s", got)
	}
	if got := names(nondet); got != "any between eq ge gt in is_distinct_from le lt ne" {
		t.Errorf("nondeterministic: %s", got)
	}
}

func TestInvoke(t *testing.T) {
	res, err := postgres.New().Invoke(context.Background(), &irv1.Invoke{Bundle: &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_POSTGRES}})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(res.GetBundle().GetCapabilities(), postgres.Profile()) || len(res.GetBundle().GetCatalogs()) != 0 {
		t.Errorf("result %v", res)
	}
	_, err = postgres.New().Invoke(context.Background(), &irv1.Invoke{Bundle: &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_MYSQL}})
	if err == nil || !strings.Contains(err.Error(), "DIALECT_MYSQL, not Postgres") {
		t.Errorf("a MySQL bundle: %v", err)
	}
}

// The shipped plugin also runs as the executable dql-plugin-postgres, and
// both paths publish the same profile (ADR 0002).
func TestExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "dql-plugin-postgres")
	if msg, err := exec.Command("go", "build", "-o", bin, "./cmd/dql-plugin-postgres").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	ctx := context.Background()
	c, err := plugin.Start(ctx, bin, dir, nil, &irv1.CoreHello{IrVersion: &irv1.IrVersion{Major: proto.Uint32(1), Minor: proto.Uint32(0), Patch: proto.Uint32(0)}}, irv1.PluginKind_PLUGIN_KIND_DIALECT)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Invoke(ctx, &irv1.Invoke{Bundle: &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_POSTGRES}})
	if cerr := c.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(res.GetBundle().GetCapabilities(), postgres.Profile()) {
		t.Errorf("the executable published %v", res.GetBundle().GetCapabilities())
	}
}
