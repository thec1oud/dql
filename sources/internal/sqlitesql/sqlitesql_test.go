package sqlitesql

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// columns of the check tests' STRICT table, and of their table that is not.
const checkColumns = `i INTEGER, d TEXT, f REAL, s TEXT, n TEXT COLLATE NOCASE, b BLOB, w ANY`

// recognize reads expr as a check of a table with checkColumns.
func recognize(t *testing.T, expr string) string {
	t.Helper()
	tb, err := ParseTable("CREATE TABLE t (" + checkColumns + ", CHECK (" + expr + ")) STRICT")
	if err != nil {
		t.Fatal(err)
	}
	cols := map[string]CheckColumn{}
	for _, c := range tb.Columns {
		typ := ParseType(c.Type, true, false)
		if c.Name == "d" {
			typ = &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}}
		}
		cc := CheckColumn{Type: typ}
		if typ.GetText() != nil {
			cc.Collation = Collation(c.Collation)
		}
		cols[c.Name] = cc
	}
	con := RecognizeCheck(tb.Constraints[0], cols)
	if !con.ClientValidated {
		return "opaque " + con.GetOpaque().GetNative()
	}
	return canonical(con.GetPortable())
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
	for expr, want := range map[string]string{
		"length(s) <= 10":                   `{"length":{"unit":"LENGTH_UNIT_CHARACTERS","max":10}}`,
		"octet_length(s) BETWEEN 1 AND 4":   `{"length":{"unit":"LENGTH_UNIT_BYTES","min":1,"max":4}}`,
		"length(s) > 0 AND length(s) < 5":   `{"length":{"unit":"LENGTH_UNIT_CHARACTERS","min":1,"max":4}}`,
		"length(s) = 3":                     `{"length":{"unit":"LENGTH_UNIT_CHARACTERS","min":3,"max":3}}`,
		"3 >= length(s)":                    `{"length":{"unit":"LENGTH_UNIT_CHARACTERS","max":3}}`,
		"length(s) <> 3":                    "opaque length(s) <> 3",
		"length(s) <= -1":                   "opaque length(s) <= -1",
		"length(s) <= 'x'":                  "opaque length(s) <= 'x'",
		"length(s) >= 1 AND length(s) >= 2": "opaque length(s) >= 1 AND length(s) >= 2",
		"length(s) >= 1 AND length(n) <= 2": "opaque length(s) >= 1 AND length(n) <= 2",
		"length(s) >= 1 AND i <= 2":         "opaque length(s) >= 1 AND i <= 2",
		"length(s) >= 1 AND 1":              "opaque length(s) >= 1 AND 1",
		"length(i) <= 3":                    "opaque length(i) <= 3",
		"length(s, 1) <= 3":                 "opaque length(s, 1) <= 3",
		"upper(s) <= 3":                     "opaque upper(s) <= 3",
		"i BETWEEN -5 AND +5":               "opaque i BETWEEN -5 AND +5",
		"i BETWEEN -5 AND 5":                `{"range":{"min":{"int":"-5"},"minInclusive":true,"max":{"int":"5"},"maxInclusive":true}}`,
		"i NOT BETWEEN 1 AND 2":             "opaque i NOT BETWEEN 1 AND 2",
		"i > 0":                             `{"range":{"min":{"int":"0"}}}`,
		"i < 9.5":                           "opaque i < 9.5",
		"d >= 0.5":                          `{"range":{"min":{"decimal":"0.5"},"minInclusive":true}}`,
		"d >= 1e3":                          "opaque d >= 1e3",
		"f < 2.5":                           `{"range":{"max":{"float":2.5}}}`,
		"f < 'x'":                           "opaque f < 'x'",
		"f < -'x'":                          "opaque f < -'x'",
		"s > 'a'":                           "opaque s > 'a'",
		"s <> ''":                           `{"notEmpty":{}}`,
		"s <> 'a'":                          "opaque s <> 'a'",
		"n <> ''":                           "opaque n <> ''",
		"i <> 0":                            "opaque i <> 0",
		"s = 'a'":                           `{"membership":{"values":[{"string":"a"}]}}`,
		"s == 1":                            "opaque s == 1",
		"s IN ('a', 'b')":                   `{"membership":{"values":[{"string":"a"},{"string":"b"}]}}`,
		"s NOT IN ('a')":                    "opaque s NOT IN ('a')",
		"length(s) IN (1)":                  "opaque length(s) IN (1)",
		"n IN ('a')":                        "opaque n IN ('a')",
		"i IN (1, 2)":                       `{"membership":{"values":[{"int":"1"},{"int":"2"}]}}`,
		"b IN (x'00')":                      "opaque b IN (x'00')",
		"w > 1":                             "opaque w > 1",
		"t.i > 1":                           "opaque t.i > 1",
		"nope > 1":                          "opaque nope > 1",
		"i > 1 OR i < 0":                    "opaque i > 1 OR i < 0",
		"i + 1 > 2":                         "opaque i + 1 > 2",
		"1 < 2":                             "opaque 1 < 2",
		"i IS NOT NULL":                     "opaque i IS NOT NULL",
		"NOT i":                             "opaque NOT i",
		"(i >= 1)":                          `{"range":{"min":{"int":"1"},"minInclusive":true}}`,
		"i >= 99999999999999999999":         "opaque i >= 99999999999999999999",
		"length(s) <= 99999999999":          "opaque length(s) <= 99999999999",
		"i >= 1 AND i > 2":                  "opaque i >= 1 AND i > 2",
	} {
		if strings.HasPrefix(want, "{") {
			var v any
			json.Unmarshal([]byte(want), &v)
			b, _ := json.Marshal(v)
			want = string(b)
		}
		if got := recognize(t, expr); got != want {
			t.Errorf("%s: got %s, want %s", expr, got, want)
		}
	}
}

// Expressions convert to the forms the recognizer reads, and anything else
// stays as written.
func TestExpr(t *testing.T) {
	for expr, want := range map[string]Expr{
		"a == 1":               Bin{"=", Col{Name: "a"}, Lit{LitNumber, "1"}},
		"a != 1":               Bin{"<>", Col{Name: "a"}, Lit{LitNumber, "1"}},
		"a = 1":                Bin{"=", Col{Name: "a"}, Lit{LitNumber, "1"}},
		"a <> 1":               Bin{"<>", Col{Name: "a"}, Lit{LitNumber, "1"}},
		"a IS NOT b":           Bin{"IS NOT", Col{Name: "a"}, Col{Name: "b"}},
		"a ISNULL":             Bin{"IS", Col{Name: "a"}, Lit{LitNull, "NULL"}},
		"a NOTNULL":            Bin{"IS NOT", Col{Name: "a"}, Lit{LitNull, "NULL"}},
		"a NOT NULL":           Bin{"IS NOT", Col{Name: "a"}, Lit{LitNull, "NULL"}},
		"a IN ()":              Other{"a IN ()"},
		"a IN (SELECT 1)":      Other{"a IN (SELECT 1)"},
		"a LIKE 'x'":           Other{"a LIKE 'x'"},
		"NOT NOT a":            Not{Not{Col{Name: "a"}}},
		"-+a":                  Neg{"-", Neg{"+", Col{Name: "a"}}},
		"a COLLATE NOCASE":     Other{"a COLLATE NOCASE"},
		"\"t\".a":              Col{Table: "t", Name: "a"},
		"main.t.a":             Other{"main.t.a"},
		"Upper(a)":             Call{"upper", []Expr{Col{Name: "a"}}},
		"count(*)":             Other{"count(*)"},
		"CAST(a AS INT)":       Other{"CAST(a AS INT)"},
		"(a, b)":               Other{"(a, b)"},
		"NULL":                 Lit{LitNull, "NULL"},
		"true":                 Lit{LitBool, "TRUE"},
		"CURRENT_DATE":         Lit{LitOther, "CURRENT_DATE"},
		"x'00'":                Lit{LitOther, "x'00'"},
		"a || 'b'":             Bin{"||", Col{Name: "a"}, Lit{LitString, "b"}},
		"a BETWEEN 1 AND 2":    Between{Col{Name: "a"}, Lit{LitNumber, "1"}, Lit{LitNumber, "2"}, false},
		"a NOT IN (1)":         In{Col{Name: "a"}, []Expr{Lit{LitNumber, "1"}}, true},
		"EXISTS (SELECT 1)":    Other{"EXISTS (SELECT 1)"},
		"a = 1 AND b OR NOT c": Bin{"OR", Bin{"AND", Bin{"=", Col{Name: "a"}, Lit{LitNumber, "1"}}, Col{Name: "b"}}, Not{Col{Name: "c"}}},
	} {
		tb, err := ParseTable("CREATE TABLE t (a, b, c, CHECK (" + expr + "))")
		if err != nil {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		if got := tb.Constraints[0].Parsed; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v, want %#v", expr, got, want)
		}
	}
	e := Between{Col{Name: "a"}, Neg{"-", Col{Name: "b"}}, Call{"f", []Expr{Col{Name: "a"}, In{Col{Name: "c"}, []Expr{Col{Name: "d"}}, false}}}, false}
	if got := References(Bin{"AND", e, Not{Col{Name: "e"}}}); !reflect.DeepEqual(got, []string{"a", "b", "c", "d", "e"}) {
		t.Errorf("references: %v", got)
	}
}

func TestParseTable(t *testing.T) {
	tb, err := ParseTable(`CREATE TABLE "T" ( -- @name(T)
  /* on its own line: id's */
  [id] integer  primary   key, -- id's
  name text COLLATE "nocase" CONSTRAINT nn NOT NULL DEFAULT 'x' UNIQUE /* in it */,
  g int AS (id * 2) STORED, other int GENERATED ALWAYS AS (1),
  ref int CONSTRAINT r REFERENCES other (x) CHECK (ref > 0),
  CONSTRAINT k PRIMARY KEY (id, lower(name)), -- k's
  FOREIGN KEY (ref, g) REFERENCES other,
  CHECK (id > 0)
) WITHOUT ROWID, STRICT`)
	if err != nil {
		t.Fatal(err)
	}
	want := &Table{
		Comment: "@name(T)", Strict: true, WithoutRowid: true,
		Columns: []Column{
			{Name: "id", Type: "integer primary key"[:7], Comment: "on its own line: id's\nid's"},
			{Name: "name", Type: "text", Comment: "in it", Collation: "nocase"},
			{Name: "g", Type: "int", Generation: "id * 2", Stored: true},
			{Name: "other", Type: "int", Generation: "1"},
			{Name: "ref", Type: "int"},
		},
		Constraints: []Constraint{
			{Kind: PrimaryKey, Columns: []string{"id"}},
			{Kind: Unique, Columns: []string{"name"}},
			{Kind: ForeignKey, Name: "r", Columns: []string{"ref"}, RefTable: "other", RefColumns: []string{"x"}},
			{Kind: Check, Columns: []string{"ref"}, Expr: "ref > 0", Parsed: Bin{">", Col{Name: "ref"}, Lit{LitNumber, "0"}}},
			{Kind: PrimaryKey, Name: "k", Comment: "k's", Columns: []string{"id"}, Expressions: []string{"lower(name)"}},
			{Kind: ForeignKey, Columns: []string{"ref", "g"}, RefTable: "other"},
			{Kind: Check, Columns: []string{"id"}, Expr: "id > 0", Parsed: Bin{">", Col{Name: "id"}, Lit{LitNumber, "0"}}},
		},
	}
	if !reflect.DeepEqual(tb, want) {
		t.Errorf("got  %#v\nwant %#v", tb, want)
	}
	for _, sql := range []string{"", "CREATE TABLE", "SELECT 1", "CREATE TABLE t AS SELECT 1", "SELECT 1; SELECT 2"} {
		if _, err := ParseTable(sql); err == nil {
			t.Errorf("%q: no error", sql)
		}
	}
}

func TestViewReads(t *testing.T) {
	got, err := ViewReads(`CREATE VIEW v AS WITH c AS (SELECT * FROM "B") SELECT * FROM c, a JOIN b ON 1 JOIN (SELECT * FROM a) WHERE EXISTS (SELECT 1 FROM C)`)
	if err != nil || !reflect.DeepEqual(got, []string{"B", "a", "b"}) {
		t.Errorf("got %v, %v", got, err)
	}
	for _, sql := range []string{"CREATE VIEW", "SELECT 1"} {
		if _, err := ViewReads(sql); err == nil {
			t.Errorf("%q: no error", sql)
		}
	}
}

func TestParseIndex(t *testing.T) {
	x, err := ParseIndex(`CREATE UNIQUE INDEX i ON t (a, lower(b) DESC, t.c)
		WHERE a IS NULL AND (b = FALSE AND c IS FALSE) AND d IS NOT TRUE AND NOT e AND f IS NOT NULL AND NOT lower(g) AND t.h IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	want := &Index{
		Expressions:    []string{"", "lower(b)", "t.c"},
		Where:          "a IS NULL AND (b = FALSE AND c IS FALSE) AND d IS NOT TRUE AND NOT e AND f IS NOT NULL AND NOT lower(g) AND t.h IS NULL",
		NullColumns:    []string{"a"},
		NotTrueColumns: []string{"b", "c", "d", "e"},
	}
	if !reflect.DeepEqual(x, want) {
		t.Errorf("got  %#v\nwant %#v", x, want)
	}
	if x, _ := ParseIndex("CREATE INDEX i ON t (a)"); x.Where != "" || x.NullColumns != nil {
		t.Errorf("plain index: %#v", x)
	}
	for _, sql := range []string{"CREATE INDEX", "SELECT 1"} {
		if _, err := ParseIndex(sql); err == nil {
			t.Errorf("%q: no error", sql)
		}
	}
}

// The expression kinds are a closed set.
func TestExprKinds(t *testing.T) {
	for _, e := range []Expr{Col{}, Lit{}, Call{}, Bin{}, Between{}, In{}, Not{}, Neg{}, Other{}} {
		e.expr()
	}
}

func TestUnquote(t *testing.T) {
	for in, want := range map[string]string{
		`"a""b"`: `a"b`, "`a`": "a", "[a]": "a", "'a''b'": "a'b", "a": "a", `"a`: `"a`, "[a": "[a", "x": "x", "": "",
	} {
		if got := Unquote(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestParseType(t *testing.T) {
	for _, c := range []struct {
		declared      string
		strict, rowid bool
		want          string
	}{
		{"INTEGER", true, false, `"int64":{}`},
		{"int", true, false, `"int64":{}`},
		{"REAL", true, false, `"float64":{}`},
		{"TEXT", true, false, `"text":{}`},
		{"BLOB", true, false, `"bytes":{}`},
		{"ANY", true, false, `"weak":{"affinity":{"bytes":{}}`},
		{"INTEGER", false, true, `"int64":{}`},
		{"BIGINT", false, false, `"weak":{"affinity":{"int64":{}}`},
		{"VARCHAR(10)", false, false, `"weak":{"affinity":{"text":{}}`},
		{"CLOB", false, false, `"weak":{"affinity":{"text":{}}`},
		{"", false, false, `"weak":{"affinity":{"bytes":{}}`},
		{"FLOAT", false, false, `"weak":{"affinity":{"float64":{}}`},
		{"DOUBLE PRECISION", false, false, `"weak":{"affinity":{"float64":{}}`},
		{"DECIMAL(10, 2)", false, false, `"weak":{"affinity":{"decimal":{}}`},
		{"BOOLEAN", false, false, `"weak":{"affinity":{"decimal":{}}`},
	} {
		b, _ := protojson.Marshal(ParseType(c.declared, c.strict, c.rowid))
		if got := strings.ReplaceAll(string(b), " ", ""); !strings.Contains(got, c.want) {
			t.Errorf("%q strict=%v rowid=%v: %s", c.declared, c.strict, c.rowid, got)
		}
	}
	if m := ParseType("DECIMAL(10, x)", false, false).GetNative().GetModifiers(); len(m) != 1 || m[0].GetInt() != 10 {
		t.Errorf("modifiers %v", m)
	}
}

func TestClassifyDefault(t *testing.T) {
	text := func(s string) *string { return &s }
	types := map[string]*irv1.Type{
		"int":     ParseType("INTEGER", true, false),
		"float":   ParseType("REAL", true, false),
		"decimal": ParseType("NUMERIC", false, false),
		"text":    ParseType("TEXT", true, false),
		"blob":    ParseType("BLOB", true, false),
	}
	for _, c := range []struct {
		text  *string
		typ   string
		rowid bool
		want  string
	}{
		{nil, "int", true, `{"class":"DEFAULT_IDENTITY_BY_DEFAULT"}`},
		{nil, "int", false, `null`},
		{text("NULL"), "int", false, `null`},
		{text("'a''b'"), "text", false, `{"class":"DEFAULT_CONSTANT","value":{"string":"a'b"},"native":"'a''b'"}`},
		{text("'1'"), "int", false, `{"class":"DEFAULT_CONSTANT","native":"'1'"}`},
		{text("-7"), "int", false, `{"class":"DEFAULT_CONSTANT","value":{"int":"-7"},"native":"-7"}`},
		{text("1.5"), "int", false, `{"class":"DEFAULT_CONSTANT","native":"1.5"}`},
		{text("1.5"), "float", false, `{"class":"DEFAULT_CONSTANT","value":{"float":1.5},"native":"1.5"}`},
		{text("1.5"), "decimal", false, `{"class":"DEFAULT_CONSTANT","value":{"decimal":"1.5"},"native":"1.5"}`},
		{text("1e5"), "decimal", false, `{"class":"DEFAULT_CONSTANT","native":"1e5"}`},
		{text("1"), "blob", false, `{"class":"DEFAULT_CONSTANT","native":"1"}`},
		{text("CURRENT_TIMESTAMP"), "text", false, `{"class":"DEFAULT_EXPRESSION","native":"CURRENT_TIMESTAMP"}`},
	} {
		got := "null"
		if d := ClassifyDefault(c.text, types[c.typ], c.rowid); d != nil {
			got = canonical(d)
		}
		if want := canonical(must(c.want)); c.want != "null" && got != want || c.want == "null" && got != "null" {
			t.Errorf("%v %s: got %s, want %s", c.text, c.typ, got, c.want)
		}
	}
}

// must reads a Default's JSON.
func must(s string) *irv1.Default {
	d := &irv1.Default{}
	protojson.Unmarshal([]byte(s), d)
	return d
}

func TestCollation(t *testing.T) {
	for name, want := range map[string]string{"": "BINARY", "binary": "BINARY", "nocase": "NOCASE", "RTRIM": "RTRIM", "mine": "mine"} {
		if got := Collation(name).GetName(); got != want {
			t.Errorf("%s: %s", name, got)
		}
	}
}
