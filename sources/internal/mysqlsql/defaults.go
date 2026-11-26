package mysqlsql

import (
	"regexp"
	"strconv"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Default is what ClassifyDefault reads about a column.
type Default struct {
	// Text is COLUMN_DEFAULT, nil when NULL.
	Text *string
	// Extra is the column's EXTRA.
	Extra string
	// MariaDB is set for a MariaDB server, which quotes constant strings and
	// prints NULL for a nullable column without a default.
	MariaDB bool
	// Type is the column's logical type.
	Type *irv1.Type
}

var numberText = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)

// ClassifyDefault returns a column's default (§11.3), nil for none. An
// AUTO_INCREMENT column is an identity by default; MySQL has no identity
// that is always generated. A column the server sets on every update has
// on_update set, whatever its default.
func ClassifyDefault(d Default) *irv1.Default {
	extra := strings.ToLower(d.Extra)
	var out *irv1.Default
	switch {
	case strings.Contains(extra, "auto_increment"):
		out = &irv1.Default{Class: irv1.DefaultClass_DEFAULT_IDENTITY_BY_DEFAULT}
	case d.Text == nil || d.MariaDB && *d.Text == "NULL":
	case d.MariaDB:
		out = mariaDefault(*d.Text, d.Type)
	case strings.Contains(extra, "default_generated"):
		out = &irv1.Default{Class: irv1.DefaultClass_DEFAULT_EXPRESSION, Native: *d.Text}
	default:
		out = constant(*d.Text, *d.Text, d.Type)
	}
	if strings.Contains(extra, "on update") {
		if out == nil {
			out = &irv1.Default{}
		}
		out.OnUpdate = true
	}
	return out
}

// mariaDefault reads MariaDB's default text: a quoted string or a number is
// a constant, anything else an expression.
func mariaDefault(text string, t *irv1.Type) *irv1.Default {
	if len(text) >= 2 && text[0] == '\'' && text[len(text)-1] == '\'' {
		return constant(text, unquote(text[1:len(text)-1]), t)
	}
	if numberText.MatchString(text) {
		return constant(text, text, t)
	}
	return &irv1.Default{Class: irv1.DefaultClass_DEFAULT_EXPRESSION, Native: text}
}

// constant is a constant default with its value in the column's logical
// type, when the source can read it as one.
func constant(native, text string, t *irv1.Type) *irv1.Default {
	d := &irv1.Default{Class: irv1.DefaultClass_DEFAULT_CONSTANT, Native: native}
	switch t.GetKind().(type) {
	case *irv1.Type_Int8, *irv1.Type_Int16, *irv1.Type_Int32, *irv1.Type_Int64:
		if v, err := strconv.ParseInt(text, 10, 64); err == nil {
			d.Value = &irv1.Value{Kind: &irv1.Value_Int{Int: v}}
		}
	case *irv1.Type_Uint8, *irv1.Type_Uint16, *irv1.Type_Uint32, *irv1.Type_Uint64:
		if v, err := strconv.ParseUint(text, 10, 64); err == nil {
			d.Value = &irv1.Value{Kind: &irv1.Value_Uint{Uint: v}}
		}
	case *irv1.Type_Decimal:
		if decimalText.MatchString(text) {
			d.Value = &irv1.Value{Kind: &irv1.Value_Decimal{Decimal: text}}
		}
	case *irv1.Type_Float32, *irv1.Type_Float64:
		if v, err := strconv.ParseFloat(text, 64); err == nil {
			d.Value = &irv1.Value{Kind: &irv1.Value_Float{Float: v}}
		}
	case *irv1.Type_Text, *irv1.Type_Varchar, *irv1.Type_Char, *irv1.Type_Named, *irv1.Type_Date,
		*irv1.Type_LocalDatetime, *irv1.Type_Instant, *irv1.Type_Interval, *irv1.Type_Json:
		d.Value = &irv1.Value{Kind: &irv1.Value_String_{String_: text}}
	}
	return d
}

var decimalText = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

var (
	dateText          = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	localDatetimeText = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}[ T][0-9]{2}:[0-9]{2}(:[0-9]{2}(\.[0-9]{1,6})?)?$`)
)
