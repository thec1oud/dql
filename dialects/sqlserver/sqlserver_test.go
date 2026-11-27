package sqlserver_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/operators"
	"github.com/thec1oud/dql/dialects/sqlserver"
	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestHandshake(t *testing.T) {
	hs := sqlserver.New().Handshake()
	if p := hs.GetPlugin(); p.GetName() != "sqlserver" || p.GetKind() != irv1.PluginKind_PLUGIN_KIND_DIALECT || p.GetVersion() != sqlserver.Version {
		t.Errorf("handshake %v", hs)
	}
}

// The profile names operators of the core's catalog only, sorted, with
// their fidelities (§9.2). SQL Server 2022 has no arrays, ranges, regular
// expressions or JSON operators.
func TestProfileOperators(t *testing.T) {
	catalog := map[string]bool{}
	for _, o := range operators.Catalog() {
		catalog[o.GetName()] = true
	}
	p := sqlserver.Profile()
	var names []string
	fidelity := map[string]irv1.Fidelity{}
	for _, s := range p.GetOperators() {
		names = append(names, s.GetName())
		fidelity[s.GetName()] = s.GetFidelity()
		if !catalog[s.GetName()] {
			t.Errorf("%s is not in the catalog", s.GetName())
		}
	}
	if !slices.IsSorted(names) {
		t.Error("operators are not sorted")
	}
	for _, name := range []string{"array_contains", "array_overlaps", "json_path", "json_contains", "json_get", "range_contains", "range_overlaps", "regex_match"} {
		if _, ok := fidelity[name]; ok {
			t.Errorf("%s is supported", name)
		}
	}
	for name, want := range map[string]irv1.Fidelity{
		"eq": irv1.Fidelity_FIDELITY_EXACT, "is_distinct_from": irv1.Fidelity_FIDELITY_EXACT, "starts_with": irv1.Fidelity_FIDELITY_EXACT,
		"like": irv1.Fidelity_FIDELITY_EXACT, "in": irv1.Fidelity_FIDELITY_EXACT, "any": irv1.Fidelity_FIDELITY_EXACT,
		"ilike": irv1.Fidelity_FIDELITY_APPROXIMATE,
	} {
		if fidelity[name] != want {
			t.Errorf("%s is %v, want %v", name, fidelity[name], want)
		}
	}
	if p.GetDialect() != irv1.Dialect_DIALECT_SQLSERVER || p.GetCrossCatalogReferences() || p.GetReturnedRows() != irv1.ReturnedRows_RETURNED_ROWS_CLAUSE {
		t.Errorf("profile %v", p)
	}
}

// A text column under a case-insensitive collation gets no pattern
// operators; one under a binary collation gets them.
func TestTextOperatorsNeedADeterministicCollation(t *testing.T) {
	column := func(d irv1.Tristate) *irv1.Column {
		return &irv1.Column{Type: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}, Collation: &irv1.Collation{Deterministic: d}}
	}
	bin, ci := column(irv1.Tristate_TRISTATE_TRUE), column(irv1.Tristate_TRISTATE_FALSE)
	b := &irv1.Bundle{Operators: operators.Catalog(), Capabilities: sqlserver.Profile(), Catalogs: []*irv1.Catalog{{
		Namespaces: []*irv1.Namespace{{Tables: []*irv1.Table{{Columns: []*irv1.Column{bin, ci}}}}},
	}}}
	if ds := operators.Resolve(b, operators.Options{}); ds != nil {
		t.Fatal(ds)
	}
	names := func(c *irv1.Column) (out []string) {
		for _, o := range c.GetOperators() {
			out = append(out, o.GetName())
		}
		return out
	}
	if got := names(bin); !slices.Contains(got, "like") || !slices.Contains(got, "starts_with") || !slices.Contains(got, "contains") {
		t.Errorf("binary collation: %v", got)
	}
	if got := names(ci); slices.Contains(got, "like") || slices.Contains(got, "ilike") || !slices.Contains(got, "eq") {
		t.Errorf("case-insensitive collation: %v", got)
	}
}

func TestInvoke(t *testing.T) {
	ctx := context.Background()
	res, err := sqlserver.New().Invoke(ctx, &irv1.Invoke{Bundle: &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_SQLSERVER}})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(res.GetBundle().GetCapabilities(), sqlserver.Profile()) || len(res.GetBundle().GetCatalogs()) != 0 {
		t.Errorf("result %v", res)
	}
	_, err = sqlserver.New().Invoke(ctx, &irv1.Invoke{Bundle: &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_POSTGRES}})
	if err == nil || !strings.Contains(err.Error(), "DIALECT_POSTGRES, not SQL Server") {
		t.Errorf("a Postgres bundle: %v", err)
	}
}

// The shipped plugin also runs as the executable dql-plugin-sqlserver, and
// both paths publish the same profile (ADR 0002).
func TestExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "dql-plugin-sqlserver")
	if msg, err := exec.Command("go", "build", "-o", bin, "./cmd/dql-plugin-sqlserver").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	ctx := context.Background()
	c, err := plugin.Start(ctx, bin, dir, nil, &irv1.CoreHello{IrVersion: &irv1.IrVersion{Major: proto.Uint32(1), Minor: proto.Uint32(0), Patch: proto.Uint32(0)}}, irv1.PluginKind_PLUGIN_KIND_DIALECT)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Invoke(ctx, &irv1.Invoke{Bundle: &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_SQLSERVER}})
	if cerr := c.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(res.GetBundle().GetCapabilities(), sqlserver.Profile()) {
		t.Errorf("the executable published %v", res.GetBundle().GetCapabilities())
	}
}
