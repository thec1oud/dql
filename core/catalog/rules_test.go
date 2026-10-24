package catalog

import (
	"cmp"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/annotate"
	"github.com/thec1oud/dql/core/facts"
	"github.com/thec1oud/dql/core/types"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

const postgres = irv1.Dialect_DIALECT_POSTGRES

// small is a catalog with one namespace, which is its default.
func small(name, namespace string, tables ...*irv1.Table) *irv1.Catalog {
	return &irv1.Catalog{
		Name:             ident(name),
		Provenance:       &irv1.Provenance{Source: "pg-live"},
		DefaultNamespace: namespace,
		Namespaces:       []*irv1.Namespace{{Name: ident(namespace), Tables: tables}},
	}
}

func items() *irv1.Table {
	t := table("items", col("id", tInt64()), col("note?", tText()))
	t.Uniqueness = []*irv1.Uniqueness{pk("items_pkey", "id")}
	return t
}

func output(d irv1.Dialect, cats ...*irv1.Catalog) SourceOutput {
	return SourceOutput{Dialect: d, Catalogs: cats}
}

// wantLines fails unless the diagnostics are exactly want.
func wantLines(t *testing.T, ds []*irv1.Diagnostic, want ...string) {
	t.Helper()
	if got := lines(ds); !slices.Equal(got, want) {
		t.Errorf("diagnostics:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// assembleOK assembles and fails on any diagnostic.
func assembleOK(t *testing.T, opts Options, in ...SourceOutput) *irv1.Bundle {
	t.Helper()
	b, ds := Assemble(in, opts)
	if b == nil || len(ds) != 0 {
		t.Fatalf("diagnostics = %v", lines(ds))
	}
	return b
}

// assembleFails assembles, expects failure, and returns the diagnostics.
func assembleFails(t *testing.T, opts Options, in ...SourceOutput) []*irv1.Diagnostic {
	t.Helper()
	b, ds := Assemble(in, opts)
	if b != nil {
		t.Fatalf("Assemble succeeded with %v", lines(ds))
	}
	return ds
}

// §5.2: catalogs from several source invocations merge into one bundle,
// sorted by name (§3.2).
func TestMergeCatalogs(t *testing.T) {
	b := assembleOK(t, Options{DefaultCatalog: "shop"},
		output(postgres, small("shop", "app", items())),
		output(postgres, small("billing", "public", table("invoices", col("id", tInt64())))),
		output(postgres))
	if b.GetDialect() != postgres || len(b.GetCatalogs()) != 2 || b.GetCatalogs()[0].GetName().GetStored() != "billing" {
		t.Fatalf("bundle = %v", b)
	}
	// §4.3: billing is not the default catalog, so its objects are prefixed.
	if w := b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0].GetHostName().GetWords(); !slices.Equal(w, []string{"billing", "invoices"}) {
		t.Errorf("billing.public.invoices host name = %v", w)
	}
	if w := b.GetCatalogs()[1].GetNamespaces()[0].GetTables()[0].GetHostName().GetWords(); !slices.Equal(w, []string{"items"}) {
		t.Errorf("shop.app.items host name = %v", w)
	}
	// The order of the source invocations does not matter (§3.2).
	reversed := assembleOK(t, Options{DefaultCatalog: "shop"},
		output(postgres, small("billing", "public", table("invoices", col("id", tInt64())))),
		output(postgres, small("shop", "app", items())))
	if !proto.Equal(b, reversed) {
		t.Error("output depends on the order of the sources")
	}
}

func TestMergeFailures(t *testing.T) {
	cases := []struct {
		name string
		in   []SourceOutput
		opts Options
		want []string
	}{
		{"no catalogs", []SourceOutput{output(postgres)}, Options{},
			[]string{"ERROR catalog-none "}},
		{"no dialect", []SourceOutput{output(irv1.Dialect_DIALECT_UNSPECIFIED, small("shop", "app"))}, Options{},
			[]string{"ERROR catalog-dialect-missing shop"}},
		{"mixed dialects", []SourceOutput{
			output(postgres, small("shop", "app")),
			output(irv1.Dialect_DIALECT_MYSQL, small("billing", ""), small("audit", ""))}, Options{},
			[]string{"ERROR catalog-dialect-mixed audit", "ERROR catalog-dialect-mixed billing", "ERROR catalog-dialect-mixed shop"}},
		{"unnamed catalog", []SourceOutput{output(postgres, &irv1.Catalog{})}, Options{},
			[]string{"ERROR catalog-name-missing "}},
		{"duplicate names", []SourceOutput{
			output(postgres, small("shop", "app")),
			output(postgres, small("shop", "app"))}, Options{},
			[]string{"ERROR catalog-name-duplicate shop"}},
		{"unknown default catalog", []SourceOutput{output(postgres, small("shop", "app", items()))}, Options{DefaultCatalog: "billing"},
			[]string{"ERROR default-catalog-unknown billing"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantLines(t, assembleFails(t, c.opts, c.in...), c.want...)
		})
	}
	ds := assembleFails(t, Options{}, output(postgres, small("shop", "app")),
		output(irv1.Dialect_DIALECT_MYSQL, small("billing", "")))
	if !strings.Contains(ds[0].GetMessage(), "postgres (shop) and mysql (billing)") {
		t.Errorf("message = %s", ds[0].GetMessage())
	}
	unnamed := small("shop", "app")
	unnamed.Provenance = nil
	ds = assembleFails(t, Options{}, output(postgres, small("shop", "app")), output(postgres, unnamed))
	if !strings.Contains(ds[0].GetMessage(), "(unnamed) and pg-live") {
		t.Errorf("message = %s", ds[0].GetMessage())
	}
}

// Diagnostics a source reported are carried into the bundle (§3.1).
func TestSourceDiagnostics(t *testing.T) {
	src := output(postgres, small("shop", "app", items()))
	warn := &irv1.Diagnostic{Severity: irv1.Severity_SEVERITY_WARNING, Code: "source-note", Target: qn("shop", "app", "items")}
	src.Diagnostics = []*irv1.Diagnostic{warn}
	b, ds := Assemble([]SourceOutput{src}, Options{})
	wantLines(t, b.GetDiagnostics(), "WARNING source-note shop.app.items")
	wantLines(t, ds, "WARNING source-note shop.app.items")
}

// §4.2: the core computes words for every identifier, replacing whatever a
// source sent.
func TestWordsAreComputed(t *testing.T) {
	tb := table("OrderLines", col("HTTPServer_log", tText()))
	tb.Name.Words = []string{"wrong"}
	b := assembleOK(t, Options{}, output(postgres, small("shop", "app", tb)))
	got := b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0]
	if !slices.Equal(got.GetName().GetWords(), []string{"order", "lines"}) ||
		!slices.Equal(got.GetColumns()[0].GetName().GetWords(), []string{"http", "server", "log"}) ||
		!slices.Equal(got.GetHostName().GetWords(), []string{"order", "lines"}) {
		t.Errorf("table = %v", got)
	}
}

// §4.3: a namespace other than the default is a prefix.
func TestNamespacePrefix(t *testing.T) {
	c := small("shop", "app", items())
	c.Namespaces = append(c.Namespaces, &irv1.Namespace{Name: ident("audit"), Tables: []*irv1.Table{table("users", col("id", tInt64()))}})
	b := assembleOK(t, Options{}, output(postgres, c))
	if w := namespace(t, b, "shop", "audit").GetTables()[0].GetHostName().GetWords(); !slices.Equal(w, []string{"audit", "users"}) {
		t.Errorf("host name = %v", w)
	}
}

// §4.3, §5.2: host types of every catalog share one package.
func TestHostNameCollisions(t *testing.T) {
	// A table and an enum of one namespace.
	c := small("shop", "app", table("order_lines", col("id", tInt64())))
	c.Namespaces[0].Enums = []*irv1.Enum{{Name: ident("OrderLines"), Labels: []string{"a"}}}
	ds := assembleFails(t, Options{}, output(postgres, c))
	wantLines(t, ds, "ERROR host-name-collision shop.app.OrderLines", "ERROR host-name-collision shop.app.order_lines")
	if !strings.Contains(ds[0].GetMessage(), "[order lines], the same as shop.app.order_lines") {
		t.Errorf("message = %s", ds[0].GetMessage())
	}

	// Across catalogs: billing's prefix meets a shop table's own words.
	shop := small("shop", "app", table("billing_invoices", col("id", tInt64())))
	billing := small("billing", "public", table("invoices", col("id", tInt64())))
	wantLines(t, assembleFails(t, Options{DefaultCatalog: "shop"}, output(postgres, shop, billing)),
		"ERROR host-name-collision billing.public.invoices", "ERROR host-name-collision shop.app.billing_invoices")

	// A name annotation renames one side, and prefixes apply to it too.
	shop = small("shop", "app", table("billing_invoices", col("id", tInt64())))
	src := output(postgres, shop, small("billing", "public", table("invoices", col("id", tInt64()))))
	src.Config = map[string][]*irv1.Annotation{"shop.app.billing_invoices": {configured("shop.app.billing_invoices", "name", wordsValue("shop", "invoice"))}}
	b := assembleOK(t, Options{DefaultCatalog: "shop"}, src)
	if w := namespace(t, b, "shop", "app").GetTables()[0].GetHostName().GetWords(); !slices.Equal(w, []string{"shop", "invoice"}) {
		t.Errorf("host name = %v", w)
	}
	// Without a default catalog every object takes its catalog's prefix.
	b = assembleOK(t, Options{}, output(postgres, small("shop", "app", items()), small("billing", "public")))
	if w := namespace(t, b, "shop", "app").GetTables()[0].GetHostName().GetWords(); !slices.Equal(w, []string{"shop", "items"}) {
		t.Errorf("host name = %v", w)
	}
}

// §4.3: columns of a table collide on words (core/facts), and so do the
// fields of a composite type and of a result table.
func TestFieldCollisions(t *testing.T) {
	wantLines(t, assembleFails(t, Options{}, output(postgres, small("shop", "app", table("t", col("user_id", tInt64()), col("userId", tInt64()))))),
		"ERROR column-name-collision shop.app.t.user_id")

	c := small("shop", "app")
	c.Namespaces[0].Composites = []*irv1.Composite{{Name: ident("pair"), Attributes: numbered(col("a_b?", tText()), col("aB?", tText()))}}
	c.Namespaces[0].Routines = []*irv1.Routine{{Name: ident("f"), Overloads: []*irv1.Overload{{
		Kind: irv1.RoutineKind_ROUTINE_KIND_FUNCTION,
		Returns: &irv1.Returns{Kind: &irv1.Returns_Table{Table: &irv1.ReturnTable{
			Columns: numbered(col("x_y?", tText()), col("xY?", tText()))}}},
	}}}}
	ds := assembleFails(t, Options{}, output(postgres, c))
	wantLines(t, ds, "ERROR column-name-collision shop.app.f.xY", "ERROR column-name-collision shop.app.pair.aB")
	if !strings.Contains(ds[1].GetMessage(), "attributes aB and a_b of composite type shop.app.pair") {
		t.Errorf("message = %s", ds[1].GetMessage())
	}

	if !strings.Contains(ds[0].GetMessage(), "in the function's definition") {
		t.Errorf("message = %s", ds[0].GetMessage())
	}
}

// §1.3: config annotations attach to the object their key names.
func TestConfigTargets(t *testing.T) {
	c := small("shop", "app", items())
	c.Namespaces[0].Tables[0].Constraints = []*irv1.Constraint{
		membershipCheck("items_note_check", []string{"note"}, "a", "b"),
		{Name: "items_excl", Kind: irv1.ConstraintKind_CONSTRAINT_KIND_EXCLUSION, Form: &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: "EXCLUDE"}}},
	}
	c.Namespaces[0].Tables[0].ForeignKeys = nil
	c.Namespaces[0].Enums = []*irv1.Enum{{Name: ident("mood"), Labels: []string{"ok"}}}
	c.Namespaces[0].Domains = []*irv1.Domain{{Name: ident("code"), Base: tText(), Nullable: true,
		Constraints: []*irv1.Constraint{lengthCheck("code_check", nil, nil, proto.Uint32(3))}}}
	c.Namespaces[0].Composites = []*irv1.Composite{{Name: ident("pair"), Attributes: numbered(col("a?", tText()))}}
	c.Namespaces[0].Sequences = []*irv1.Sequence{{Name: ident("items_id_seq"), Type: tInt64()}}
	c.Namespaces[0].Routines = []*irv1.Routine{
		{Name: ident("score"), Overloads: []*irv1.Overload{
			{Kind: irv1.RoutineKind_ROUTINE_KIND_FUNCTION, Arguments: []*irv1.Argument{{Name: ident("a"), Type: tInt64()}},
				Returns: &irv1.Returns{Kind: &irv1.Returns_Nothing{Nothing: empty()}}},
			{Kind: irv1.RoutineKind_ROUTINE_KIND_FUNCTION, Arguments: []*irv1.Argument{{Name: ident("a"), Type: tInt64()}, {Name: ident("b"), Type: tText()}},
				Returns: &irv1.Returns{Kind: &irv1.Returns_Nothing{Nothing: empty()}}},
		}},
		{Name: ident("refresh"), Overloads: []*irv1.Overload{{Kind: irv1.RoutineKind_ROUTINE_KIND_PROCEDURE,
			Returns: &irv1.Returns{Kind: &irv1.Returns_Nothing{Nothing: empty()}}}}},
	}
	src := output(postgres, c)
	src.Config = map[string][]*irv1.Annotation{}
	add := func(key, ann string, v *irv1.AnnotationValue) {
		src.Config[key] = append(src.Config[key], configured(key, ann, v))
	}
	add("shop.app", "x.go.package", stringValue("shopdb"))
	add("shop.app.items.note", "nonnull", nil)
	add("shop.app.items.items_note_check", "enum", stringValue("note_kind"))
	add("shop.app.items.items_excl", "x.go.skip", nil)
	add("shop.app.mood", "name", stringValue("feeling"))
	add("shop.app.code", "name", stringValue("short_code"))
	add("shop.app.code.code_check", "x.go.message", stringValue("too long"))
	add("shop.app.pair.a", "nonnull", nil)
	add("shop.app.items_id_seq", "name", stringValue("item_ids"))
	add("shop.app.score(int64)", "name", wordsValue("score", "one"))
	add("shop.app.score(int64, text)", "name", wordsValue("score", "two"))
	add("shop.app.refresh", "x.go.skip", nil)
	b := assembleOK(t, Options{}, src)
	ns := b.GetCatalogs()[0].GetNamespaces()[0]
	keys := func(anns []*irv1.Annotation) string {
		var ks []string
		for _, a := range anns {
			ks = append(ks, a.GetKey())
		}
		return strings.Join(ks, ",")
	}
	tb := ns.GetTables()[0]
	got := []string{
		keys(ns.GetAnnotations()),
		keys(tb.GetColumns()[1].GetAnnotations()),
		keys(tb.GetConstraints()[0].GetAnnotations()),
		keys(tb.GetConstraints()[1].GetAnnotations()),
		keys(ns.GetEnums()[0].GetAnnotations()),
		keys(ns.GetDomains()[0].GetConstraints()[0].GetAnnotations()),
		keys(ns.GetComposites()[0].GetAttributes()[0].GetAnnotations()),
		keys(ns.GetRoutines()[0].GetOverloads()[0].GetAnnotations()),
	}
	want := []string{"x.go.package", "core.nonnull", "x.go.skip", "core.enum", "core.name", "x.go.message", "core.nonnull", "x.go.skip"}
	if !slices.Equal(got, want) {
		t.Errorf("annotations = %v", got)
	}
	for _, w := range [][]string{
		ns.GetEnums()[0].GetHostName().GetWords(),
		ns.GetDomains()[0].GetHostName().GetWords(),
		ns.GetSequences()[0].GetHostName().GetWords(),
		ns.GetRoutines()[1].GetOverloads()[0].GetHostName().GetWords(),
		ns.GetRoutines()[1].GetOverloads()[1].GetHostName().GetWords(),
	} {
		got = append(got, strings.Join(w, " "))
	}
	if g := got[len(want):]; !slices.Equal(g, []string{"feeling", "short code", "item ids", "score one", "score two"}) {
		t.Errorf("host names = %v", g)
	}
	if tb.GetColumns()[1].GetNullable() || ns.GetComposites()[0].GetAttributes()[0].GetNullable() {
		t.Error("nonnull did not narrow")
	}
}

func TestConfigTargetFailures(t *testing.T) {
	c := small("shop", "app", items())
	c.Namespaces[0].Tables[0].Constraints = []*irv1.Constraint{opaqueCheck("note", []string{"note"}, "note <> ''")}
	c.Namespaces[0].Routines = []*irv1.Routine{{Name: ident("score"), Overloads: []*irv1.Overload{
		{Kind: irv1.RoutineKind_ROUTINE_KIND_FUNCTION, Returns: &irv1.Returns{Kind: &irv1.Returns_Nothing{Nothing: empty()}}},
		{Kind: irv1.RoutineKind_ROUTINE_KIND_FUNCTION, Arguments: []*irv1.Argument{{Name: ident("a"), Type: tInt64()}},
			Returns: &irv1.Returns{Kind: &irv1.Returns_Nothing{Nothing: empty()}}},
	}}}
	src := output(postgres, c)
	src.Config = map[string][]*irv1.Annotation{}
	for _, k := range []string{"shop.app.missing", "shop.app.items.nope.deeper.still", "shop.app.items.note", "shop.app.score", "shop"} {
		src.Config[k] = []*irv1.Annotation{configured(k, "x.go.skip", nil)}
	}
	ds := assembleFails(t, Options{}, src)
	wantLines(t, ds,
		"ERROR annotation-target-unknown shop",
		"ERROR annotation-target-unknown shop.app.items.nope.deeper.still",
		"ERROR annotation-target-ambiguous shop.app.items.note",
		"ERROR annotation-target-unknown shop.app.missing",
		"ERROR annotation-target-ambiguous shop.app.score")
	if !strings.Contains(ds[2].GetMessage(), "names check constraint shop.app.items.note and column shop.app.items.note") ||
		!strings.Contains(ds[4].GetMessage(), "function shop.app.score() and function shop.app.score(int64)") {
		t.Errorf("messages = %s | %s", ds[2].GetMessage(), ds[4].GetMessage())
	}
}

// §7.1, §7.3: annotations are merged over each other and checked; any
// failure stops the derivations of core/facts, which read them.
func TestAnnotationChecks(t *testing.T) {
	c := small("shop", "app", items())
	c.Namespaces[0].Tables[0].Columns[0].Annotations = []*irv1.Annotation{
		comment("nonnull", "COMMENT ON COLUMN app.items.id", nil),
	}
	c.Namespaces[0].Tables[0].Columns[1].Annotations = []*irv1.Annotation{
		comment("version", "COMMENT ON COLUMN app.items.note", nil),
		{Key: "core.immutable"},
		{Key: "x.go", Origin: &irv1.Origin{Kind: irv1.OriginKind_ORIGIN_DB_COMMENT}},
	}
	ds := assembleFails(t, Options{}, output(postgres, c))
	wantLines(t, ds,
		"ERROR annotation-key-invalid shop.app.items.note",
		"ERROR annotation-origin-missing shop.app.items.note",
		"ERROR annotation-wrong-target shop.app.items.note",
		"WARNING annotation-redundant shop.app.items.id",
		"WARNING annotation-redundant shop.app.items.note")

	// A redundant annotation alone is a warning carried in the bundle.
	c = small("shop", "app", items())
	c.Namespaces[0].Tables[0].Columns[0].Annotations = []*irv1.Annotation{comment("nonnull", "COMMENT ON COLUMN app.items.id", nil)}
	b, ds := Assemble([]SourceOutput{output(postgres, c)}, Options{})
	wantLines(t, ds, "WARNING annotation-redundant shop.app.items.id")
	wantLines(t, b.GetDiagnostics(), "WARNING annotation-redundant shop.app.items.id")
	// It changes nothing: the database's guarantee stays its source.
	if s := b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0].GetColumns()[0].GetNullabilitySource(); s != irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE {
		t.Errorf("nullability source = %v", s)
	}
}

// §6.4, §11.1: a logical key over a column nonnull narrows passes; over a
// nullable column it fails, reported once, by annotate.Check.
func TestLogicalKey(t *testing.T) {
	v := view("v", col("id?", tInt64()), col("note?", tText()))
	v.Columns[0].Annotations = []*irv1.Annotation{comment("nonnull", "COMMENT ON COLUMN app.v.id", nil)}
	v.Annotations = []*irv1.Annotation{comment("logical-key", "COMMENT ON VIEW app.v", listValue("id"))}
	b := assembleOK(t, Options{}, output(postgres, small("shop", "app", v)))
	assertContains(t, b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0], `{"logicalKey": {"columns": ["id"], "origin": "ORIGIN_DB_COMMENT"}}`)
	if k := facts.Key(b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0]); !slices.Equal(k, []string{"id"}) {
		t.Errorf("key = %v", k)
	}

	v = view("v", col("id?", tInt64()))
	v.Annotations = []*irv1.Annotation{comment("logical-key", "COMMENT ON VIEW app.v", listValue("id"))}
	wantLines(t, assembleFails(t, Options{}, output(postgres, small("shop", "app", v))),
		"ERROR "+annotate.CodeLogicalKeyNullable+" shop.app.v")
}

// §7.3, through Assemble: the facts the checks need are the ones this
// package reads from the catalog. Each check has a passing and a failing
// case.
func TestTargetFacts(t *testing.T) {
	assemble := func(tb *irv1.Table, domains ...*irv1.Domain) []*irv1.Diagnostic {
		c := small("shop", "app", tb)
		c.Namespaces[0].Domains = domains
		_, ds := Assemble([]SourceOutput{output(postgres, c)}, Options{})
		return ds
	}
	// enum needs a check recognized as a single-column membership test (§8.4).
	tb := table("orders", col("status", tText()))
	tb.Constraints = []*irv1.Constraint{
		membershipCheck("orders_status_check", []string{"status"}, "new", "paid"),
		opaqueCheck("orders_status_len", []string{"status"}, "CHECK (length(status) > octet_length(status) - 4)"),
	}
	tb.Constraints[0].Annotations = []*irv1.Annotation{comment("enum", "COMMENT ON CONSTRAINT", stringValue("order_status"))}
	tb.Constraints[1].Annotations = []*irv1.Annotation{comment("enum", "COMMENT ON CONSTRAINT", stringValue("order_len"))}
	wantLines(t, assemble(tb), "ERROR "+annotate.CodeEnumNotMembership+" shop.app.orders.orders_status_len")

	// version needs an integer column, through a domain (§8.5).
	counter := &irv1.Domain{Name: ident("counter"), Base: tInt32(), Nullable: true}
	label := &irv1.Domain{Name: ident("label"), Base: tText(), Nullable: true}
	tb = table("items", col("version", tDomain(app("counter"))), col("note", tDomain(app("label"))))
	tb.Columns[0].Annotations = []*irv1.Annotation{comment("version", "COMMENT ON COLUMN app.items.version", nil)}
	tb.Columns[1].Annotations = []*irv1.Annotation{comment("version", "COMMENT ON COLUMN app.items.note", nil)}
	wantLines(t, assemble(tb, counter, label), "ERROR annotation-wrong-target shop.app.items.note")

	// logical-key designates the key of a table without a primary key
	// (§11.1).
	keyed, unkeyed := items(), table("events", col("id", tInt64()))
	unkeyed.Uniqueness = []*irv1.Uniqueness{unique("events_id_key", "id")}
	for _, tb := range []*irv1.Table{keyed, unkeyed} {
		tb.Annotations = []*irv1.Annotation{comment("logical-key", "COMMENT ON TABLE", stringValue("id"))}
	}
	wantLines(t, assemble(keyed), "ERROR annotation-wrong-target shop.app.items")
	wantLines(t, assemble(unkeyed))

	// bound and version on one column (§7.3).
	tb = table("users", col("tenant_id", tInt64()), col("version", tInt32()))
	tb.Columns[0].Annotations = []*irv1.Annotation{comment("bound", "COMMENT ON COLUMN app.users.tenant_id", nil)}
	tb.Columns[1].Annotations = []*irv1.Annotation{comment("version", "COMMENT ON COLUMN app.users.version", nil)}
	wantLines(t, assemble(tb))
	tb.Columns[1].Annotations = append(tb.Columns[1].Annotations, comment("bound", "COMMENT ON COLUMN app.users.version", nil))
	wantLines(t, assemble(tb), "ERROR "+annotate.CodeBoundWithVersion+" shop.app.users.version")
}

// §8.6: array-elements-nonnull narrows an array column's elements; on any
// other column it fails.
func TestArrayElementsNonNull(t *testing.T) {
	tb := table("t", col("tags?", tArray(tText())), col("name", tText()))
	tb.Columns[0].Annotations = []*irv1.Annotation{comment("array-elements-nonnull", "COMMENT ON COLUMN app.t.tags", nil)}
	b := assembleOK(t, Options{}, output(postgres, small("shop", "app", tb)))
	got := b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0].GetColumns()[0]
	if got.GetType().GetArray().GetElementNullable() || !got.GetNullable() {
		t.Errorf("column = %v", got)
	}

	tb = table("t", col("name", tText()))
	tb.Columns[0].Annotations = []*irv1.Annotation{comment("array-elements-nonnull", "COMMENT ON COLUMN app.t.name", nil)}
	wantLines(t, assembleFails(t, Options{}, output(postgres, small("shop", "app", tb))),
		"ERROR annotation-wrong-target shop.app.t.name")
}

// §7.2: nonnull on a function narrows its one result value; on a function
// returning a table it fails, since its result columns are narrowed instead.
func TestFunctionResultNonNull(t *testing.T) {
	routine := func(o *irv1.Overload) *irv1.Catalog {
		c := small("shop", "app")
		o.Annotations = append(o.Annotations, comment("nonnull", "COMMENT ON FUNCTION app.f()", nil))
		c.Namespaces[0].Routines = []*irv1.Routine{{Name: ident("f"), Overloads: []*irv1.Overload{o}}}
		return c
	}
	for name, o := range map[string]*irv1.Overload{
		"scalar": overload("COMMENT ON FUNCTION app.f()"),
		"set of": {Kind: irv1.RoutineKind_ROUTINE_KIND_FUNCTION, Returns: &irv1.Returns{Kind: &irv1.Returns_SetOf{
			SetOf: &irv1.ReturnValue{Type: tText(), Nullable: true}}}},
	} {
		b := assembleOK(t, Options{}, output(postgres, routine(o)))
		got := b.GetCatalogs()[0].GetNamespaces()[0].GetRoutines()[0].GetOverloads()[0].GetReturns()
		if v := cmp.Or(got.GetScalar(), got.GetSetOf()); v.GetNullable() {
			t.Errorf("%s: returns = %v", name, got)
		}
	}

	table := &irv1.Overload{Kind: irv1.RoutineKind_ROUTINE_KIND_FUNCTION, Returns: &irv1.Returns{Kind: &irv1.Returns_Table{
		Table: &irv1.ReturnTable{Columns: []*irv1.Column{col("score", tText())}}}}}
	wantLines(t, assembleFails(t, Options{}, output(postgres, routine(table))),
		"ERROR annotation-wrong-target shop.app.f")
}

// §4.2, §7.6: a words annotation written as a string is split by the core;
// a list stays as written, and a value of the wrong form fails.
func TestWordValues(t *testing.T) {
	tb := table("t", col("id", tInt64()))
	tb.Annotations = []*irv1.Annotation{comment("name", "COMMENT ON TABLE app.t", stringValue("BlogPost"))}
	// A source may send an annotation with what it already superseded.
	tb.Annotations[0].Superseded = []*irv1.Annotation{{Key: "core.name", Value: stringValue("old_name"),
		Origin: &irv1.Origin{Kind: irv1.OriginKind_ORIGIN_INFERRED}}}
	tb.Columns[0].Annotations = []*irv1.Annotation{comment("name", "COMMENT ON COLUMN app.t.id", wordsValue("key"))}
	b := assembleOK(t, Options{}, output(postgres, small("shop", "app", tb)))
	got := b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0]
	assertContains(t, got, `{"hostName": {"words": ["blog", "post"]}, "annotations": [{"value": {"words": ["blog", "post"]},
	  "superseded": [{"value": {"words": ["old", "name"]}}]}]}`)
	assertContains(t, got.GetColumns()[0], `{"hostName": {"words": ["key"]}}`)

	tb = table("t", col("id", tInt64()))
	tb.Annotations = []*irv1.Annotation{comment("name", "COMMENT ON TABLE app.t", wordsValue("a", ""))}
	wantLines(t, assembleFails(t, Options{}, output(postgres, small("shop", "app", tb))),
		"ERROR annotation-value-invalid shop.app.t")
}

// §1.3, §5.3, §7.4: list and struct values written as text, as comments
// write them, are read by their key's form; struct names are split into
// words. Text that is not a value of the form fails, once.
func TestTextValues(t *testing.T) {
	users := table("users", col("id", tInt64()), col("tenant_id", tInt64()), col("slug", tText()))
	users.Uniqueness = []*irv1.Uniqueness{unique("users_id_key", "id"), unique("users_tenant_slug_key", "tenant_id", "slug")}
	users.Annotations = []*irv1.Annotation{comment("logical-key", "COMMENT ON TABLE app.users", stringValue(" tenant_id,slug "))}
	posts := table("posts", col("id", tInt64()), col("user_id?", tInt64()))
	posts.Uniqueness = []*irv1.Uniqueness{pk("posts_pkey", "id")}
	posts.Columns[1].Annotations = []*irv1.Annotation{comment("relation", "COMMENT ON COLUMN app.posts.user_id",
		stringValue("to: shop.app.users.id, name: Writer, inverse-name: \"written posts\""))}
	b := assembleOK(t, Options{}, output(postgres, small("shop", "app", users, posts)))
	ns := namespace(t, b, "shop", "app")
	assertContains(t, tableOf(t, ns, "users"), `{"annotations": [{"key": "core.logical-key", "value": {"list": ["tenant_id", "slug"]}}],
	  "logicalKey": {"columns": ["tenant_id", "slug"], "origin": "ORIGIN_DB_COMMENT"}}`)
	assertContains(t, columnOf(t, tableOf(t, ns, "posts"), "user_id"), `{"annotations": [{"key": "core.relation",
	  "value": {"struct": {"to": "shop.app.users.id", "name": ["writer"], "inverse-name": ["written", "posts"]}}}]}`)
	assertContains(t, relationOf(t, tableOf(t, ns, "posts"), "writer"), `{"origin": "RELATION_ORIGIN_ANNOTATED"}`)
	assertContains(t, relationOf(t, tableOf(t, ns, "users"), "written_posts"), `{"name": {"words": ["written", "posts"]}}`)

	for _, c := range []struct{ key, text, want string }{
		{"logical-key", "tenant_id,", "item 2: it is empty"},
		{"logical-key", `"tenant_id`, "a double quote is never closed"},
		{"relation", "shop.app.users.id", "has no colon"},
	} {
		tb := table("users", col("id", tInt64()), col("tenant_id", tInt64()))
		on := &tb.Annotations
		if c.key == "relation" {
			on = &tb.Columns[1].Annotations
		}
		*on = []*irv1.Annotation{comment(c.key, "COMMENT", stringValue(c.text))}
		ds := assembleFails(t, Options{}, output(postgres, small("shop", "app", tb)))
		if len(ds) != 1 || ds[0].GetCode() != annotate.CodeValueMalformed || !strings.Contains(ds[0].GetMessage(), c.want) {
			t.Errorf("@%s(%s): %v", c.key, c.text, ds)
		}
	}

	// A superseded annotation is read the same way.
	tb := table("users", col("id", tInt64()))
	tb.Uniqueness = []*irv1.Uniqueness{unique("users_id_key", "id")}
	tb.Annotations = []*irv1.Annotation{comment("logical-key", "COMMENT", stringValue("id"))}
	tb.Annotations[0].Superseded = []*irv1.Annotation{{Key: "core.logical-key", Value: stringValue(","),
		Origin: &irv1.Origin{Kind: irv1.OriginKind_ORIGIN_INFERRED, Location: "default"}}}
	ds := assembleFails(t, Options{}, output(postgres, small("shop", "app", tb)))
	wantLines(t, ds, "ERROR annotation-value-malformed shop.app.users")
	if !strings.Contains(ds[0].GetMessage(), `core.logical-key on shop.app.users, read at default, is written ",", which is not a list value`) {
		t.Errorf("message: %s", ds[0].GetMessage())
	}
}

// §5.3: a relation declared in config on MySQL crosses catalogs, with the
// namespace left out of the key; its names are split into words.
func TestCrossCatalogRelation(t *testing.T) {
	users := table("users", col("id", tInt64()))
	users.Uniqueness = []*irv1.Uniqueness{pk("PRIMARY", "id")}
	invoices := table("invoices", col("id", tInt64()), col("user_id", tInt64()))
	invoices.Uniqueness = []*irv1.Uniqueness{pk("PRIMARY", "id")}
	src := func(d irv1.Dialect) SourceOutput {
		s := output(d, small("shop", "", proto.CloneOf(users)), small("billing", "", proto.CloneOf(invoices)))
		s.Config = map[string][]*irv1.Annotation{"billing.invoices.user_id": {configured("billing.invoices.user_id", "relation",
			structValue(map[string]any{"to": "shop.users.id", "name": "user", "inverse-name": "invoices"}))}}
		return s
	}
	b := assembleOK(t, Options{CrossCatalogReferences: true, DefaultCatalog: "shop"}, src(irv1.Dialect_DIALECT_MYSQL))
	// The IR writes §5.3's cardinality as the relation's kind.
	assertContains(t, relationOf(t, namespace(t, b, "billing", "").GetTables()[0], "user"), `{
	  "name": { "stored": "user", "words": ["user"] },
	  "kind": "RELATION_KIND_MANY_TO_ONE",
	  "origin": "RELATION_ORIGIN_ANNOTATED",
	  "to": {
	    "table": { "catalog": "shop", "namespace": "", "object": "users" },
	    "columns": ["id"]
	  },
	  "crossCatalog": true
	}`)
	assertContains(t, relationOf(t, namespace(t, b, "shop", "").GetTables()[0], "invoices"), `{
	  "name": { "stored": "invoices", "words": ["invoices"] }
	}`)
	if !slices.Equal(b.GetRequiredFeatures(), []string{types.FeatureCrossCatalog}) {
		t.Errorf("required features = %v", b.GetRequiredFeatures())
	}
	ds := assembleFails(t, Options{DefaultCatalog: "shop"}, src(postgres))
	wantLines(t, ds, "ERROR relation-cross-catalog billing.invoices.user")
}

// §5.2: a view dependency may cross catalogs only where the dialect allows.
func TestCrossCatalogDependency(t *testing.T) {
	v := view("recent", col("id?", tInt64()))
	v.DependsOn = []*irv1.QualifiedName{qn("shop", "app", "items"), qn("", "app", "items"), qn("billing", "public", "local")}
	in := func() SourceOutput {
		return output(postgres, small("shop", "app", items()), small("billing", "public", proto.CloneOf(v)))
	}
	ds := assembleFails(t, Options{DefaultCatalog: "shop"}, in())
	wantLines(t, ds, "ERROR dependency-cross-catalog billing.public.recent")
	if !strings.Contains(ds[0].GetMessage(), "dialect postgres cannot") {
		t.Errorf("message = %s", ds[0].GetMessage())
	}
	b := assembleOK(t, Options{DefaultCatalog: "shop", CrossCatalogReferences: true}, in())
	if !slices.Equal(b.GetRequiredFeatures(), []string{types.FeatureCrossCatalog}) {
		t.Errorf("required features = %v", b.GetRequiredFeatures())
	}
	// Sorted by qualified name (§3.2).
	deps := namespace(t, b, "billing", "public").GetTables()[0].GetDependsOn()
	if deps[0].GetCatalog() != "" || deps[1].GetCatalog() != "billing" {
		t.Errorf("depends on = %v", deps)
	}
}

// §8.3, §8, §10: the type and constraint checks run on the assembled
// catalogs.
func TestTypeAndConstraintChecks(t *testing.T) {
	tb := table("t", col("x?", tOpaque("public", "citext")), col("y", tDomain(qn("shop", "app", "nowhere"))))
	tb.Constraints = []*irv1.Constraint{{Name: "t_check", Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK, Columns: []string{"x"}}}
	wantLines(t, assembleFails(t, Options{}, output(postgres, small("shop", "app", tb))),
		"ERROR constraint-form-missing shop.app.t.t_check",
		"ERROR type-opaque-unmapped shop.app.t.x",
		"ERROR type-reference-unresolved shop.app.t.y")

	tb = table("t", col("x?", tOpaque("public", "citext")))
	b, ds := Assemble([]SourceOutput{output(postgres, small("shop", "app", tb))}, Options{OpaqueFallback: types.FallbackString})
	wantLines(t, ds, "WARNING type-opaque-fallback shop.app.t.x")
	if !slices.Equal(b.GetRequiredFeatures(), []string{types.FeatureOpaqueTypes}) {
		t.Errorf("required features = %v", b.GetRequiredFeatures())
	}
}

// §3.2 rule 2: what the core sorts besides core/facts' lists.
func TestSorting(t *testing.T) {
	c := small("shop", "app", items())
	c.Provenance.FidelityLimits = []string{"z", "a"}
	c.Namespaces = append(c.Namespaces, &irv1.Namespace{Name: ident("aaa")})
	ns := c.Namespaces[0]
	ns.Enums = []*irv1.Enum{{Name: ident("b"), Labels: []string{"x"}}, {Name: ident("a"), Labels: []string{"x"}}}
	ns.Composites = []*irv1.Composite{{Name: ident("d")}, {Name: ident("c")}}
	ns.Sequences = []*irv1.Sequence{{Name: ident("s2"), Type: tInt64()}, {Name: ident("s1"), Type: tInt64()}}
	ns.Domains = []*irv1.Domain{{Name: ident("dom"), Base: tText(), Constraints: []*irv1.Constraint{
		lengthCheck("dom_b", nil, nil, proto.Uint32(2)), lengthCheck("dom_a", nil, nil, proto.Uint32(1))}}}
	b := assembleOK(t, Options{}, output(postgres, c))
	got := b.GetCatalogs()[0]
	n := namespace(t, b, "shop", "app")
	if !slices.Equal(got.GetProvenance().GetFidelityLimits(), []string{"a", "z"}) ||
		got.GetNamespaces()[0].GetName().GetStored() != "aaa" ||
		n.GetEnums()[0].GetName().GetStored() != "a" ||
		n.GetComposites()[0].GetName().GetStored() != "c" ||
		n.GetSequences()[0].GetName().GetStored() != "s1" ||
		n.GetDomains()[0].GetConstraints()[0].GetName() != "dom_a" {
		t.Errorf("catalog = %v", got)
	}
}

// §7.3: a foreign key with a NOT NULL column cannot unlink; one whose
// columns are all nullable can.
func TestForeignKeyTarget(t *testing.T) {
	child := func(nullable bool) *irv1.Table {
		name := "parent_id"
		if nullable {
			name += "?"
		}
		tb := table("child", col("id", tInt64()), col(name, tInt64()))
		tb.Uniqueness = []*irv1.Uniqueness{pk("child_pkey", "id")}
		tb.ForeignKeys = []*irv1.ForeignKey{fk("child_parent_id_fkey", []string{"parent_id"}, qn("shop", "app", "items"), "id")}
		tb.ForeignKeys[0].Annotations = []*irv1.Annotation{comment("removal", "COMMENT ON CONSTRAINT", stringValue("unlink"))}
		return tb
	}
	assembleOK(t, Options{}, output(postgres, small("shop", "app", items(), child(true))))
	ds := assembleFails(t, Options{}, output(postgres, small("shop", "app", items(), child(false))))
	wantLines(t, ds, "ERROR annotation-wrong-target shop.app.child.child_parent_id_fkey")

	// A key column the table does not have proves nothing.
	tb := child(true)
	tb.ForeignKeys[0].Columns = []string{"ghost"}
	tb.ForeignKeys[0].References.Columns = []string{"id"}
	ds = assembleFails(t, Options{}, output(postgres, small("shop", "app", items(), tb)))
	if slices.Contains(lines(ds), "ERROR annotation-wrong-target shop.app.child.child_parent_id_fkey") {
		t.Errorf("diagnostics = %v", lines(ds))
	}
}

// §4.2, §8.4: every label gets its word split; labels without words, or with
// equal words, fail.
func TestEnumLabelNames(t *testing.T) {
	c := small("shop", "app")
	c.Namespaces[0].Enums = []*irv1.Enum{{Name: ident("post_status"), Labels: []string{"draft", "in-review", "HTTPError"}}}
	b := assembleOK(t, Options{}, output(postgres, c))
	var got []string
	for _, l := range namespace(t, b, "shop", "app").GetEnums()[0].GetLabelNames() {
		got = append(got, l.GetStored()+"="+strings.Join(l.GetWords(), " "))
	}
	if want := []string{"draft=draft", "in-review=in review", "HTTPError=http error"}; !slices.Equal(got, want) {
		t.Errorf("label names = %q", got)
	}

	c = small("shop", "app")
	c.Namespaces[0].Enums = []*irv1.Enum{{Name: ident("mood"), Labels: []string{"in_progress", "+", "in-progress", "InProgress", "ok"}}}
	ds := assembleFails(t, Options{}, output(postgres, c))
	wantLines(t, ds, "ERROR enum-label-unnamed shop.app.mood.+", "ERROR enum-label-collision shop.app.mood.InProgress")
	for i, s := range []string{`label "+" of enum shop.app.mood has no letters`, "labels InProgress, in-progress and in_progress of enum shop.app.mood all have the words [in progress]"} {
		if !strings.Contains(ds[i].GetMessage(), s) {
			t.Errorf("message = %s", ds[i].GetMessage())
		}
	}
}
