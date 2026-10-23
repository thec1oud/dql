package constraints

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func id(s string) *irv1.Identifier { return &irv1.Identifier{Stored: s, Words: strings.Split(s, "_")} }

func ty(kind string) *irv1.Type {
	t := &irv1.Type{}
	m := t.ProtoReflect()
	fd := m.Descriptor().Fields().ByName(protoreflect.Name(kind))
	m.Set(fd, m.NewField(fd))
	return t
}

func named(kind, obj string) *irv1.Type {
	q := &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: obj}
	n := &irv1.NamedRef{}
	switch kind {
	case "enum":
		n.Kind = &irv1.NamedRef_Enum{Enum: q}
	default:
		n.Kind = &irv1.NamedRef_Domain{Domain: q}
	}
	return &irv1.Type{Kind: &irv1.Type_Named{Named: n}}
}

func weak(aff *irv1.Type) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Weak{Weak: &irv1.Weak{Affinity: aff}}}
}

func length(min, max *uint32, unit irv1.LengthUnit) *irv1.PortableConstraint {
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Length{Length: &irv1.LengthConstraint{Min: min, Max: max, Unit: unit}}}
}

func rangeOf(min, max *irv1.Value) *irv1.PortableConstraint {
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Range{Range: &irv1.RangeConstraint{
		Min: min, Max: max, MinInclusive: min != nil, MaxInclusive: max != nil}}}
}

func pattern(re string) *irv1.PortableConstraint {
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Pattern{Pattern: &irv1.PatternConstraint{Regex: re}}}
}

func portable(name string, p *irv1.PortableConstraint, cols ...string) *irv1.Constraint {
	return &irv1.Constraint{Name: name, Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK, Columns: cols,
		Form: &irv1.Constraint_Portable{Portable: p}, ClientValidated: true}
}

var (
	chars = irv1.LengthUnit_LENGTH_UNIT_CHARACTERS
	vInt  = func(n int64) *irv1.Value { return &irv1.Value{Kind: &irv1.Value_Int{Int: n}} }
	vUint = func(n uint64) *irv1.Value { return &irv1.Value{Kind: &irv1.Value_Uint{Uint: n}} }
	vDec  = func(s string) *irv1.Value { return &irv1.Value{Kind: &irv1.Value_Decimal{Decimal: s}} }
	vFlt  = func(f float64) *irv1.Value { return &irv1.Value{Kind: &irv1.Value_Float{Float: f}} }
	vStr  = func(s string) *irv1.Value { return &irv1.Value{Kind: &irv1.Value_String_{String_: s}} }
	vBool = func(b bool) *irv1.Value { return &irv1.Value{Kind: &irv1.Value_Bool{Bool: b}} }
)

// shop holds the constraints of the running example (§1.3, §8.5, §10.4),
// plus columns of every type family a range can and cannot compare in.
func shop() *irv1.Bundle {
	cols := []*irv1.Column{}
	for _, c := range []struct {
		name string
		t    *irv1.Type
	}{
		{"title", ty("text")}, {"status", named("enum", "post_status")}, {"published_at", ty("instant")},
		{"n", ty("int32")}, {"u", ty("uint64")}, {"f", ty("float64")}, {"price", ty("decimal")}, {"cash", ty("money")},
		{"d", ty("date")}, {"lt", ty("local_time")}, {"iv", ty("interval")}, {"b", ty("bool")},
		{"slug", named("domain", "slug_text")}, {"score", named("domain", "positive")},
		{"lost", named("domain", "missing")}, {"attempts", weak(ty("int64"))}, {"untyped", nil},
	} {
		cols = append(cols, &irv1.Column{Name: id(c.name), Type: c.t})
	}
	return &irv1.Bundle{Catalogs: []*irv1.Catalog{{
		Name:       id("shop"),
		Provenance: &irv1.Provenance{Source: "pg-live"},
		Namespaces: []*irv1.Namespace{{
			Name: id("app"),
			Tables: []*irv1.Table{{Name: id("posts"), Columns: cols, Constraints: []*irv1.Constraint{
				portable("posts_title_check", length(proto.Uint32(1), proto.Uint32(200), chars), "title"),
				{Name: "posts_published_check", Columns: []string{"published_at", "status"},
					Form: &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{
						Native: "CHECK (published_at IS NULL OR status <> 'draft'::app.post_status)"}}},
			}}},
			Domains: []*irv1.Domain{
				{Name: id("email_address"), Base: ty("text"), Constraints: []*irv1.Constraint{
					portable("email_address_check", length(nil, proto.Uint32(320), chars)),
				}},
				{Name: id("slug_text"), Base: ty("text"), Constraints: []*irv1.Constraint{
					portable("slug_text_check", pattern("^[a-z0-9]+(-[a-z0-9]+)*$")),
				}},
				{Name: id("positive"), Base: ty("int64")},
			},
		}},
	}}}
}

func ns(b *irv1.Bundle) *irv1.Namespace { return b.GetCatalogs()[0].GetNamespaces()[0] }

func codes(ds []*irv1.Diagnostic) []string {
	var out []string
	for _, d := range ds {
		q := d.GetTarget()
		out = append(out, d.GetCode()+" "+strings.Join([]string{q.GetCatalog(), q.GetNamespace(), q.GetObject(), q.GetMember()}, "."))
	}
	return out
}

func TestCheckShopIsClean(t *testing.T) {
	if ds := Check(shop()); len(ds) != 0 {
		t.Fatalf("Check(shop) = %q", codes(ds))
	}
}

func TestCheck(t *testing.T) {
	onTable := func(c *irv1.Constraint) func(*irv1.Bundle) {
		return func(b *irv1.Bundle) { ns(b).Tables[0].Constraints = append(ns(b).Tables[0].Constraints, c) }
	}
	onDomain := func(i int, c *irv1.Constraint) func(*irv1.Bundle) {
		return func(b *irv1.Bundle) { ns(b).Domains[i].Constraints = append(ns(b).Domains[i].Constraints, c) }
	}
	rng := func(col string, min, max *irv1.Value) func(*irv1.Bundle) {
		return onTable(portable("c", rangeOf(min, max), col))
	}
	const tc = " shop.app.posts.c"
	cases := []struct {
		name   string
		mutate func(*irv1.Bundle)
		want   []string // empty: accepted
		in     string
	}{
		// Form and client_validated (§10.4).
		{"no form", onTable(&irv1.Constraint{Name: "c", Columns: []string{"n"}}), []string{"constraint-form-missing" + tc}, "neither portable nor opaque"},
		{"portable not client validated", onTable(&irv1.Constraint{Name: "c", Columns: []string{"title"},
			Form: &irv1.Constraint_Portable{Portable: &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_NotEmpty{NotEmpty: &irv1.NotEmptyConstraint{}}}}}),
			[]string{"constraint-client-validated" + tc}, "must be true"},
		{"opaque client validated", onTable(&irv1.Constraint{Name: "c", Columns: []string{"n"}, ClientValidated: true,
			Form: &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: "CHECK (n % 2 = 0)"}}}),
			[]string{"constraint-client-validated" + tc}, "must be false"},

		// Columns.
		{"unknown column", onTable(&irv1.Constraint{Name: "c", Columns: []string{"nope"},
			Form: &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: "CHECK (nope)"}}}),
			[]string{"constraint-column-unknown" + tc}, "reads column nope"},
		{"portable on two columns", onTable(portable("c", length(nil, proto.Uint32(3), chars), "title", "n")),
			[]string{"constraint-portable-columns" + tc}, "reads 2 columns"},
		{"portable on no column", onTable(portable("c", length(nil, proto.Uint32(3), chars))),
			[]string{"constraint-portable-columns" + tc}, "reads 0 columns"},
		{"domain portable naming columns", onDomain(0, portable("c", length(nil, proto.Uint32(3), chars), "value")),
			[]string{"constraint-portable-columns shop.app.email_address.c"}, "names columns"},
		{"domain opaque", onDomain(0, &irv1.Constraint{Name: "c", Form: &irv1.Constraint_Opaque{Opaque: &irv1.NativeText{Native: "CHECK (VALUE LIKE '%@%')"}}}), nil, ""},

		// Kinds.
		{"no kind", onTable(portable("c", &irv1.PortableConstraint{}, "n")), []string{"constraint-portable-kind-missing" + tc}, "no kind"},
		{"not empty", onTable(portable("c", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_NotEmpty{NotEmpty: &irv1.NotEmptyConstraint{}}}, "title")), nil, ""},

		// length
		{"length without unit", onTable(portable("c", length(nil, proto.Uint32(3), irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED), "title")),
			[]string{"constraint-length-unit-missing" + tc}, "without a unit"},
		{"length unbounded", onTable(portable("c", length(nil, nil, chars), "title")), []string{"constraint-length-unbounded" + tc}, "neither"},
		{"length min above max", onTable(portable("c", length(proto.Uint32(5), proto.Uint32(3), chars), "title")),
			[]string{"constraint-length-min-above-max" + tc}, "minimum 5 above its maximum 3"},
		{"length min equals max", onTable(portable("c", length(proto.Uint32(3), proto.Uint32(3), chars), "title")), nil, ""},
		{"length min only", onTable(portable("c", length(proto.Uint32(1), nil, irv1.LengthUnit_LENGTH_UNIT_BYTES), "title")), nil, ""},
		{"length on a domain", onDomain(1, portable("c", length(nil, proto.Uint32(3), chars))), nil, ""},

		// range: bounds
		{"range unbounded", rng("n", nil, nil), []string{"constraint-range-unbounded" + tc}, "neither"},
		{"range inclusive without min", onTable(portable("c", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Range{Range: &irv1.RangeConstraint{
			Max: vInt(3), MinInclusive: true}}}, "n")), []string{"constraint-range-inclusive-without-bound" + tc}, "inclusive flag"},
		{"range inclusive without max", onTable(portable("c", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Range{Range: &irv1.RangeConstraint{
			Min: vInt(3), MaxInclusive: true}}}, "n")), []string{"constraint-range-inclusive-without-bound" + tc}, ""},
		{"range exclusive min only", onTable(portable("c", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Range{Range: &irv1.RangeConstraint{
			Min: vInt(0)}}}, "n")), nil, ""},

		// range: logical types (§10.2: numbers, dates, times)
		{"int range", rng("n", vInt(1), vInt(10)), nil, ""},
		{"int range with uint bound", rng("n", vUint(1), nil), nil, ""},
		{"uint range", rng("u", vUint(1), vInt(10)), nil, ""},
		{"int range with string bound", rng("n", vStr("1"), nil), []string{"constraint-range-bound-type" + tc}, "int32 whose minimum is a string"},
		{"int range with decimal bound", rng("n", nil, vDec("1.5")), []string{"constraint-range-bound-type" + tc}, "maximum is a decimal"},
		{"int range with bool bound", rng("n", vBool(true), nil), []string{"constraint-range-bound-type" + tc}, "a bool"},
		{"int range with empty bound", rng("n", &irv1.Value{}, nil), []string{"constraint-range-bound-type" + tc}, "an empty"},
		{"float range", rng("f", vFlt(0.5), vDec("2.5")), nil, ""},
		{"decimal range", rng("price", vDec("0.00"), vInt(1000)), nil, ""},
		{"decimal range with float bound", rng("price", vFlt(0.1), nil), []string{"constraint-range-bound-type" + tc}, "a float"},
		{"money range with float bound", rng("cash", nil, vFlt(9.99)), []string{"constraint-range-bound-type" + tc}, "money"},
		{"date range", rng("d", vStr("2000-01-01"), nil), nil, ""},
		{"time range", rng("lt", vStr("09:00"), vStr("17:00")), nil, ""},
		{"instant range with int bound", rng("published_at", vInt(0), nil), []string{"constraint-range-bound-type" + tc}, "instant"},
		{"both bounds wrong", rng("d", vInt(1), vInt(2)), []string{"constraint-range-bound-type" + tc, "constraint-range-bound-type" + tc}, ""},
		{"text range", rng("title", vStr("a"), nil), []string{"constraint-range-type" + tc}, "a range over text"},
		{"bool range", rng("b", vBool(false), nil), []string{"constraint-range-type" + tc}, "bool"},
		{"interval range", rng("iv", vStr("1 day"), nil), []string{"constraint-range-type" + tc}, "interval"},
		{"enum range", rng("status", vStr("draft"), nil), []string{"constraint-range-type" + tc}, "named"},
		{"range through a domain", rng("score", vInt(1), nil), nil, ""},
		{"range through a domain, wrong bound", rng("score", vStr("1"), nil), []string{"constraint-range-bound-type" + tc}, "int64"},
		{"range on a domain over text", rng("slug", vStr("a"), nil), []string{"constraint-range-type" + tc}, "text"},
		{"range through weak", rng("attempts", vInt(0), nil), nil, ""},
		{"range on unresolved domain", rng("lost", vStr("x"), nil), nil, ""},
		{"range on untyped column", rng("untyped", vStr("x"), nil), nil, ""},
		{"domain range", onDomain(2, portable("c", rangeOf(vInt(1), nil))), nil, ""},
		{"domain range, wrong bound", onDomain(2, portable("c", rangeOf(vStr("1"), nil))),
			[]string{"constraint-range-bound-type shop.app.positive.c"}, ""},

		// digits
		{"digits", onTable(portable("c", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Digits{Digits: &irv1.DigitsConstraint{Precision: 12, Scale: 2}}}, "price")), nil, ""},
		{"digits scale equals precision", onTable(portable("c", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Digits{Digits: &irv1.DigitsConstraint{Precision: 2, Scale: 2}}}, "price")), nil, ""},
		{"digits precision zero", onTable(portable("c", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Digits{Digits: &irv1.DigitsConstraint{}}}, "price")),
			[]string{"constraint-digits-precision-zero" + tc}, "precision 0"},
		{"digits scale above precision", onTable(portable("c", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Digits{Digits: &irv1.DigitsConstraint{Precision: 2, Scale: 3}}}, "price")),
			[]string{"constraint-digits-scale-above-precision" + tc}, "scale 3 above precision 2"},

		// membership
		{"membership", onTable(portable("c", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Membership{Membership: &irv1.MembershipConstraint{Values: []*irv1.Value{vStr("draft")}}}}, "title")), nil, ""},
		{"membership empty", onTable(portable("c", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Membership{Membership: &irv1.MembershipConstraint{}}}, "title")),
			[]string{"constraint-membership-empty" + tc}, "lists no values"},

		// pattern
		{"pattern empty", onDomain(1, portable("c", pattern(""))), []string{"constraint-pattern-empty shop.app.slug_text.c"}, "without a regex"},
		{"pattern unportable", onDomain(1, portable("c", pattern("(?i)^[a-z]+$"))),
			[]string{"constraint-pattern-unportable shop.app.slug_text.c"}, `flags "(?i" at code point 0`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := shop()
			c.mutate(b)
			ds := Check(b)
			if got := codes(ds); !slices.Equal(got, c.want) {
				t.Fatalf("codes = %q, want %q", got, c.want)
			}
			for _, d := range ds {
				if d.GetSeverity() != irv1.Severity_SEVERITY_ERROR {
					t.Errorf("%s must be an error", d.GetCode())
				}
				if !strings.Contains(d.GetMessage(), "source plugin pg-live") {
					t.Errorf("message %q does not say who must fix it", d.GetMessage())
				}
			}
			if c.in != "" && !strings.Contains(ds[0].GetMessage(), c.in) {
				t.Errorf("message %q does not contain %q", ds[0].GetMessage(), c.in)
			}
		})
	}
}

func TestCheckWithoutProvenance(t *testing.T) {
	b := shop()
	b.Catalogs[0].Provenance = nil
	ns(b).Tables[0].Constraints[0].ClientValidated = false
	ds := Check(b)
	if len(ds) != 1 || !strings.Contains(ds[0].GetMessage(), "source plugin that produced the catalog") {
		t.Fatalf("diagnostics = %v", ds)
	}
}

func TestCheckSorts(t *testing.T) {
	b := shop()
	ns(b).Domains[0].Constraints[0].ClientValidated = false
	ns(b).Tables[0].Constraints[0].ClientValidated = false
	ns(b).Tables[0].Constraints[1].ClientValidated = true
	want := []string{
		"constraint-client-validated shop.app.email_address.email_address_check",
		"constraint-client-validated shop.app.posts.posts_published_check",
		"constraint-client-validated shop.app.posts.posts_title_check",
	}
	if got := codes(Check(b)); !slices.Equal(got, want) {
		t.Errorf("codes = %q, want %q", got, want)
	}
}
