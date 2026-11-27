package constraints

import (
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// The table of §10.3.
func TestTypeLengthUnit(t *testing.T) {
	const (
		chars = irv1.LengthUnit_LENGTH_UNIT_CHARACTERS
		bytes = irv1.LengthUnit_LENGTH_UNIT_BYTES
		utf16 = irv1.LengthUnit_LENGTH_UNIT_UTF16_CODE_UNITS
		none  = irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED
	)
	cases := []struct {
		d    irv1.Dialect
		typ  string
		want irv1.LengthUnit
		ok   bool
	}{
		{irv1.Dialect_DIALECT_POSTGRES, "varchar", chars, true},
		{irv1.Dialect_DIALECT_POSTGRES, "bpchar", chars, true},
		{irv1.Dialect_DIALECT_POSTGRES, "text", none, false},
		{irv1.Dialect_DIALECT_MYSQL, "varchar", chars, true},
		{irv1.Dialect_DIALECT_MYSQL, "char", chars, true},
		{irv1.Dialect_DIALECT_SQLSERVER, "varchar", bytes, true},
		{irv1.Dialect_DIALECT_SQLSERVER, "char", bytes, true},
		{irv1.Dialect_DIALECT_SQLSERVER, "nvarchar", utf16, true},
		{irv1.Dialect_DIALECT_SQLSERVER, "nchar", utf16, true},
		{irv1.Dialect_DIALECT_SQLITE, "varchar", none, false},
		{irv1.Dialect_DIALECT_SQLITE, "", none, false},
		// Names match exactly as stored (§4.1).
		{irv1.Dialect_DIALECT_SQLSERVER, "NVARCHAR", none, false},
		{irv1.Dialect_DIALECT_UNSPECIFIED, "varchar", none, false},
	}
	for _, c := range cases {
		got, ok := TypeLengthUnit(c.d, c.typ)
		if got != c.want || ok != c.ok {
			t.Errorf("TypeLengthUnit(%s, %q) = %s, %v; want %s, %v", c.d, c.typ, got, ok, c.want, c.ok)
		}
	}
}

func TestFunctionLengthUnit(t *testing.T) {
	if u, ok := FunctionLengthUnit(irv1.Dialect_DIALECT_POSTGRES, "char_length"); !ok || u != irv1.LengthUnit_LENGTH_UNIT_CHARACTERS {
		t.Errorf("char_length = %s, %v", u, ok)
	}
	if _, ok := FunctionLengthUnit(irv1.Dialect_DIALECT_POSTGRES, "octet_length"); ok {
		t.Error("octet_length is not in §10.3")
	}
}

func TestLengthRules(t *testing.T) {
	rules := LengthRules()
	if len(rules) != 5 {
		t.Fatalf("%d rules, want 5", len(rules))
	}
	sqlite := rules[len(rules)-1]
	if sqlite.Dialect != irv1.Dialect_DIALECT_SQLITE || sqlite.Enforced || sqlite.Unit != irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED {
		t.Errorf("SQLite rule = %+v; SQLite enforces no length (§10.3)", sqlite)
	}
	// The result is a copy.
	rules[0].Types[0] = "x"
	rules[0].Functions[0] = "x"
	if LengthRules()[0].Types[0] == "x" || LengthRules()[0].Functions[0] == "x" {
		t.Error("LengthRules returned shared storage")
	}
}
