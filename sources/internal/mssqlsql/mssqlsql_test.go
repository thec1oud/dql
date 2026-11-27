package mssqlsql

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// checkColumns are the columns the check tests read.
var checkColumns = map[string]Type{
	"i": {Name: "int", MaxLength: 4},
	"t": {Name: "tinyint", MaxLength: 1},
	"d": {Name: "decimal", MaxLength: 5, Precision: 9, Scale: 2},
	"f": {Name: "float", MaxLength: 8, Precision: 53},
	"s": {Name: "nvarchar", MaxLength: 80},
	"m": {Name: "nvarchar", MaxLength: -1},
	"x": {Name: "ntext", MaxLength: 16},
	"v": {Name: "varchar", MaxLength: 40},
	"a": {Name: "varchar", MaxLength: 40},
	"c": {Name: "nchar", MaxLength: 8},
	"u": {Name: "uniqueidentifier", MaxLength: 16},
}

var checkCollations = map[string]string{
	"s": "SQL_Latin1_General_CP1_CI_AS", "m": "Latin1_General_100_CI_AS", "x": "Latin1_General_CI_AS",
	"v": "Latin1_General_100_CI_AS_SC_UTF8", "a": "Latin1_General_CI_AS", "c": "Latin1_General_BIN2",
}

// recognize reads definition as a check on checkColumns.
func recognize(t *testing.T, definition string) string {
	t.Helper()
	cols := map[string]CheckColumn{}
	for name, typ := range checkColumns {
		cc := CheckColumn{System: typ.Name, Type: ParseType(typ)}
		if coll, ok := checkCollations[name]; ok {
			cc.Collation = Collation(coll)
		}
		cols[name] = cc
	}
	con, err := RecognizeCheck(definition, cols)
	if err != nil {
		t.Fatal(err)
	}
	if !con.ClientValidated {
		return "opaque " + con.GetOpaque().GetNative()
	}
	return canonical(con.GetPortable())
}

// sorted returns JSON text with sorted keys, or text that is not JSON.
func sorted(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// canonical returns m's JSON with sorted keys and no spaces.
func canonical(m proto.Message) string {
	b, _ := protojson.Marshal(m)
	var v any
	json.Unmarshal(b, &v)
	b, _ = json.Marshal(v)
	return string(b)
}

func TestRecognizeCheck(t *testing.T) {
	for def, want := range map[string]string{
		// Lengths, from DATALENGTH only.
		"(datalength([s])>=(2) AND datalength([s])<=(400))": `{"length":{"unit":"LENGTH_UNIT_UTF16_CODE_UNITS","min":1,"max":200}}`,
		"(datalength([s])>(2) AND datalength([s])<(9))":     `{"length":{"unit":"LENGTH_UNIT_UTF16_CODE_UNITS","min":2,"max":4}}`,
		"(datalength([s])>=(3))":                            `{"length":{"unit":"LENGTH_UNIT_UTF16_CODE_UNITS","min":2}}`,
		"(datalength([m])<=(641))":                          `{"length":{"unit":"LENGTH_UNIT_UTF16_CODE_UNITS","max":320}}`,
		"(datalength([x])=(8))":                             `{"length":{"unit":"LENGTH_UNIT_UTF16_CODE_UNITS","min":4,"max":4}}`,
		"((10)>=datalength([v]))":                           `{"length":{"unit":"LENGTH_UNIT_BYTES","max":10}}`,
		"(datalength([v]) between (1) and (4))":             `{"length":{"unit":"LENGTH_UNIT_BYTES","min":1,"max":4}}`,
		"(datalength([s])=(7))":                             "opaque (datalength([s])=(7))",
		"(datalength([s])<=(-1))":                           "opaque (datalength([s])<=(-1))",
		"(datalength([s])<(0))":                             "opaque (datalength([s])<(0))",
		"(datalength([s])>=(-4))":                           "opaque (datalength([s])>=(-4))",
		"(datalength([s])<>(4))":                            "opaque (datalength([s])<>(4))",
		"(datalength([s])<=(1.5))":                          "opaque (datalength([s])<=(1.5))",
		"(datalength([s])>=(2) AND datalength([s])>=(4))":   "opaque (datalength([s])>=(2) AND datalength([s])>=(4))",
		"(datalength([a])<=(4))":                            "opaque (datalength([a])<=(4))",
		"(datalength([c])<=(4))":                            "opaque (datalength([c])<=(4))",
		"(datalength([i])<=(4))":                            "opaque (datalength([i])<=(4))",
		"(datalength([s],[s])<=(4))":                        "opaque (datalength([s],[s])<=(4))",
		"(datalength([q])<=(4))":                            "opaque (datalength([q])<=(4))",
		"(len([s])<=(4))":                                   "opaque (len([s])<=(4))",
		"(datalength([s])>=(2) AND datalength([v])<=(4))":   "opaque (datalength([s])>=(2) AND datalength([v])<=(4))",
		"(datalength([s])>=(2) AND [s]<=(4))":               "opaque (datalength([s])>=(2) AND [s]<=(4))",
		"(datalength([s])>=(2) AND [q]<=(4))":               "opaque (datalength([s])>=(2) AND [q]<=(4))",
		"(datalength([s])>=(9999999999))":                   "opaque (datalength([s])>=(9999999999))",
		// Ranges.
		"([i]>=(0))":                         `{"range":{"min":{"int":"0"},"minInclusive":true}}`,
		"([i]>(-5) AND [i]<(5))":             `{"range":{"min":{"int":"-5"},"max":{"int":"5"}}}`,
		"([t] between (1) and (9))":          `{"range":{"min":{"int":"1"},"minInclusive":true,"max":{"int":"9"},"maxInclusive":true}}`,
		"([d]<=(10.50))":                     `{"range":{"max":{"decimal":"10.50"},"maxInclusive":true}}`,
		"([f]>(0.5))":                        `{"range":{"min":{"float":0.5}}}`,
		"([f]>(1e3))":                        "opaque ([f]>(1e3))",
		"([d]>(1e3))":                        "opaque ([d]>(1e3))",
		"([i]>(1.5))":                        "opaque ([i]>(1.5))",
		"([i]>'1')":                          "opaque ([i]>'1')",
		"([i]>-'1')":                         "opaque ([i]>-'1')",
		"([i]>NULL)":                         "opaque ([i]>NULL)",
		"([i]>[t])":                          "opaque ([i]>[t])",
		"([i]>=(1) AND [i]>=(2))":            "opaque ([i]>=(1) AND [i]>=(2))",
		"([i]<>(1))":                         "opaque ([i]<>(1))",
		"([s]>=N'a')":                        "opaque ([s]>=N'a')",
		"((1)<(2))":                          "opaque ((1)<(2))",
		"(([i]=(1) OR [i]=(2)) AND [i]>(0))": "opaque (([i]=(1) OR [i]=(2)) AND [i]>(0))",
		"([u]>=(1))":                         "opaque ([u]>=(1))",
		"([i] not between (1) and (2))":      "opaque ([i] not between (1) and (2))",
		"([q] between (1) and (2))":          "opaque ([q] between (1) and (2))",
		"((1)+[i]>(0))":                      "opaque ((1)+[i]>(0))",
		"([i]>(0) AND [i]+(1)<(5))":          "opaque ([i]>(0) AND [i]+(1)<(5))",
		"([i]>(0) OR [i]<(-5))":              "opaque ([i]>(0) OR [i]<(-5))",
		"(NOT [i]>(0))":                      "opaque (NOT [i]>(0))",
		// Membership, an IN list stored as a reversed OR chain.
		"([s]='c' OR [s]='b' OR [s]=N'a')": `{"membership":{"values":[{"string":"a"},{"string":"b"},{"string":"c"}]}}`,
		"('b'=[s] OR [s]='a')":             `{"membership":{"values":[{"string":"a"},{"string":"b"}]}}`,
		"([s]='it''s')":                    `{"membership":{"values":[{"string":"it's"}]}}`,
		"([i] IN ((1), (2)))":              `{"membership":{"values":[{"int":"1"},{"int":"2"}]}}`,
		"([i] NOT IN ((1), (2)))":          "opaque ([i] NOT IN ((1), (2)))",
		"([q] IN ((1), (2)))":              "opaque ([q] IN ((1), (2)))",
		"([i] IN ((1), [t]))":              "opaque ([i] IN ((1), [t]))",
		"([c]='ab' OR [c]='cd')":           "opaque ([c]='ab' OR [c]='cd')",
		"([s]='a' OR [v]='b')":             "opaque ([s]='a' OR [v]='b')",
		"([s]='a' OR [s]<>'b')":            "opaque ([s]='a' OR [s]<>'b')",
		"([s]='a' OR datalength([s])=(2))": "opaque ([s]='a' OR datalength([s])=(2))",
		"([s]='a' OR [s] IS NULL)":         "opaque ([s]='a' OR [s] IS NULL)",
		"([s]=(1))":                        "opaque ([s]=(1))",
		"([q]=(1))":                        "opaque ([q]=(1))",
		"([s]<>'')":                        "opaque ([s]<>'')",
		"([s] like 'a%')":                  "opaque ([s] like 'a%')",
	} {
		if got := recognize(t, def); got != sorted(want) {
			t.Errorf("%s: got %s, want %s", def, got, want)
		}
	}
}

func TestRecognizeCheckColumns(t *testing.T) {
	con, err := RecognizeCheck("([published_at] IS NULL OR [status]<>'draft' OR [published_at]>[created_at])", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"published_at", "status", "created_at"}; !reflect.DeepEqual(con.Columns, want) {
		t.Errorf("got %v, want %v", con.Columns, want)
	}
	if _, err := RecognizeCheck("([a]=", nil); err == nil {
		t.Error("no error for a definition that does not parse")
	}
}

func TestParseCondition(t *testing.T) {
	for text, want := range map[string]Expr{
		"[a]=1":                           Bin{Op: "=", L: Col{Name: "a"}, R: Lit{Kind: LitNumber, Text: "1"}},
		`"ab"!=1.5`:                       Bin{Op: "<>", L: Col{Name: "ab"}, R: Lit{Kind: LitNumber, Text: "1.5"}},
		"a!>1e2":                          Bin{Op: "<=", L: Col{Name: "a"}, R: Lit{Kind: LitNumber, Text: "1e2"}},
		"a!<NULL":                         Bin{Op: ">=", L: Col{Name: "a"}, R: Lit{Kind: LitNull, Text: "NULL"}},
		"a=N'x''y'":                       Bin{Op: "=", L: Col{Name: "a"}, R: Lit{Kind: LitString, Text: "x'y"}},
		"a=$1.5":                          Bin{Op: "=", L: Col{Name: "a"}, R: Other{Text: "$1.5"}},
		"a=0x1F":                          Bin{Op: "=", L: Col{Name: "a"}, R: Other{Text: "0x1F"}},
		"a=@v":                            Bin{Op: "=", L: Col{Name: "a"}, R: Other{Text: "@v"}},
		"a=t.b":                           Bin{Op: "=", L: Col{Name: "a"}, R: Other{Text: "t.b"}},
		"a=-(1)":                          Bin{Op: "=", L: Col{Name: "a"}, R: Neg{Op: "-", X: Lit{Kind: LitNumber, Text: "1"}}},
		"a=+b":                            Bin{Op: "=", L: Col{Name: "a"}, R: Neg{Op: "+", X: Col{Name: "b"}}},
		"a=~b":                            Bin{Op: "=", L: Col{Name: "a"}, R: Other{Text: "~b", Cols: []string{"b"}}},
		"a=b*(c-1)":                       Bin{Op: "=", L: Col{Name: "a"}, R: Bin{Op: "*", L: Col{Name: "b"}, R: Bin{Op: "-", L: Col{Name: "c"}, R: Lit{Kind: LitNumber, Text: "1"}}}},
		"a=b COLLATE Latin1_General_BIN2": Bin{Op: "=", L: Col{Name: "a"}, R: Other{Text: "b COLLATE Latin1_General_BIN2", Cols: []string{"b"}}},
		"a=(select 1)":                    Bin{Op: "=", L: Col{Name: "a"}, R: Other{Text: "(select 1)"}},
		"a=len(b)":                        Bin{Op: "=", L: Col{Name: "a"}, R: Call{Name: "len", Args: []Expr{Col{Name: "b"}}}},
		"a=COALESCE(b,c)":                 Bin{Op: "=", L: Col{Name: "a"}, R: Call{Name: "coalesce", Args: []Expr{Col{Name: "b"}, Col{Name: "c"}}}},
		"a=[dbo].[f](b, 2)":               Bin{Op: "=", L: Col{Name: "a"}, R: Call{Name: "dbo.f", Args: []Expr{Col{Name: "b"}, Lit{Kind: LitNumber, Text: "2"}}}},
		"a=f()":                           Bin{Op: "=", L: Col{Name: "a"}, R: Call{Name: "f"}},
		"a=count(*) over ()":              Bin{Op: "=", L: Col{Name: "a"}, R: Other{Text: "count(*) over ()"}},
		"a between 1 and b":               Between{X: Col{Name: "a"}, Lo: Lit{Kind: LitNumber, Text: "1"}, Hi: Col{Name: "b"}},
		"a not between 1 and 2":           Between{X: Col{Name: "a"}, Lo: Lit{Kind: LitNumber, Text: "1"}, Hi: Lit{Kind: LitNumber, Text: "2"}, Not: true},
		"a in (1, b)":                     In{X: Col{Name: "a"}, Items: []Expr{Lit{Kind: LitNumber, Text: "1"}, Col{Name: "b"}}},
		"a not in (1)":                    In{X: Col{Name: "a"}, Items: []Expr{Lit{Kind: LitNumber, Text: "1"}}, Not: true},
		"a in (select b from t)":          Other{Text: "a in (select b from t)", Cols: []string{"a", "b"}},
		"a is null":                       Is{X: Col{Name: "a"}},
		"a is not null":                   Is{X: Col{Name: "a"}, Not: true},
		"a like 'x%'":                     Other{Text: "a like 'x%'", Cols: []string{"a"}},
		"exists (select a, a from t)":     Other{Text: "exists (select a, a from t)", Cols: []string{"a"}},
		"not a=1":                         Not{X: Bin{Op: "=", L: Col{Name: "a"}, R: Lit{Kind: LitNumber, Text: "1"}}},
		"((a=1))":                         Bin{Op: "=", L: Col{Name: "a"}, R: Lit{Kind: LitNumber, Text: "1"}},
		"a=1 AND b=2 OR c=3": Bin{Op: "OR",
			L: Bin{Op: "AND", L: Bin{Op: "=", L: Col{Name: "a"}, R: Lit{Kind: LitNumber, Text: "1"}}, R: Bin{Op: "=", L: Col{Name: "b"}, R: Lit{Kind: LitNumber, Text: "2"}}},
			R: Bin{Op: "=", L: Col{Name: "c"}, R: Lit{Kind: LitNumber, Text: "3"}}},
	} {
		got, err := ParseCondition(text)
		if err != nil {
			t.Errorf("%s: %v", text, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v, want %#v", text, got, want)
		}
	}
	for _, text := range []string{"a=", "a=1 b", "a = 'x"} {
		if _, err := ParseCondition(text); err == nil {
			t.Errorf("%s: no error", text)
		}
	}
}

func TestReferences(t *testing.T) {
	e, err := ParseCondition("f(a, b) = 1 AND c between d and e AND g in (h) AND i is null AND not j = -k AND l like m")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Split("a b c d e g h i j k l m", " ")
	if got := References(e); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := References(Bin{Op: "=", L: Col{Name: "a"}, R: Col{Name: "a"}}); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("got %v", got)
	}
}

func TestExprKinds(t *testing.T) {
	for _, e := range []Expr{Col{}, Lit{}, Call{}, Bin{}, Between{}, In{}, Is{}, Not{}, Neg{}, Other{}} {
		e.expr()
	}
}

func TestUnquote(t *testing.T) {
	for in, want := range map[string]string{"[a]]b]": "a]b", `"a""b"`: `a"b`, "ab": "ab", "a": "a", "[a": "[a"} {
		if got := Unquote(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestParseType(t *testing.T) {
	for _, c := range []struct {
		t         Type
		raw, kind string
	}{
		{Type{Name: "bit"}, "bit", `{"bool":{}}`},
		{Type{Name: "tinyint"}, "tinyint", `{"uint8":{}}`},
		{Type{Name: "smallint"}, "smallint", `{"int16":{}}`},
		{Type{Name: "INT"}, "int", `{"int32":{}}`},
		{Type{Name: "bigint"}, "bigint", `{"int64":{}}`},
		{Type{Name: "real", Precision: 24}, "real", `{"float32":{}}`},
		{Type{Name: "float", Precision: 24}, "float(24)", `{"float32":{}}`},
		{Type{Name: "float", Precision: 53}, "float(53)", `{"float64":{}}`},
		{Type{Name: "decimal", Precision: 9, Scale: 3}, "decimal(9,3)", `{"decimal":{"precision":9,"scale":3}}`},
		{Type{Name: "numeric", Precision: 38}, "numeric(38,0)", `{"decimal":{"precision":38,"scale":0}}`},
		{Type{Name: "money"}, "money", `{"decimal":{"precision":19,"scale":4}}`},
		{Type{Name: "smallmoney"}, "smallmoney", `{"decimal":{"precision":10,"scale":4}}`},
		{Type{Name: "char", MaxLength: 4}, "char(4)", `{"char":{"length":4,"unit":"LENGTH_UNIT_BYTES"}}`},
		{Type{Name: "nchar", MaxLength: 8}, "nchar(4)", `{"char":{"length":4,"unit":"LENGTH_UNIT_UTF16_CODE_UNITS"}}`},
		{Type{Name: "varchar", MaxLength: 40}, "varchar(40)", `{"varchar":{"length":40,"unit":"LENGTH_UNIT_BYTES"}}`},
		{Type{Name: "nvarchar", MaxLength: 160}, "nvarchar(80)", `{"varchar":{"length":80,"unit":"LENGTH_UNIT_UTF16_CODE_UNITS"}}`},
		{Type{Name: "nvarchar", MaxLength: -1}, "nvarchar(max)", `{"text":{}}`},
		{Type{Name: "text", MaxLength: 16}, "text", `{"text":{}}`},
		{Type{Name: "ntext", MaxLength: 16}, "ntext", `{"text":{}}`},
		{Type{Name: "binary", MaxLength: 16}, "binary(16)", `{"bytes":{"maxLength":16}}`},
		{Type{Name: "varbinary", MaxLength: -1}, "varbinary(max)", `{"bytes":{}}`},
		{Type{Name: "timestamp", MaxLength: 8}, "timestamp", `{"bytes":{"maxLength":8}}`},
		{Type{Name: "image", MaxLength: 16}, "image", `{"bytes":{}}`},
		{Type{Name: "uniqueidentifier"}, "uniqueidentifier", `{"uuid":{}}`},
		{Type{Name: "date"}, "date", `{"date":{}}`},
		{Type{Name: "time", Scale: 7}, "time(7)", `{"localTime":{"precision":7}}`},
		{Type{Name: "datetime2", Scale: 3}, "datetime2(3)", `{"localDatetime":{"precision":3}}`},
		{Type{Name: "datetime"}, "datetime", `{"localDatetime":{"precision":3}}`},
		{Type{Name: "smalldatetime"}, "smalldatetime", `{"localDatetime":{"precision":0}}`},
		{Type{Name: "datetimeoffset", Scale: 6}, "datetimeoffset(6)", `{"instantWithOffset":{"precision":6}}`},
		{Type{Name: "xml"}, "xml", `{"xml":{}}`},
		{Type{Name: "json"}, "json", `{"json":{}}`},
		{Type{Name: "sql_variant"}, "sql_variant", `{"opaque":{}}`},
		{Type{Name: "geography"}, "geography", `{"opaque":{}}`},
	} {
		got := ParseType(c.t)
		if got.Native.Raw != c.raw || got.Native.Name.Namespace != "sys" || got.Native.Name.Object != strings.ToLower(c.t.Name) {
			t.Errorf("%v: native %v, want %s", c.t, got.Native, c.raw)
		}
		got.Native = nil
		if k := canonical(got); k != sorted(c.kind) {
			t.Errorf("%v: got %s, want %s", c.t, k, c.kind)
		}
	}
	got := ParseType(Type{Name: "nvarchar", MaxLength: 20, Alias: "billing.code"})
	if n := got.Native.Name; n.Namespace != "billing" || n.Object != "code" || got.Native.Raw != "nvarchar(10)" {
		t.Errorf("alias: got %v", got.Native)
	}
}

func TestClassifyDefault(t *testing.T) {
	str := func(s string) *string { return &s }
	typ := func(name string, p, s uint8) *irv1.Type {
		return ParseType(Type{Name: name, MaxLength: 40, Precision: p, Scale: s})
	}
	for _, c := range []struct {
		def  *string
		t    *irv1.Type
		id   bool
		want string
	}{
		{nil, typ("int", 0, 0), true, `{"class":"DEFAULT_IDENTITY_ALWAYS"}`},
		{nil, typ("int", 0, 0), false, `null`},
		{str("(NULL)"), typ("int", 0, 0), false, `null`},
		{str("((1))"), typ("int", 0, 0), false, `{"class":"DEFAULT_CONSTANT","native":"((1))","value":{"int":"1"}}`},
		{str("((-1))"), typ("bigint", 0, 0), false, `{"class":"DEFAULT_CONSTANT","native":"((-1))","value":{"int":"-1"}}`},
		{str("((99999999999999999999))"), typ("bigint", 0, 0), false, `{"class":"DEFAULT_CONSTANT","native":"((99999999999999999999))"}`},
		{str("((1))"), typ("bit", 0, 0), false, `{"class":"DEFAULT_CONSTANT","native":"((1))","value":{"bool":true}}`},
		{str("((0))"), typ("bit", 0, 0), false, `{"class":"DEFAULT_CONSTANT","native":"((0))","value":{"bool":false}}`},
		{str("((2))"), typ("bit", 0, 0), false, `{"class":"DEFAULT_CONSTANT","native":"((2))"}`},
		{str("((1.50))"), typ("decimal", 9, 2), false, `{"class":"DEFAULT_CONSTANT","native":"((1.50))","value":{"decimal":"1.50"}}`},
		{str("((1e2))"), typ("decimal", 9, 2), false, `{"class":"DEFAULT_CONSTANT","native":"((1e2))"}`},
		{str("((0.5))"), typ("float", 53, 0), false, `{"class":"DEFAULT_CONSTANT","native":"((0.5))","value":{"float":0.5}}`},
		{str("((1))"), typ("uniqueidentifier", 0, 0), false, `{"class":"DEFAULT_CONSTANT","native":"((1))"}`},
		{str("('draft')"), typ("nvarchar", 0, 0), false, `{"class":"DEFAULT_CONSTANT","native":"('draft')","value":{"string":"draft"}}`},
		{str("(N'it''s')"), typ("nchar", 0, 0), false, `{"class":"DEFAULT_CONSTANT","native":"(N'it''s')","value":{"string":"it's"}}`},
		{str("('x')"), ParseType(Type{Name: "ntext"}), false, `{"class":"DEFAULT_CONSTANT","native":"('x')","value":{"string":"x"}}`},
		{str("('2020-01-01')"), typ("date", 0, 0), false, `{"class":"DEFAULT_CONSTANT","native":"('2020-01-01')"}`},
		{str("('a)(b')"), typ("varchar", 0, 0), false, `{"class":"DEFAULT_CONSTANT","native":"('a)(b')","value":{"string":"a)(b"}}`},
		{str("(newid())"), typ("uniqueidentifier", 0, 0), false, `{"class":"DEFAULT_EXPRESSION","native":"(newid())"}`},
		{str("((1)+(2))"), typ("int", 0, 0), false, `{"class":"DEFAULT_EXPRESSION","native":"((1)+(2))"}`},
		{str("(1"), typ("int", 0, 0), false, `{"class":"DEFAULT_EXPRESSION","native":"(1"}`},
		{str("('x)"), typ("int", 0, 0), false, `{"class":"DEFAULT_EXPRESSION","native":"('x)"}`},
	} {
		var got string
		if d := ClassifyDefault(c.def, c.t, c.id); d == nil {
			got = "null"
		} else {
			got = canonical(d)
		}
		if got != sorted(c.want) {
			t.Errorf("%v: got %s, want %s", c.def, got, c.want)
		}
	}
}

func TestCollation(t *testing.T) {
	for name, want := range map[string]string{
		"Latin1_General_BIN2":              `{"name":"Latin1_General_BIN2","caseSensitive":"TRISTATE_TRUE","accentSensitive":"TRISTATE_TRUE","padSpace":"TRISTATE_TRUE","deterministic":"TRISTATE_TRUE"}`,
		"Latin1_General_BIN":               `{"name":"Latin1_General_BIN","caseSensitive":"TRISTATE_TRUE","accentSensitive":"TRISTATE_TRUE","padSpace":"TRISTATE_TRUE","deterministic":"TRISTATE_TRUE"}`,
		"SQL_Latin1_General_CP1_CI_AS":     `{"name":"SQL_Latin1_General_CP1_CI_AS","caseSensitive":"TRISTATE_FALSE","accentSensitive":"TRISTATE_TRUE","padSpace":"TRISTATE_TRUE","deterministic":"TRISTATE_FALSE"}`,
		"Latin1_General_100_CS_AI_SC_UTF8": `{"name":"Latin1_General_100_CS_AI_SC_UTF8","caseSensitive":"TRISTATE_TRUE","accentSensitive":"TRISTATE_FALSE","padSpace":"TRISTATE_TRUE","deterministic":"TRISTATE_FALSE"}`,
		"Odd":                              `{"name":"Odd","padSpace":"TRISTATE_TRUE","deterministic":"TRISTATE_FALSE"}`,
	} {
		if got := canonical(Collation(name)); got != sorted(want) {
			t.Errorf("%s: got %s, want %s", name, got, want)
		}
	}
}

func TestRecognizePredicate(t *testing.T) {
	bits := map[string]bool{"deleted": true}
	for text, want := range map[string]Predicate{
		"([deleted_at] IS NULL AND [deleted]=(0) AND [status]='published')": {NullColumns: []string{"deleted_at"}, NotTrueColumns: []string{"deleted"}},
		"([a] IS NOT NULL AND [n]=(0) AND [deleted]=(1) AND (0)=[deleted])": {},
		"([deleted]=N'0' AND len([a]) IS NULL AND [a]>(0))":                 {},
		"([a] IS NULL OR [b] IS NULL)":                                      {},
	} {
		got, err := RecognizePredicate(text, bits)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v, %v, want %+v", text, got, err, want)
		}
	}
	if _, err := RecognizePredicate("[a] IS", nil); err == nil {
		t.Error("no error for a predicate that does not parse")
	}
}
