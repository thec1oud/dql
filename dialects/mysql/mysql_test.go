package mysql_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/thec1oud/dql/core/operators"
	"github.com/thec1oud/dql/dialects/mysql"
	"github.com/thec1oud/dql/plugin"
	mysqlextv1 "github.com/thec1oud/dql/proto/dql/ext/mysql/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestHandshake(t *testing.T) {
	hs := mysql.New().Handshake()
	if p := hs.GetPlugin(); p.GetName() != "mysql" || p.GetKind() != irv1.PluginKind_PLUGIN_KIND_DIALECT || p.GetVersion() != mysql.Version {
		t.Errorf("handshake %v", hs)
	}
}

// The profile names operators of the core's catalog only, sorted, with
// their fidelities (§9.2). MySQL has no arrays or ranges, and its JSON
// paths are not SQL/JSON paths.
func TestProfileOperators(t *testing.T) {
	catalog := map[string]bool{}
	for _, o := range operators.Catalog() {
		catalog[o.GetName()] = true
	}
	p := mysql.Profile()
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
	for _, name := range []string{"array_contains", "array_overlaps", "json_path", "range_contains", "range_overlaps"} {
		if _, ok := fidelity[name]; ok {
			t.Errorf("%s is supported", name)
		}
	}
	for name, want := range map[string]irv1.Fidelity{
		"eq": irv1.Fidelity_FIDELITY_EXACT, "in": irv1.Fidelity_FIDELITY_EXACT, "starts_with": irv1.Fidelity_FIDELITY_EXACT,
		"div": irv1.Fidelity_FIDELITY_APPROXIMATE, "regex_match": irv1.Fidelity_FIDELITY_APPROXIMATE,
		"ilike": irv1.Fidelity_FIDELITY_EMULATED, "json_get": irv1.Fidelity_FIDELITY_EMULATED, "json_contains": irv1.Fidelity_FIDELITY_EMULATED,
	} {
		if fidelity[name] != want {
			t.Errorf("%s is %v, want %v", name, fidelity[name], want)
		}
	}
	if p.GetDialect() != irv1.Dialect_DIALECT_MYSQL || p.GetCrossCatalogReferences() || p.GetReturnedRows() != irv1.ReturnedRows_RETURNED_ROWS_REREAD {
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
	b := &irv1.Bundle{Operators: operators.Catalog(), Capabilities: mysql.Profile(), Catalogs: []*irv1.Catalog{{
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
	if got := names(bin); !slices.Contains(got, "like") || !slices.Contains(got, "starts_with") || slices.Contains(got, "regex_match") {
		t.Errorf("binary collation: %v", got)
	}
	if got := names(ci); slices.Contains(got, "like") || !slices.Contains(got, "eq") {
		t.Errorf("case-insensitive collation: %v", got)
	}
}

func flavored(flavor string) *irv1.Catalog {
	ext, _ := anypb.New(&mysqlextv1.Database{Flavor: flavor})
	return &irv1.Catalog{Name: &irv1.Identifier{Stored: flavor}, Extensions: []*anypb.Any{ext}}
}

func TestInvoke(t *testing.T) {
	ctx := context.Background()
	res, err := mysql.New().Invoke(ctx, &irv1.Invoke{Bundle: &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_MYSQL}})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(res.GetBundle().GetCapabilities(), mysql.Profile()) || len(res.GetBundle().GetCatalogs()) != 0 {
		t.Errorf("result %v", res)
	}
	_, err = mysql.New().Invoke(ctx, &irv1.Invoke{Bundle: &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_POSTGRES}})
	if err == nil || !strings.Contains(err.Error(), "DIALECT_POSTGRES, not MySQL") {
		t.Errorf("a Postgres bundle: %v", err)
	}
	mixed := &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_MYSQL, Catalogs: []*irv1.Catalog{flavored("mysql"), flavored("mariadb")}}
	if _, err = mysql.New().Invoke(ctx, &irv1.Invoke{Bundle: mixed}); err == nil || !strings.Contains(err.Error(), "mixes MySQL and MariaDB") {
		t.Errorf("mixed flavors: %v", err)
	}
	// A MariaDB catalog gets MariaDB's spellings.
	maria := &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_MYSQL, Catalogs: []*irv1.Catalog{flavored("mariadb")}}
	res, err = mysql.New().Invoke(ctx, &irv1.Invoke{Bundle: maria})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.GetBundle().GetQueries().GetFragments().GetFragments() {
		if f.GetId() == "lock.share" && f.GetTemplate() != " LOCK IN SHARE MODE" {
			t.Errorf("lock.share on MariaDB: %q", f.GetTemplate())
		}
	}
}

// The shipped plugin also runs as the executable dql-plugin-mysql, and
// both paths publish the same profile (ADR 0002).
func TestExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "dql-plugin-mysql")
	if msg, err := exec.Command("go", "build", "-o", bin, "./cmd/dql-plugin-mysql").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	ctx := context.Background()
	c, err := plugin.Start(ctx, bin, dir, nil, &irv1.CoreHello{IrVersion: &irv1.IrVersion{Major: proto.Uint32(1), Minor: proto.Uint32(0), Patch: proto.Uint32(0)}}, irv1.PluginKind_PLUGIN_KIND_DIALECT)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Invoke(ctx, &irv1.Invoke{Bundle: &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_MYSQL}})
	if cerr := c.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(res.GetBundle().GetCapabilities(), mysql.Profile()) {
		t.Errorf("the executable published %v", res.GetBundle().GetCapabilities())
	}
}
