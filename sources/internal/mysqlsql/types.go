// Package mysqlsql reads the SQL text MySQL and MariaDB print in
// information_schema: column types, defaults and check constraints, into
// their IR forms (design record §8, §10, §11.3).
package mysqlsql

import (
	"fmt"
	"strconv"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Column is what ParseType needs to know about a column.
type Column struct {
	// DataType is information_schema's DATA_TYPE, such as varchar.
	DataType string
	// ColumnType is COLUMN_TYPE, such as varchar(80) or int unsigned.
	ColumnType string
	// Enum names the inline enum a column of type enum refers to (§8.4).
	Enum *irv1.QualifiedName
}

// ParseType returns a column's logical type, with its native type (§8.1,
// §8.2). Types the IR has no logical type for are opaque (§8.3): SET,
// YEAR and the spatial types. For an enum it also returns the labels in
// declared order.
func ParseType(c Column) (*irv1.Type, []string, error) {
	base, mods, rest, err := split(c.ColumnType)
	if err != nil {
		return nil, nil, err
	}
	unsigned := strings.Contains(rest, "unsigned")
	native := &irv1.NativeType{Name: &irv1.QualifiedName{Object: c.DataType}, Raw: c.ColumnType}
	t := &irv1.Type{Native: native}
	var labels []string
	if base == "enum" || base == "set" {
		labels, err = labelsOf(mods)
		if err != nil {
			return nil, nil, fmt.Errorf("type %s: %w", c.ColumnType, err)
		}
		for _, l := range labels {
			native.Modifiers = append(native.Modifiers, &irv1.TypeModifier{Kind: &irv1.TypeModifier_String_{String_: l}})
		}
	} else {
		for _, m := range mods {
			n, err := strconv.ParseInt(m, 10, 32)
			if err != nil {
				return nil, nil, fmt.Errorf("type %s: modifier %q is not a number", c.ColumnType, m)
			}
			native.Modifiers = append(native.Modifiers, &irv1.TypeModifier{Kind: &irv1.TypeModifier_Int{Int: int32(n)}})
		}
	}
	mod := func(i int) (uint32, bool) {
		if i >= len(mods) {
			return 0, false
		}
		n, _ := strconv.ParseUint(mods[i], 10, 32)
		return uint32(n), true
	}
	empty := &irv1.Empty{}
	switch base {
	case "tinyint":
		t.Kind = &irv1.Type_Int8{Int8: empty}
		if unsigned {
			t.Kind = &irv1.Type_Uint8{Uint8: empty}
		}
	case "smallint":
		t.Kind = &irv1.Type_Int16{Int16: empty}
		if unsigned {
			t.Kind = &irv1.Type_Uint16{Uint16: empty}
		}
	case "mediumint", "int", "integer":
		t.Kind = &irv1.Type_Int32{Int32: empty}
		if unsigned {
			t.Kind = &irv1.Type_Uint32{Uint32: empty}
		}
	case "bigint":
		t.Kind = &irv1.Type_Int64{Int64: empty}
		if unsigned {
			t.Kind = &irv1.Type_Uint64{Uint64: empty}
		}
	case "float":
		t.Kind = &irv1.Type_Float32{Float32: empty}
	case "double", "real":
		t.Kind = &irv1.Type_Float64{Float64: empty}
	case "decimal", "numeric":
		d := &irv1.Decimal{}
		if p, ok := mod(0); ok {
			d.Precision = &p
			s, _ := mod(1)
			d.Scale = &s
		}
		t.Kind = &irv1.Type_Decimal{Decimal: d}
	case "char":
		n, _ := mod(0)
		t.Kind = &irv1.Type_Char{Char: &irv1.Char{Length: n, Unit: irv1.LengthUnit_LENGTH_UNIT_CHARACTERS}}
	case "varchar":
		n, _ := mod(0)
		t.Kind = &irv1.Type_Varchar{Varchar: &irv1.Varchar{Length: n, Unit: irv1.LengthUnit_LENGTH_UNIT_CHARACTERS}}
	case "tinytext", "text", "mediumtext", "longtext":
		t.Kind = &irv1.Type_Text{Text: &irv1.Text{}}
	case "binary", "varbinary":
		n, _ := mod(0)
		t.Kind = &irv1.Type_Bytes{Bytes: &irv1.Bytes{MaxLength: &n}}
	case "tinyblob", "blob", "mediumblob", "longblob":
		n := blobMax[base]
		t.Kind = &irv1.Type_Bytes{Bytes: &irv1.Bytes{MaxLength: &n}}
	case "date":
		t.Kind = &irv1.Type_Date{Date: empty}
	case "datetime":
		t.Kind = &irv1.Type_LocalDatetime{LocalDatetime: precision(mod)}
	case "timestamp":
		t.Kind = &irv1.Type_Instant{Instant: precision(mod)}
	case "time":
		// TIME holds −838:59:59 to 838:59:59, a duration, not a time of day
		// (§8.8 rule 2).
		t.Kind = &irv1.Type_Interval{Interval: precision(mod)}
	case "json":
		t.Kind = &irv1.Type_Json{Json: empty}
	case "bit":
		n, ok := mod(0)
		if !ok {
			n = 1
		}
		t.Kind = &irv1.Type_Bit{Bit: &irv1.Bit{Length: &n}}
	case "enum":
		if c.Enum == nil {
			return nil, nil, fmt.Errorf("type %s: an enum column needs its enum's name", c.ColumnType)
		}
		t.Kind = &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: c.Enum}}}
		return t, labels, nil
	default:
		t.Kind = &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}
	}
	return t, nil, nil
}

// blobMax is each BLOB type's maximum length in bytes.
var blobMax = map[string]uint32{"tinyblob": 255, "blob": 65535, "mediumblob": 16777215, "longblob": 4294967295}

// precision reads a time type's fractional-second digits, 0 when absent.
func precision(mod func(int) (uint32, bool)) *irv1.TimeType {
	p, _ := mod(0)
	return &irv1.TimeType{Precision: &p}
}

// TinyIntOne reports whether a column type is TINYINT(1), which the bool
// annotation can map to the boolean type (§8.7). MySQL 8.0.19 and later
// print no display width, except for TINYINT(1).
func TinyIntOne(columnType string) bool {
	return strings.HasPrefix(strings.ToLower(columnType), "tinyint(1)")
}

// split splits a column type into its base name, its parenthesized
// modifiers and the rest, such as unsigned zerofill. Enum and set labels are
// kept quoted in the modifiers.
func split(s string) (base string, mods []string, rest string, err error) {
	open := strings.IndexByte(s, '(')
	if open < 0 {
		base, rest, _ = strings.Cut(s, " ")
		return strings.ToLower(base), nil, rest, nil
	}
	base = strings.ToLower(s[:open])
	i, start, quoted := open+1, open+1, false
	for ; i < len(s); i++ {
		switch c := s[i]; {
		case quoted && c == '\'' && i+1 < len(s) && s[i+1] == '\'':
			i++
		case c == '\'':
			quoted = !quoted
		case quoted:
		case c == ',':
			mods = append(mods, strings.TrimSpace(s[start:i]))
			start = i + 1
		case c == ')':
			mods = append(mods, strings.TrimSpace(s[start:i]))
			return base, mods, strings.TrimSpace(s[i+1:]), nil
		}
	}
	return "", nil, "", fmt.Errorf("type %s: unclosed parenthesis", s)
}

// labelsOf unquotes enum or set labels: 'a', with ” for a quote. MySQL
// prints a backslash in a label as \\.
func labelsOf(mods []string) ([]string, error) {
	var out []string
	for _, m := range mods {
		if len(m) < 2 || m[0] != '\'' || m[len(m)-1] != '\'' {
			return nil, fmt.Errorf("label %s is not quoted", m)
		}
		out = append(out, unquote(m[1:len(m)-1]))
	}
	return out, nil
}

// unquote reads the inside of a quoted string as MySQL prints it.
func unquote(s string) string {
	r := strings.NewReplacer("''", "'", `\\`, `\`, `\'`, "'")
	return r.Replace(s)
}
