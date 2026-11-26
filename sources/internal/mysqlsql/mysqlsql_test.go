package mysqlsql

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// compact writes m without spaces, "" for nil, so wants can be spaced freely.
func compact(m proto.Message) string {
	if m == nil || !m.ProtoReflect().IsValid() {
		return ""
	}
	return squash(prototext.Format(m))
}

func squash(s string) string { return strings.Join(strings.Fields(s), "") }

func kindOf(t *irv1.Type) string {
	c := proto.Clone(t).(*irv1.Type)
	c.Native = nil
	return compact(c)
}

func TestParseType(t *testing.T) {
	en := &irv1.QualifiedName{Object: "t_e"}
	for _, c := range []struct{ data, column, want string }{
		{"tinyint", "tinyint", "int8: {}"},
		{"tinyint", "tinyint(1)", "int8: {}"},
		{"tinyint", "tinyint unsigned", "uint8: {}"},
		{"smallint", "smallint unsigned", "uint16: {}"},
		{"smallint", "smallint", "int16: {}"},
		{"mediumint", "mediumint unsigned", "uint32: {}"},
		{"int", "int", "int32: {}"},
		{"bigint", "bigint unsigned zerofill", "uint64: {}"},
		{"bigint", "bigint", "int64: {}"},
		{"float", "float", "float32: {}"},
		{"double", "double", "float64: {}"},
		{"decimal", "decimal(12,2)", "decimal: { precision: 12 scale: 2 }"},
		{"decimal", "decimal", "decimal: {}"},
		{"char", "char(3)", "char: { length: 3 unit: LENGTH_UNIT_CHARACTERS }"},
		{"varchar", "varchar(80)", "varchar: { length: 80 unit: LENGTH_UNIT_CHARACTERS }"},
		{"text", "mediumtext", "text: {}"},
		{"varbinary", "varbinary(8)", "bytes: { max_length: 8 }"},
		{"blob", "longblob", "bytes: { max_length: 4294967295 }"},
		{"date", "date", "date: {}"},
		{"datetime", "datetime(3)", "local_datetime: { precision: 3 }"},
		{"timestamp", "timestamp", "instant: { precision: 0 }"},
		{"time", "time(2)", "interval: { precision: 2 }"},
		{"json", "json", "json: {}"},
		{"bit", "bit(3)", "bit: { length: 3 }"},
		{"bit", "bit", "bit: { length: 1 }"},
		{"set", "set('a','b')", "opaque: {}"},
		{"enum", "enum('a','it''s','b\\\\c')", `named: { enum: { object: "t_e" } }`},
	} {
		typ, labels, err := ParseType(Column{DataType: c.data, ColumnType: c.column, Enum: en})
		if err != nil {
			t.Errorf("%s: %v", c.column, err)
			continue
		}
		if got := kindOf(typ); got != squash(c.want) {
			t.Errorf("%s: got %s, want %s", c.column, got, c.want)
		}
		if c.data == "enum" && strings.Join(labels, "|") != `a|it's|b\c` {
			t.Errorf("labels %q", labels)
		}
	}
	for _, c := range []struct {
		col  Column
		want string
	}{
		{Column{DataType: "int", ColumnType: "int(x)"}, `modifier "x" is not a number`},
		{Column{DataType: "enum", ColumnType: "enum(a)"}, "label a is not quoted"},
		{Column{DataType: "enum", ColumnType: "enum('a')"}, "an enum column needs its enum's name"},
		{Column{DataType: "int", ColumnType: "int(1"}, "unclosed parenthesis"},
	} {
		if _, _, err := ParseType(c.col); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %s", c.col.ColumnType, err, c.want)
		}
	}
	if !TinyIntOne("TINYINT(1) unsigned") || TinyIntOne("tinyint") {
		t.Error("TinyIntOne")
	}
}

func TestCollation(t *testing.T) {
	for _, c := range []struct {
		name  string
		pad   bool
		want  string
		exact bool
	}{
		{"utf8mb4_bin", true, `name: "utf8mb4_bin" case_sensitive: TRISTATE_TRUE accent_sensitive: TRISTATE_TRUE pad_space: TRISTATE_TRUE deterministic: TRISTATE_TRUE client_fold: CLIENT_FOLD_NONE`, false},
		{"utf8mb4_0900_bin", false, `name: "utf8mb4_0900_bin" case_sensitive: TRISTATE_TRUE accent_sensitive: TRISTATE_TRUE pad_space: TRISTATE_FALSE deterministic: TRISTATE_TRUE client_fold: CLIENT_FOLD_NONE`, true},
		{"utf8mb4_0900_as_cs", false, `name: "utf8mb4_0900_as_cs" case_sensitive: TRISTATE_TRUE accent_sensitive: TRISTATE_TRUE pad_space: TRISTATE_FALSE deterministic: TRISTATE_FALSE`, false},
		{"utf8mb4_0900_ai_cs", false, `name: "utf8mb4_0900_ai_cs" case_sensitive: TRISTATE_TRUE accent_sensitive: TRISTATE_FALSE pad_space: TRISTATE_FALSE deterministic: TRISTATE_FALSE`, false},
		{"latin1_swedish_ci", true, `name: "latin1_swedish_ci" case_sensitive: TRISTATE_FALSE accent_sensitive: TRISTATE_FALSE pad_space: TRISTATE_TRUE deterministic: TRISTATE_FALSE`, false},
		{"utf8mb4_0900_as_ci", false, `name: "utf8mb4_0900_as_ci" case_sensitive: TRISTATE_FALSE accent_sensitive: TRISTATE_TRUE pad_space: TRISTATE_FALSE deterministic: TRISTATE_FALSE`, false},
		{"odd", false, `name: "odd" pad_space: TRISTATE_FALSE deterministic: TRISTATE_FALSE`, false},
	} {
		got := Collation(c.name, c.pad)
		if compact(got) != squash(c.want) || Exact(got) != c.exact {
			t.Errorf("%s: got %s exact %v", c.name, compact(got), Exact(got))
		}
	}
}

func TestClassifyDefault(t *testing.T) {
	ty := func(k string) *irv1.Type {
		m := map[string]*irv1.Type{
			"int": {Kind: &irv1.Type_Int32{}}, "uint": {Kind: &irv1.Type_Uint32{}}, "dec": {Kind: &irv1.Type_Decimal{}},
			"float": {Kind: &irv1.Type_Float64{}}, "text": {Kind: &irv1.Type_Text{}}, "bytes": {Kind: &irv1.Type_Bytes{}},
		}
		return m[k]
	}
	s := func(v string) *string { return &v }
	for _, c := range []struct {
		d    Default
		want string
	}{
		{Default{Extra: "auto_increment", Type: ty("int")}, "class: DEFAULT_IDENTITY_BY_DEFAULT"},
		{Default{Type: ty("int")}, ""},
		{Default{Text: s("NULL"), MariaDB: true, Type: ty("int")}, ""},
		{Default{Text: s("'it''s'"), MariaDB: true, Type: ty("text")}, `class: DEFAULT_CONSTANT native: "'it''s'" value: { string: "it's" }`},
		{Default{Text: s("12"), MariaDB: true, Type: ty("int")}, `class: DEFAULT_CONSTANT native: "12" value: { int: 12 }`},
		{Default{Text: s("current_timestamp()"), MariaDB: true, Type: ty("text")}, `class: DEFAULT_EXPRESSION native: "current_timestamp()"`},
		{Default{Text: s("uuid()"), Extra: "DEFAULT_GENERATED", Type: ty("text")}, `class: DEFAULT_EXPRESSION native: "uuid()"`},
		{Default{Text: s("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED on update CURRENT_TIMESTAMP", Type: ty("text")}, `class: DEFAULT_EXPRESSION native: "CURRENT_TIMESTAMP" on_update: true`},
		{Default{Extra: "on update CURRENT_TIMESTAMP", Type: ty("text")}, "on_update: true"},
		{Default{Text: s("7"), Type: ty("uint")}, `class: DEFAULT_CONSTANT native: "7" value: { uint: 7 }`},
		{Default{Text: s("x"), Type: ty("uint")}, `class: DEFAULT_CONSTANT native: "x"`},
		{Default{Text: s("x"), Type: ty("int")}, `class: DEFAULT_CONSTANT native: "x"`},
		{Default{Text: s("1.50"), Type: ty("dec")}, `class: DEFAULT_CONSTANT native: "1.50" value: { decimal: "1.50" }`},
		{Default{Text: s("x"), Type: ty("dec")}, `class: DEFAULT_CONSTANT native: "x"`},
		{Default{Text: s("2.5"), Type: ty("float")}, `class: DEFAULT_CONSTANT native: "2.5" value: { float: 2.5 }`},
		{Default{Text: s("x"), Type: ty("float")}, `class: DEFAULT_CONSTANT native: "x"`},
		{Default{Text: s("abc"), Type: ty("bytes")}, `class: DEFAULT_CONSTANT native: "abc"`},
	} {
		got := ClassifyDefault(c.d)
		if g := compact(got); g != squash(c.want) {
			t.Errorf("%+v: got %s, want %s", c.d, g, c.want)
		}
	}
}

func TestRecognizeCheck(t *testing.T) {
	bin := Collation("utf8mb4_0900_bin", false)
	ci := Collation("utf8mb4_0900_ai_ci", false)
	cols := map[string]CheckColumn{
		"n":  {Type: &irv1.Type{Kind: &irv1.Type_Int32{}}},
		"u":  {Type: &irv1.Type{Kind: &irv1.Type_Uint64{}}},
		"d":  {Type: &irv1.Type{Kind: &irv1.Type_Decimal{}}},
		"f":  {Type: &irv1.Type{Kind: &irv1.Type_Float64{}}},
		"s":  {Type: &irv1.Type{Kind: &irv1.Type_Text{}}, Collation: bin},
		"v":  {Type: &irv1.Type{Kind: &irv1.Type_Varchar{}}, Collation: ci},
		"dt": {Type: &irv1.Type{Kind: &irv1.Type_Date{}}},
		"ld": {Type: &irv1.Type{Kind: &irv1.Type_LocalDatetime{}}},
		"i":  {Type: &irv1.Type{Kind: &irv1.Type_Instant{}}},
	}
	for _, c := range []struct{ clause, want string }{
		{"(char_length(`s`) between 1 and 200)", "length: { min: 1 max: 200 unit: LENGTH_UNIT_CHARACTERS }"},
		{"length(`s`) <= 10", "length: { max: 10 unit: LENGTH_UNIT_BYTES }"},
		{"(octet_length(`s`) > 1) and (octet_length(`s`) < 9)", "length: { min: 2 max: 8 unit: LENGTH_UNIT_BYTES }"},
		{"character_length(`s`) = 3", "length: { min: 3 max: 3 unit: LENGTH_UNIT_CHARACTERS }"},
		{"5 >= char_length(`s`)", "length: { max: 5 unit: LENGTH_UNIT_CHARACTERS }"},
		{"char_length(`s`) <> 3", ""},
		{"char_length(`s`) <= -1", ""},
		{"char_length(`s`) <= 'x'", ""},
		{"(char_length(`s`) >= 1) and (char_length(`s`) >= 2)", ""},
		{"`s` <> _utf8mb4''", "not_empty: {}"},
		{"`s` <> 'a'", ""},
		{"`v` <> ''", ""},
		{"`n` <> 0", ""},
		{"`s` = 'a'", `membership: { values: { string: "a" } }`},
		{"`v` in ('a')", ""},
		{"`n` in (1, -2)", "membership: { values: { int: 1 } values: { int: -2 } }"},
		{"`u` in (18446744073709551615)", "membership: { values: { uint: 18446744073709551615 } }"},
		{"`d` in (1.25)", `membership: { values: { decimal: "1.25" } }`},
		{"`n` not in (1)", ""},
		{"`n` in (select 1)", ""},
		{"`n` in ('a')", ""},
		{"`x` in (1)", ""},
		{"`f` in (1.5)", ""},
		{"`n` between -(5) and 10", "range: { min: { int: -5 } max: { int: 10 } min_inclusive: true max_inclusive: true }"},
		{"`n` not between 1 and 2", ""},
		{"`f` > 0.5", "range: { min: { float: 0.5 } }"},
		{"`f` > 1e3", "range: { min: { float: 1000 } }"},
		{"(`n` > 0) and (`n` is null)", ""},
		{"(`n` > 0) and (`n` > 1)", ""},
		{"(`n` > 0) and (`n` + 1)", ""},
		{"`u` < 9", "range: { max: { uint: 9 } }"},
		{"`u` < -1", ""},
		{"`d` <= 99.99", `range: { max: { decimal: "99.99" } max_inclusive: true }`},
		{"`dt` >= '2020-01-01'", `range: { min: { string: "2020-01-01" } min_inclusive: true }`},
		{"`dt` >= 'soon'", ""},
		{"`ld` < '2020-01-01 10:00:00'", `range: { max: { string: "2020-01-01 10:00:00" } }`},
		{"`i` < '2020-01-01 10:00:00'", ""},
		{"`s` > 'a'", ""},
		{"`n` > `n`", ""},
		{"`n` > -'a'", ""},
		{"`n` > - -1", ""},
		{"`n` > null", ""},
		{"`n` + 1 > 0", ""},
		{"t.`n` > 0", ""},
		{"(`n` > 0) and (`d` > 0)", ""},
		{"(`n` > 0) or (`n` < -5)", ""},
		{"`n` is null", ""},
		{"regexp_like(`s`, '^[a-z]+$', 'c')", `pattern: { regex: "^[a-z]+$" }`},
		{"regexp_like(`s`, '^[a-z]+$')", ""},
		{"regexp_like(`s`, '^[a-z]+$', 'i')", ""},
		{"regexp_like(`s`, '\\\\d', 'c')", ""},
		{"regexp_like(`n`, 'x', 'c')", ""},
		{"regexp_like(`s`, 1, 'c')", ""},
		{"abs(`n`) > 0", ""},
		{"char_length(`n`) > 0", ""},
		{"char_length(`s`, 1) > 0", ""},
	} {
		got, err := RecognizeCheck(c.clause, cols)
		if err != nil {
			t.Errorf("%s: %v", c.clause, err)
			continue
		}
		p := compact(got.GetPortable())
		if p != squash(c.want) {
			t.Errorf("%s: got %q, want %q", c.clause, p, c.want)
		}
		if (c.want == "") != (got.GetOpaque() != nil) || got.GetClientValidated() == (c.want == "") {
			t.Errorf("%s: form %s", c.clause, compact(got))
		}
	}
	if got, _ := RecognizeCheck("`n` > `d` + `n`", cols); strings.Join(got.GetColumns(), ",") != "n,d" {
		t.Errorf("columns %v", got.GetColumns())
	}
	for _, bad := range []string{"((", "1; select 2", "1, 2"} {
		if _, err := RecognizeCheck(bad, cols); err == nil {
			t.Errorf("%s parses", bad)
		}
	}
	if got := Unescape(`a \'b\'`); got != "a 'b'" {
		t.Errorf("Unescape %q", got)
	}
}
