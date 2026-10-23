package facts

import (
	"slices"
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func routineCatalog(name string, rs ...*irv1.Routine) *irv1.Catalog {
	return &irv1.Catalog{
		Name: ident(name, name), DefaultNamespace: "app",
		Namespaces: []*irv1.Namespace{{Name: ident("app", "app"), Routines: rs}},
	}
}

func hostNames(cs ...*irv1.Catalog) []string {
	var out []string
	for _, c := range cs {
		for _, ns := range c.GetNamespaces() {
			for _, r := range ns.GetRoutines() {
				for _, o := range r.GetOverloads() {
					out = append(out, strings.Join(o.GetSignature(), ",")+"="+strings.Join(o.GetHostName().GetWords(), " "))
				}
			}
		}
	}
	return out
}

func postScore(overloads ...*irv1.Overload) *irv1.Routine {
	return &irv1.Routine{Name: ident("post_score", "post", "score"), Overloads: overloads}
}

func named(o *irv1.Overload, v *irv1.AnnotationValue) *irv1.Overload {
	o.Annotations = append(o.Annotations, configured(keyName, "x", v))
	return o
}

func TestOverloadHostNames(t *testing.T) {
	pid := func() *irv1.Argument { return arg("p_id", i64(), "p", "id") }
	days := func() *irv1.Argument { return arg("window_days", i32(), "window", "days") }
	out := &irv1.Argument{Name: ident("score", "score"), Type: i32(), Mode: irv1.ArgMode_ARG_MODE_OUT}
	for _, tc := range []struct {
		name  string
		r     *irv1.Routine
		want  []string
		diags []string
	}{
		{"one public overload uses the routine's words",
			postScore(publicOverload(pid())), []string{"int64=post score"}, nil},
		{"several append their arguments' logical types",
			postScore(publicOverload(pid(), days()), publicOverload(pid())),
			[]string{"int64=post score int64", "int64,int32=post score int64 int32"}, nil},
		{"OUT arguments do not identify an overload",
			postScore(publicOverload(pid(), out), publicOverload()),
			[]string{"=post score", "int64=post score int64"}, nil},
		{"a name annotation overrides",
			postScore(named(publicOverload(pid(), days()), wordsValue("post", "score", "window")), publicOverload(pid())),
			[]string{"int64=post score int64", "int64,int32=post score window"}, nil},
		{"a remaining collision fails",
			postScore(named(publicOverload(pid(), days()), wordsValue("post", "score", "int64")), publicOverload(pid())),
			[]string{"int64=post score int64", "int64,int32=post score int64"},
			[]string{"ERROR overload-name-collision shop.app.post_score", "ERROR overload-name-collision shop.app.post_score"}},
		{"an invalid name annotation fails and falls back",
			postScore(named(publicOverload(pid()), nil)),
			[]string{"int64=post score"},
			[]string{"ERROR annotation-value-invalid shop.app.post_score"}},
	} {
		c := routineCatalog("shop", tc.r)
		ds := Derive([]*irv1.Catalog{c}, postgres)
		if got := hostNames(c); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
		if got := diagLines(ds); !slices.Equal(got, tc.diags) {
			t.Errorf("%s: diagnostics %v, want %v", tc.name, got, tc.diags)
		}
	}
}

func TestOverloadCollisionMessage(t *testing.T) {
	c := routineCatalog("shop", postScore(
		named(publicOverload(arg("p_id", i64(), "p", "id"), arg("d", i32(), "d")), wordsValue("post", "score", "int64")),
		publicOverload(arg("p_id", i64(), "p", "id"))))
	ds := Derive([]*irv1.Catalog{c}, postgres)
	want := "overload `shop.app.post_score(int64)` gets the host name [post score int64], the same as `shop.app.post_score(int64, int32)`; give it a distinct name annotation"
	if len(ds) != 2 || ds[0].GetMessage() != want {
		t.Errorf("got %v", ds)
	}
}

// §4.3 prefixes: namespace words outside the default namespace, catalog
// words outside the default catalog when the bundle has several.
func TestOverloadPrefixes(t *testing.T) {
	one := func() *irv1.Routine { return postScore(publicOverload(arg("p_id", i64(), "p", "id"))) }
	shopCat := routineCatalog("shop", one())
	audit := &irv1.Namespace{Name: ident("audit", "audit"), Routines: []*irv1.Routine{one()}}
	shopCat.Namespaces = append(shopCat.Namespaces, audit)
	billing := routineCatalog("billing", one())
	if ds := Derive([]*irv1.Catalog{shopCat, billing}, Options{DefaultCatalog: "shop"}); len(ds) != 0 {
		t.Fatal(diagLines(ds))
	}
	want := []string{"int64=post score", "int64=audit post score", "int64=billing post score"}
	if got := hostNames(shopCat, billing); !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}

	// Without a default catalog, the same names in two catalogs collide
	// only when no prefix tells them apart.
	a, b := routineCatalog("shop", one()), routineCatalog("shop", one())
	if got := codes(Derive([]*irv1.Catalog{a, b}, Options{DefaultCatalog: "shop"})); !slices.Equal(got, []string{CodeOverloadNameCollision, CodeOverloadNameCollision}) {
		t.Errorf("got %v", got)
	}
}

func TestLogicalTypeNames(t *testing.T) {
	enumRef := &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: "post_status"}
	domRef := &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: "missing"}
	compRef := &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: "money_amount"}
	types := []*irv1.Type{
		{Kind: &irv1.Type_LocalTime{LocalTime: &irv1.TimeType{}}},
		{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: i64()}}},
		{Kind: &irv1.Type_Range{Range: &irv1.RangeType{Element: i32()}}},
		{Kind: &irv1.Type_Multirange{Multirange: &irv1.RangeType{Element: i32()}}},
		{Kind: &irv1.Type_Weak{Weak: &irv1.Weak{Affinity: text()}}},
		{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: enumRef}}}},
		{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: domRef}}}},
		{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Composite{Composite: compRef}}}},
		{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{}}},
		{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}, Native: &irv1.NativeType{Raw: "citext"}},
		{},
	}
	var overloads []*irv1.Overload
	for i, typ := range types {
		overloads = append(overloads, publicOverload(arg("a", i64(), "a"), arg("b", typ, "b")))
		overloads[i].Arguments[0].Type = &irv1.Type{Kind: &irv1.Type_Int8{Int8: &irv1.Empty{}}}
		for j := 0; j < i; j++ {
			overloads[i].Arguments = append(overloads[i].Arguments, arg("pad", i32(), "pad"))
		}
	}
	c := routineCatalog("shop", &irv1.Routine{Name: ident("f", "f"), Overloads: overloads}, &irv1.Routine{Name: ident("e", "e")})
	ns := c.Namespaces[0]
	ns.Enums = []*irv1.Enum{{Name: ident("post_status", "post", "status")}}
	ns.Domains = []*irv1.Domain{{Name: ident("email_address", "email", "address")}}
	ns.Composites = []*irv1.Composite{{Name: ident("money_amount", "money", "amount")}}
	if ds := Derive([]*irv1.Catalog{c}, postgres); len(ds) != 0 {
		t.Fatal(diagLines(ds))
	}
	got := map[string]string{}
	for _, o := range ns.Routines[1].GetOverloads() {
		w := o.GetHostName().GetWords()
		got[o.GetSignature()[1]] = strings.Join(w[2:len(w)-(len(o.GetSignature())-2)], " ")
	}
	want := map[string]string{
		"local_time":            "local time",
		"array<int64>":          "array int64",
		"range<int32>":          "range int32",
		"multirange<int32>":     "multirange int32",
		"weak<text>":            "weak text",
		"shop.app.post_status":  "post status",
		"shop.app.missing":      "named",
		"shop.app.money_amount": "money amount",
		"":                      "named",
		"opaque<citext>":        "opaque",
		"unspecified":           "unspecified",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%q: got %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %v", got)
	}
}
