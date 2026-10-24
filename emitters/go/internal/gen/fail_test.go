package gen

import (
	"slices"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func enum(stored string, words []string, labels ...string) *irv1.Enum {
	en := &irv1.Enum{Name: &irv1.Identifier{Stored: stored}, HostName: &irv1.Identifier{Words: words}, Labels: labels}
	for _, l := range labels {
		en.LabelNames = append(en.LabelNames, &irv1.Identifier{Stored: l, Words: strings.Fields(strings.ReplaceAll(l, "_", " "))})
	}
	return en
}

// §4.3, §15.2: names that meet only after Go casing, and generated names
// that meet another object's, fail with go-name-collision.
func TestNameCollisions(t *testing.T) {
	ns := composite(
		attr("i_d", &irv1.Type{Kind: &irv1.Type_Int64{Int64: e()}}, false, "i", "d"),
		attr("id", &irv1.Type{Kind: &irv1.Type_Int64{Int64: e()}}, false),
	)
	ns.Enums = []*irv1.Enum{enum("post_status", []string{"post", "status"}, "draft"), enum("x", []string{"x"})}
	ns.Domains = []*irv1.Domain{
		{Name: &irv1.Identifier{Stored: "post_status_draft"}, HostName: &irv1.Identifier{Words: []string{"post", "status", "draft"}}, Base: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}},
		{Name: &irv1.Identifier{Stored: "x_values"}, HostName: &irv1.Identifier{Words: []string{"x", "values"}}, Base: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}},
	}
	files, codes := generate(t, bundle(ns), Options{})
	if len(files) != 0 {
		t.Errorf("files despite errors")
	}
	want := []string{
		"SEVERITY_ERROR go-name-collision shop.app.every.id",
		"SEVERITY_ERROR go-name-collision shop.app.post_status_draft",
		"SEVERITY_ERROR go-name-collision shop.app.x_values",
	}
	if !slices.Equal(codes, want) {
		t.Fatalf("codes %q", codes)
	}
	_, ds := Generate(bundle(ns), Options{Package: "p"})
	for i, s := range []string{
		"attributes i_d and id of composite type shop.app.every both get the Go name ID",
		`domain shop.app.post_status_draft gets the Go name PostStatusDraft, as label "draft" of enum shop.app.post_status does`,
		"domain shop.app.x_values gets the Go name XValues, as the values function of enum shop.app.x does",
	} {
		if !strings.Contains(ds[i].GetMessage(), s) {
			t.Errorf("message %q lacks %q", ds[i].GetMessage(), s)
		}
	}
}

// A type kind of a newer IR, a reference to a missing named type, and an
// opaque type nothing maps.
func TestUnmappedTypes(t *testing.T) {
	ns := composite(
		attr("future", &irv1.Type{}, false),
		attr("gone", &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Composite{Composite: qn("gone")}}}}, false),
		attr("geo", opaque("public", "geometry", irv1.OpaqueFallback_OPAQUE_FALLBACK_UNSPECIFIED), true),
	)
	_, codes := generate(t, bundle(ns), Options{})
	want := []string{
		"SEVERITY_ERROR go-type-unknown shop.app.every.future",
		"SEVERITY_ERROR go-opaque-unmapped shop.app.every.geo",
		"SEVERITY_ERROR go-type-unknown shop.app.every.gone",
	}
	if !slices.Equal(codes, want) {
		t.Fatalf("codes %q", codes)
	}
	_, ds := Generate(bundle(ns), Options{Package: "p"})
	if m := ds[1].GetMessage(); !strings.Contains(m, "add public.geometry: { go: <type> } under types") {
		t.Errorf("message %q", m)
	}
}

// Diagnostics sort by severity, then target, then code (§3.2).
func TestSortDiagnostics(t *testing.T) {
	d := func(sev irv1.Severity, object, code string) *irv1.Diagnostic {
		return &irv1.Diagnostic{Severity: sev, Code: code, Target: qn(object)}
	}
	ds := []*irv1.Diagnostic{
		d(irv1.Severity_SEVERITY_WARNING, "a", "x"),
		d(irv1.Severity_SEVERITY_ERROR, "b", "y"),
		d(irv1.Severity_SEVERITY_ERROR, "b", "x"),
		d(irv1.Severity_SEVERITY_ERROR, "a", "z"),
	}
	sortDiagnostics(ds)
	var got []string
	for _, x := range ds {
		got = append(got, x.GetTarget().GetObject()+x.GetCode())
	}
	if want := []string{"az", "bx", "by", "ax"}; !slices.Equal(got, want) {
		t.Errorf("order %q", got)
	}
}

func TestRenderPanicsOnInvalidGo(t *testing.T) {
	g := newGenerator(bundle(&irv1.Namespace{}), Options{Package: "p"})
	f := g.newFile("bad")
	f.printf("func {")
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "bad.dql.go does not format") {
			t.Errorf("recover() = %v", r)
		}
	}()
	g.render(f)
}

func TestImportPaths(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"github.com/jackc/pgx/v5/pgtype", "pgtype"},
		{"github.com/jackc/pgx/v5", "pgx"},
		{"example.com/go-thing", "go_thing"},
		{"v2", "v2"},
		{"example.com/vx", "vx"},
	} {
		if got := packageName(c.path); got != c.want {
			t.Errorf("packageName(%s) = %s, want %s", c.path, got, c.want)
		}
	}
	for _, c := range []struct {
		spec       string
		path, name string
		ok         bool
	}{
		{"github.com/x/y.Z", "github.com/x/y", "Z", true},
		{"github.com/x", "", "", false},
		{"string", "", "", false},
	} {
		if p, n, ok := splitQualified(c.spec); p != c.path || n != c.name || ok != c.ok {
			t.Errorf("splitQualified(%s) = %s %s %v", c.spec, p, n, ok)
		}
	}
	// An import whose package name differs from its last element is aliased.
	ns := composite(attr("a", opaque("public", "x", 0), false))
	files, _ := generate(t, bundle(ns), Options{Types: map[string]string{"public.x": "example.com/go-thing.T"}})
	if src := files["types.dql.go"]; !strings.Contains(src, `go_thing "example.com/go-thing"`) || !strings.Contains(src, "A go_thing.T") {
		t.Errorf("alias:\n%s", src)
	}
}

// Every catalog is listed in the header, and a namespace without objects
// generates nothing but the version check.
func TestHeaderAndFiles(t *testing.T) {
	b := bundle(&irv1.Namespace{})
	b.Catalogs = append(b.Catalogs, &irv1.Catalog{Name: &irv1.Identifier{Stored: "billing"},
		Provenance: &irv1.Provenance{Source: "dpg-snapshot", SourceVersion: "0.2.0", InputFingerprint: "sha256:11"}})
	files, codes := generate(t, b, Options{})
	if codes != nil {
		t.Fatal(codes)
	}
	var names []string
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"types.dql.go", "version.dql.go"}) {
		t.Errorf("files %q", names)
	}
	v := files["version.dql.go"]
	if !strings.HasPrefix(v, Header+"\n") || !strings.Contains(v, "// Input: catalog billing from dpg-snapshot 0.2.0, sha256:11\n") ||
		!strings.Contains(v, "_ = dql.EnforceVersion(1 - dql.MinVersion)") {
		t.Errorf("version.dql.go:\n%s", v)
	}
	if strings.Contains(files["types.dql.go"], "import") {
		t.Errorf("types.dql.go imports:\n%s", files["types.dql.go"])
	}
}
