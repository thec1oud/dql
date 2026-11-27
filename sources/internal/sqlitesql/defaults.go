package sqlitesql

import (
	"strconv"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// ClassifyDefault returns a column's default (§11.3), nil for none, from
// PRAGMA table_info's dflt_value: NULL is none, a quoted string or a
// number is a constant, and anything else, CURRENT_TIMESTAMP or a
// parenthesized expression, is an expression. A rowid alias is an identity
// by default: SQLite numbers it when an insert leaves it out.
func ClassifyDefault(text *string, t *irv1.Type, rowid bool) *irv1.Default {
	if rowid {
		return &irv1.Default{Class: irv1.DefaultClass_DEFAULT_IDENTITY_BY_DEFAULT}
	}
	if text == nil || *text == "NULL" {
		return nil
	}
	native := *text
	if w := t.GetWeak(); w != nil {
		t = w.GetAffinity()
	}
	switch {
	case len(native) >= 2 && native[0] == '\'' && native[len(native)-1] == '\'':
		d := &irv1.Default{Class: irv1.DefaultClass_DEFAULT_CONSTANT, Native: native}
		if _, ok := t.GetKind().(*irv1.Type_Text); ok {
			d.Value = &irv1.Value{Kind: &irv1.Value_String_{String_: Unquote(native)}}
		}
		return d
	case numberText.MatchString(native):
		d := &irv1.Default{Class: irv1.DefaultClass_DEFAULT_CONSTANT, Native: native}
		switch t.GetKind().(type) {
		case *irv1.Type_Int64:
			if v, err := strconv.ParseInt(native, 10, 64); err == nil {
				d.Value = &irv1.Value{Kind: &irv1.Value_Int{Int: v}}
			}
		case *irv1.Type_Float64:
			v, _ := strconv.ParseFloat(native, 64)
			d.Value = &irv1.Value{Kind: &irv1.Value_Float{Float: v}}
		case *irv1.Type_Decimal:
			if decimalText.MatchString(native) {
				d.Value = &irv1.Value{Kind: &irv1.Value_Decimal{Decimal: native}}
			}
		}
		return d
	}
	return &irv1.Default{Class: irv1.DefaultClass_DEFAULT_EXPRESSION, Native: native}
}
