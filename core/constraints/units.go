package constraints

import (
	"slices"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// LengthRule says in which unit a dialect counts the length of some types
// (§10.3). Emitters count in the database's unit, never the host's: Go
// strings are bytes and JS strings UTF-16 code units.
type LengthRule struct {
	Dialect irv1.Dialect
	// Native type names as the dialect's source plugin stores them in
	// NativeType.name.object (§8.2). Postgres stores char(n) as bpchar.
	Types []string
	// Functions whose result counts in Unit, which a source plugin may
	// recognize in a check constraint (§10.1).
	Functions []string
	// LENGTH_UNIT_UNSPECIFIED when the dialect does not enforce lengths.
	Unit irv1.LengthUnit
	// Enforced is false when the dialect stores any length (SQLite): there
	// is no length fact unless a check states one.
	Enforced bool
}

// lengthRules is the table of §10.3. MySQL and MariaDB share DIALECT_MYSQL.
var lengthRules = []LengthRule{
	{Dialect: irv1.Dialect_DIALECT_POSTGRES, Types: []string{"bpchar", "varchar"}, Functions: []string{"char_length"},
		Unit: irv1.LengthUnit_LENGTH_UNIT_CHARACTERS, Enforced: true},
	{Dialect: irv1.Dialect_DIALECT_MYSQL, Types: []string{"char", "varchar"},
		Unit: irv1.LengthUnit_LENGTH_UNIT_CHARACTERS, Enforced: true},
	{Dialect: irv1.Dialect_DIALECT_SQLSERVER, Types: []string{"char", "varchar"},
		Unit: irv1.LengthUnit_LENGTH_UNIT_BYTES, Enforced: true},
	{Dialect: irv1.Dialect_DIALECT_SQLSERVER, Types: []string{"nchar", "nvarchar"},
		Unit: irv1.LengthUnit_LENGTH_UNIT_UTF16_CODE_UNITS, Enforced: true},
	{Dialect: irv1.Dialect_DIALECT_SQLITE},
}

// LengthRules returns the length units of every dialect (§10.3), as a copy.
func LengthRules() []LengthRule {
	out := slices.Clone(lengthRules)
	for i := range out {
		out[i].Types = slices.Clone(out[i].Types)
		out[i].Functions = slices.Clone(out[i].Functions)
	}
	return out
}

// TypeLengthUnit returns the unit in which dialect d counts the length of
// native type typ, such as LENGTH_UNIT_UTF16_CODE_UNITS for SQL Server
// nvarchar. ok is false when the dialect enforces no length for typ: SQLite,
// or a type §10.3 does not list. Names match exactly as stored (§4.1).
func TypeLengthUnit(d irv1.Dialect, typ string) (u irv1.LengthUnit, ok bool) {
	return lookupUnit(d, func(r LengthRule) []string { return r.Types }, typ)
}

// FunctionLengthUnit returns the unit in which a length function of dialect
// d counts, such as LENGTH_UNIT_CHARACTERS for Postgres char_length.
func FunctionLengthUnit(d irv1.Dialect, fn string) (u irv1.LengthUnit, ok bool) {
	return lookupUnit(d, func(r LengthRule) []string { return r.Functions }, fn)
}

func lookupUnit(d irv1.Dialect, names func(LengthRule) []string, name string) (irv1.LengthUnit, bool) {
	for _, r := range lengthRules {
		if r.Dialect == d && slices.Contains(names(r), name) {
			return r.Unit, true
		}
	}
	return irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED, false
}
