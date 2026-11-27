package operators_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/thec1oud/dql/core/operators"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func qn(object string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: object}
}

func ty(kind string) *irv1.Type {
	e := &irv1.Empty{}
	switch kind {
	case "bool":
		return &irv1.Type{Kind: &irv1.Type_Bool{Bool: e}}
	case "int64":
		return &irv1.Type{Kind: &irv1.Type_Int64{Int64: e}}
	case "uint8":
		return &irv1.Type{Kind: &irv1.Type_Uint8{Uint8: e}}
	case "float64":
		return &irv1.Type{Kind: &irv1.Type_Float64{Float64: e}}
	case "decimal":
		return &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}}
	case "money":
		return &irv1.Type{Kind: &irv1.Type_Money{Money: e}}
	case "text":
		return &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}
	case "varchar":
		return &irv1.Type{Kind: &irv1.Type_Varchar{Varchar: &irv1.Varchar{}}}
	case "char":
		return &irv1.Type{Kind: &irv1.Type_Char{Char: &irv1.Char{}}}
	case "bytes":
		return &irv1.Type{Kind: &irv1.Type_Bytes{Bytes: &irv1.Bytes{}}}
	case "instant":
		return &irv1.Type{Kind: &irv1.Type_Instant{Instant: &irv1.TimeType{}}}
	case "date":
		return &irv1.Type{Kind: &irv1.Type_Date{Date: e}}
	case "uuid":
		return &irv1.Type{Kind: &irv1.Type_Uuid{Uuid: e}}
	case "json":
		return &irv1.Type{Kind: &irv1.Type_Json{Json: e}}
	case "jsonb":
		return &irv1.Type{Kind: &irv1.Type_Jsonb{Jsonb: e}}
	case "xml":
		return &irv1.Type{Kind: &irv1.Type_Xml{Xml: e}}
	case "bit":
		return &irv1.Type{Kind: &irv1.Type_Bit{Bit: &irv1.Bit{}}}
	case "opaque":
		return &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}}
	}
	panic(kind)
}

func arrayOf(t *irv1.Type) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: t, Dimensions: 1}}}
}

func rangeOf(t *irv1.Type) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Range{Range: &irv1.RangeType{Element: t}}}
}

func named(n *irv1.NamedRef) *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Named{Named: n}} }

func enumOf(o string) *irv1.Type {
	return named(&irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: qn(o)}})
}

func domainOf(o string) *irv1.Type {
	return named(&irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: qn(o)}})
}

func compositeOf(o string) *irv1.Type {
	return named(&irv1.NamedRef{Kind: &irv1.NamedRef_Composite{Composite: qn(o)}})
}

func weakOf(t *irv1.Type) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Weak{Weak: &irv1.Weak{Affinity: t}}}
}

func col(name string, t *irv1.Type) *irv1.Column {
	return &irv1.Column{Name: &irv1.Identifier{Stored: name}, Type: t}
}

// every is a capability profile that performs every operator of the
// catalog exactly.
func every() *irv1.CapabilityProfile {
	p := &irv1.CapabilityProfile{Dialect: irv1.Dialect_DIALECT_POSTGRES}
	for _, o := range operators.Catalog() {
		p.Operators = append(p.Operators, &irv1.OperatorSupport{Name: o.GetName(), Fidelity: irv1.Fidelity_FIDELITY_EXACT})
	}
	return p
}

// bundle holds one table of the given columns, the named types the columns
// use, the catalog's operators and profile.
func bundle(p *irv1.CapabilityProfile, cols ...*irv1.Column) *irv1.Bundle {
	return &irv1.Bundle{
		Operators:    operators.Catalog(),
		Capabilities: p,
		Catalogs: []*irv1.Catalog{{
			Name: &irv1.Identifier{Stored: "shop"},
			Namespaces: []*irv1.Namespace{{
				Name:   &irv1.Identifier{Stored: "app"},
				Tables: []*irv1.Table{{Name: &irv1.Identifier{Stored: "t"}, Columns: cols}},
				Enums:  []*irv1.Enum{{Name: &irv1.Identifier{Stored: "status"}}},
				Domains: []*irv1.Domain{
					{Name: &irv1.Identifier{Stored: "email"}, Base: ty("text"),
						Collation: &irv1.Collation{CaseSensitive: irv1.Tristate_TRISTATE_FALSE}},
					{Name: &irv1.Identifier{Stored: "short_email"}, Base: domainOf("email")},
					{Name: &irv1.Identifier{Stored: "loop"}, Base: domainOf("loop")},
					{Name: &irv1.Identifier{Stored: "status_domain"}, Base: enumOf("status")},
				},
				Composites: []*irv1.Composite{
					{Name: &irv1.Identifier{Stored: "money_amount"}, Attributes: []*irv1.Column{col("amount", ty("decimal")), col("currency", ty("text"))}},
					{Name: &irv1.Identifier{Stored: "doc"}, Attributes: []*irv1.Column{col("body", ty("json"))}},
				},
			}},
		}},
	}
}

func names(c *irv1.Column) string {
	var out []string
	for _, o := range c.GetOperators() {
		n := o.GetName()
		if o.GetFidelity() != irv1.Fidelity_FIDELITY_EXACT {
			n += "~" + strings.ToLower(strings.TrimPrefix(o.GetFidelity().String(), "FIDELITY_"))
		}
		out = append(out, n)
	}
	return strings.Join(out, " ")
}

const (
	text    = "between contains eq ge gt ilike in is_distinct_from le like lt ne regex_match starts_with"
	arrayOp = "any array_contains array_overlaps eq in is_distinct_from ne"
)

func TestCatalog(t *testing.T) {
	ops := operators.Catalog()
	if len(ops) != 30 {
		t.Errorf("the catalog has %d operators, want the 30 of §9.1", len(ops))
	}
	if !slices.IsSortedFunc(ops, func(a, b *irv1.Operator) int { return strings.Compare(a.GetName(), b.GetName()) }) {
		t.Error("the catalog is not sorted by name")
	}
	ops[0].Name = "changed"
	if operators.Catalog()[0].GetName() == "changed" {
		t.Error("Catalog shares its messages between calls")
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name string
		col  *irv1.Column
		want string
	}{
		{"bool", col("c", ty("bool")), "any eq in is_distinct_from ne"},
		{"int64", col("c", ty("int64")), "add any between div eq ge gt in is_distinct_from le lt mod mul ne neg sub"},
		{"uint8", col("c", ty("uint8")), "add any between div eq ge gt in is_distinct_from le lt mod mul ne neg sub"},
		{"float64", col("c", ty("float64")), "add any between div eq ge gt in is_distinct_from le lt mod mul ne neg sub"},
		{"decimal", col("c", ty("decimal")), "add any between div eq ge gt in is_distinct_from le lt mod mul ne neg sub"},
		{"money", col("c", ty("money")), "any between eq ge gt in is_distinct_from le lt ne"},
		{"text", col("c", ty("text")), "any " + text},
		{"varchar", col("c", ty("varchar")), "any " + text},
		{"char", col("c", ty("char")), "any " + text},
		{"bytes", col("c", ty("bytes")), "any between eq ge gt in is_distinct_from le lt ne"},
		{"instant", col("c", ty("instant")), "any between eq ge gt in is_distinct_from le lt ne"},
		{"date", col("c", ty("date")), "any between eq ge gt in is_distinct_from le lt ne"},
		{"uuid", col("c", ty("uuid")), "any between eq ge gt in is_distinct_from le lt ne"},
		{"bit", col("c", ty("bit")), "any eq in is_distinct_from ne"},
		{"json", col("c", ty("json")), "json_contains json_get json_path"},
		{"jsonb", col("c", ty("jsonb")), "any eq in is_distinct_from json_contains json_get json_path ne"},
		{"xml", col("c", ty("xml")), ""},
		{"opaque", col("c", ty("opaque")), ""},
		{"nullable", &irv1.Column{Type: ty("xml"), Nullable: true}, "is_not_null is_null"},
		{"enum", col("c", enumOf("status")), "any between eq ge gt in is_distinct_from le lt ne"},
		{"unknown enum", col("c", enumOf("nope")), ""},
		{"domain over a domain", col("c", domainOf("short_email")), "any " + text},
		{"domain over an enum", col("c", domainOf("status_domain")), "any between eq ge gt in is_distinct_from le lt ne"},
		{"unknown domain", col("c", domainOf("nope")), ""},
		{"domain loop", col("c", domainOf("loop")), ""},
		{"composite", col("c", compositeOf("money_amount")), "any eq in is_distinct_from ne"},
		{"composite of json", col("c", compositeOf("doc")), ""},
		{"unknown composite", col("c", compositeOf("nope")), ""},
		{"weak", col("c", weakOf(ty("int64"))), "add any between div eq ge gt in is_distinct_from le lt mod mul ne neg sub"},
		{"array", col("c", arrayOf(ty("int64"))), arrayOp},
		{"array of a domain", col("c", arrayOf(domainOf("email"))), arrayOp},
		{"array of json", col("c", arrayOf(ty("json"))), ""},
		{"range", col("c", rangeOf(ty("int64"))), "any eq in is_distinct_from ne range_contains range_overlaps"},
		{"range of bit", col("c", rangeOf(ty("bit"))), "any eq in is_distinct_from ne"},
		{"multirange", col("c", &irv1.Type{Kind: &irv1.Type_Multirange{Multirange: &irv1.RangeType{Element: ty("int64")}}}), "any eq in is_distinct_from ne"},
		{"no type", col("c", nil), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := bundle(every(), tt.col)
			if ds := operators.Resolve(b, operators.Options{}); ds != nil {
				t.Fatal(ds)
			}
			if got := names(tt.col); got != tt.want {
				t.Errorf("operators\n got %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestFidelity(t *testing.T) {
	ci := &irv1.Collation{CaseSensitive: irv1.Tristate_TRISTATE_FALSE}
	cs := &irv1.Collation{Name: "utf8mb4_bin", CaseSensitive: irv1.Tristate_TRISTATE_TRUE, Deterministic: irv1.Tristate_TRISTATE_TRUE}
	when := func(c *irv1.Collation) *irv1.OperatorCondition { return &irv1.OperatorCondition{Collation: c} }
	// §9.2's MySQL profile.
	mysql := &irv1.CapabilityProfile{Dialect: irv1.Dialect_DIALECT_MYSQL, Operators: []*irv1.OperatorSupport{
		{Name: "eq", Fidelity: irv1.Fidelity_FIDELITY_EXACT},
		{Name: "ilike", Fidelity: irv1.Fidelity_FIDELITY_EXACT, When: when(ci), Otherwise: irv1.Fidelity_FIDELITY_APPROXIMATE},
		{Name: "like", Fidelity: irv1.Fidelity_FIDELITY_EMULATED, When: when(&irv1.Collation{Name: "utf8mb4_bin"})},
		{Name: "regex_match", Fidelity: irv1.Fidelity_FIDELITY_APPROXIMATE},
	}}
	tests := []struct {
		name  string
		col   *irv1.Column
		allow []string
		want  string
	}{
		{"case-insensitive column", &irv1.Column{Type: ty("text"), Collation: ci}, nil, "eq ilike"},
		{"case-insensitive through a domain", col("c", domainOf("email")), nil, "eq ilike"},
		{"case-sensitive column", &irv1.Column{Type: ty("text"), Collation: cs}, nil, "eq like~emulated"},
		{"approximate allowed", &irv1.Column{Type: ty("text"), Collation: cs}, []string{"ilike", "regex_match"}, "eq ilike~approximate like~emulated regex_match~approximate"},
		{"unknown collation", col("c", ty("text")), []string{"ilike"}, "eq ilike~approximate"},
		{"pad space differs", &irv1.Column{Type: ty("text"), Collation: &irv1.Collation{CaseSensitive: irv1.Tristate_TRISTATE_FALSE, PadSpace: irv1.Tristate_TRISTATE_TRUE}}, nil, "eq ilike"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := bundle(mysql, tt.col)
			if ds := operators.Resolve(b, operators.Options{AllowApproximate: tt.allow}); ds != nil {
				t.Fatal(ds)
			}
			if got := names(tt.col); got != tt.want {
				t.Errorf("operators\n got %s\nwant %s", got, tt.want)
			}
		})
	}
	// Every fact a condition sets must hold on the column.
	for _, c := range []*irv1.Collation{
		{AccentSensitive: irv1.Tristate_TRISTATE_TRUE},
		{PadSpace: irv1.Tristate_TRISTATE_TRUE},
		{Deterministic: irv1.Tristate_TRISTATE_FALSE},
	} {
		p := &irv1.CapabilityProfile{Operators: []*irv1.OperatorSupport{{Name: "eq", Fidelity: irv1.Fidelity_FIDELITY_EXACT, When: when(c)}}}
		column := &irv1.Column{Type: ty("text"), Collation: cs}
		operators.Resolve(bundle(p, column), operators.Options{})
		if got := names(column); got != "" {
			t.Errorf("condition %v: operators %q, want none", c, got)
		}
	}
}

// A scalar condition holds on every column but an array one.
func TestScalarCondition(t *testing.T) {
	p := &irv1.CapabilityProfile{Operators: []*irv1.OperatorSupport{
		{Name: "eq", Fidelity: irv1.Fidelity_FIDELITY_EXACT},
		{Name: "in", Fidelity: irv1.Fidelity_FIDELITY_EXACT, When: &irv1.OperatorCondition{Scalar: true}},
	}}
	scalar := col("c", ty("int64"))
	array := col("a", &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: ty("int64")}}})
	operators.Resolve(bundle(p, scalar), operators.Options{})
	operators.Resolve(bundle(p, array), operators.Options{})
	if got := names(scalar); got != "eq in" {
		t.Errorf("scalar column: %s", got)
	}
	if got := names(array); got != "eq" {
		t.Errorf("array column: %s", got)
	}
}

func TestResolveUnknownOperator(t *testing.T) {
	column := col("c", ty("text"))
	ds := operators.Resolve(bundle(every(), column), operators.Options{AllowApproximate: []string{"ilike", "fuzzy", "soundex"}})
	if len(ds) != 2 || ds[0].GetCode() != operators.CodeOperatorUnknown || !strings.Contains(ds[0].GetMessage(), `"fuzzy"`) {
		t.Fatalf("diagnostics %v, want operator-unknown for fuzzy and soundex", ds)
	}
	if column.GetOperators() != nil {
		t.Error("Resolve set operators despite an error")
	}
}

// An operator a type plugin adds with no operands, an operand of no kind,
// or a requirement the core does not know fits no column.
func TestResolveMalformedOperator(t *testing.T) {
	column := col("c", ty("int64"))
	b := bundle(&irv1.CapabilityProfile{Operators: []*irv1.OperatorSupport{
		{Name: "empty", Fidelity: irv1.Fidelity_FIDELITY_EXACT},
		{Name: "hash_eq", Fidelity: irv1.Fidelity_FIDELITY_EXACT},
		{Name: "nothing", Fidelity: irv1.Fidelity_FIDELITY_EXACT},
	}}, column)
	b.Operators = []*irv1.Operator{
		{Name: "empty", Signature: &irv1.Signature{Args: []*irv1.OperandType{{}}}},
		{Name: "nothing"},
		{Name: "hash_eq", Signature: &irv1.Signature{Args: []*irv1.OperandType{{Kind: &irv1.OperandType_Var{Var: "T"}}}}, Requires: []string{"hashable"}},
	}
	if ds := operators.Resolve(b, operators.Options{}); ds != nil {
		t.Fatal(ds)
	}
	if got := names(column); got != "" {
		t.Errorf("operators %q, want none", got)
	}
}

func TestAdd(t *testing.T) {
	sig := &irv1.Signature{Args: []*irv1.OperandType{{Kind: &irv1.OperandType_Var{Var: "T"}}}}
	contributed := []*irv1.Operator{
		{Name: "postgres.jsonb_has_key", Signature: sig},
		{Name: "postgres.fts_match", Signature: sig},
		{Name: "jsonb_has_key", Signature: sig},
		{Name: "postgres.", Signature: sig},
		{Name: "postgres.fts_match", Signature: sig},
	}
	ops, ds := operators.Add(operators.Catalog(), contributed, irv1.OperatorOriginKind_OPERATOR_ORIGIN_KIND_DIALECT_PLUGIN, "postgres")
	var codes []string
	for _, d := range ds {
		codes = append(codes, d.GetCode())
	}
	if want := []string{"operator-duplicate", "operator-unprefixed", "operator-unprefixed"}; !slices.Equal(codes, want) {
		t.Errorf("codes %v, want %v: %v", codes, want, ds)
	}
	if len(ops) != 32 || !slices.IsSortedFunc(ops, func(a, b *irv1.Operator) int { return strings.Compare(a.GetName(), b.GetName()) }) {
		t.Fatalf("%d operators, sorted %v", len(ops), ops)
	}
	for _, o := range ops {
		want := &irv1.OperatorOrigin{Kind: irv1.OperatorOriginKind_OPERATOR_ORIGIN_KIND_CORE}
		if strings.HasPrefix(o.GetName(), "postgres.") {
			want = &irv1.OperatorOrigin{Kind: irv1.OperatorOriginKind_OPERATOR_ORIGIN_KIND_DIALECT_PLUGIN, Plugin: "postgres"}
		}
		if o.GetOrigin().GetKind() != want.GetKind() || o.GetOrigin().GetPlugin() != want.GetPlugin() {
			t.Errorf("%s: origin %v, want %v", o.GetName(), o.GetOrigin(), want)
		}
	}
}
