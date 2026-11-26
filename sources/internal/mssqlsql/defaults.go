package mssqlsql

import (
	"regexp"
	"strconv"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// ClassifyDefault returns a column's default (§11.3), nil for none, from
// its default constraint's definition, which SQL Server prints in
// parentheses: a number in another pair, as ((1)), or a string, as
// ('draft') or (N'x'), is a constant; NULL is none; anything else, such as
// (newid()), is an expression. An IDENTITY column is an identity always:
// an insert writes it only with IDENTITY_INSERT on (§11.4).
func ClassifyDefault(definition *string, t *irv1.Type, identity bool) *irv1.Default {
	if identity {
		return &irv1.Default{Class: irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS}
	}
	if definition == nil {
		return nil
	}
	native := *definition
	inner := unwrap(native)
	switch {
	case strings.EqualFold(inner, "NULL"):
		return nil
	case stringText.MatchString(inner):
		d := &irv1.Default{Class: irv1.DefaultClass_DEFAULT_CONSTANT, Native: native}
		switch t.GetKind().(type) {
		case *irv1.Type_Text, *irv1.Type_Varchar, *irv1.Type_Char:
			d.Value = &irv1.Value{Kind: &irv1.Value_String_{String_: unquoteString(inner)}}
		}
		return d
	case numberText.MatchString(inner):
		d := &irv1.Default{Class: irv1.DefaultClass_DEFAULT_CONSTANT, Native: native}
		switch t.GetKind().(type) {
		case *irv1.Type_Uint8, *irv1.Type_Int16, *irv1.Type_Int32, *irv1.Type_Int64:
			if v, err := strconv.ParseInt(inner, 10, 64); err == nil {
				d.Value = &irv1.Value{Kind: &irv1.Value_Int{Int: v}}
			}
		case *irv1.Type_Bool:
			if inner == "0" || inner == "1" {
				d.Value = &irv1.Value{Kind: &irv1.Value_Bool{Bool: inner == "1"}}
			}
		case *irv1.Type_Decimal:
			if decimalText.MatchString(inner) {
				d.Value = &irv1.Value{Kind: &irv1.Value_Decimal{Decimal: inner}}
			}
		case *irv1.Type_Float32, *irv1.Type_Float64:
			v, _ := strconv.ParseFloat(inner, 64)
			d.Value = &irv1.Value{Kind: &irv1.Value_Float{Float: v}}
		}
		return d
	}
	return &irv1.Default{Class: irv1.DefaultClass_DEFAULT_EXPRESSION, Native: native}
}

var (
	stringText  = regexp.MustCompile(`^N?'(?:[^']|'')*'$`)
	numberText  = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)
	decimalText = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
)

// unwrap removes the parentheses around a whole definition, as many pairs
// as enclose it.
func unwrap(s string) string {
	for len(s) >= 2 && s[0] == '(' && s[len(s)-1] == ')' && closes(s) == len(s)-1 {
		s = s[1 : len(s)-1]
	}
	return s
}

// closes returns the index of the parenthesis that closes s's first, which
// is an opening one, skipping string literals; -1 if none does.
func closes(s string) int {
	depth, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'':
			quoted = !quoted
		case quoted:
		case c == '(':
			depth++
		case c == ')':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}
