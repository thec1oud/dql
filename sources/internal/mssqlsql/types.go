package mssqlsql

import (
	"fmt"
	"strconv"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Type is what ParseType needs to know about a column's or a parameter's
// type: its system type's name and sys.columns' max_length, precision and
// scale.
type Type struct {
	Name      string
	MaxLength int16
	Precision uint8
	Scale     uint8
	// Alias is the alias type it was declared with, schema.name, or empty.
	Alias string
}

// Render prints a type as a declaration would write it, such as
// nvarchar(80), varbinary(max), decimal(9,3) or datetime2(6).
func Render(t Type) string {
	name := strings.ToLower(t.Name)
	length := func(unit int16) string {
		if t.MaxLength == -1 {
			return name + "(max)"
		}
		return name + "(" + strconv.Itoa(int(t.MaxLength/unit)) + ")"
	}
	switch name {
	case "char", "varchar", "binary", "varbinary":
		return length(1)
	case "nchar", "nvarchar":
		return length(2)
	case "decimal", "numeric":
		return fmt.Sprintf("%s(%d,%d)", name, t.Precision, t.Scale)
	case "datetime2", "datetimeoffset", "time":
		return fmt.Sprintf("%s(%d)", name, t.Scale)
	case "float":
		return fmt.Sprintf("float(%d)", t.Precision)
	}
	return name
}

// ParseType returns a type's logical type, with its native type (§8.1,
// §8.2). Lengths are the database's: bytes for char, varchar and binary,
// UTF-16 code units for nchar and nvarchar (§10.3), and a (max) type has
// none. bit is bool and tinyint, which holds 0 to 255, uint8 (§8.7);
// money is decimal(19,4) and smallmoney decimal(10,4). SQL Server has no
// instant: datetimeoffset is an instant with its offset, and datetime2,
// datetime and smalldatetime are wall-clock readings (§8.8). rowversion
// is bytes; the mapping marks it generated. Types the IR has no logical
// type for are opaque (§8.3): sql_variant, hierarchyid and the spatial
// types.
func ParseType(t Type) *irv1.Type {
	name := strings.ToLower(t.Name)
	native := &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: "sys", Object: name}, Raw: Render(t)}
	if t.Alias != "" {
		schema, alias, _ := strings.Cut(t.Alias, ".")
		native.Name = &irv1.QualifiedName{Namespace: schema, Object: alias}
	}
	out := &irv1.Type{Native: native}
	empty := &irv1.Empty{}
	u32 := func(n int) *uint32 { v := uint32(n); return &v }
	digits := &irv1.TimeType{Precision: u32(int(t.Scale))}
	switch name {
	case "bit":
		out.Kind = &irv1.Type_Bool{Bool: empty}
	case "tinyint":
		out.Kind = &irv1.Type_Uint8{Uint8: empty}
	case "smallint":
		out.Kind = &irv1.Type_Int16{Int16: empty}
	case "int":
		out.Kind = &irv1.Type_Int32{Int32: empty}
	case "bigint":
		out.Kind = &irv1.Type_Int64{Int64: empty}
	case "real":
		out.Kind = &irv1.Type_Float32{Float32: empty}
	case "float":
		out.Kind = &irv1.Type_Float64{Float64: empty}
		if t.Precision <= 24 {
			out.Kind = &irv1.Type_Float32{Float32: empty}
		}
	case "decimal", "numeric":
		out.Kind = &irv1.Type_Decimal{Decimal: &irv1.Decimal{Precision: u32(int(t.Precision)), Scale: u32(int(t.Scale))}}
	case "money":
		out.Kind = &irv1.Type_Decimal{Decimal: &irv1.Decimal{Precision: u32(19), Scale: u32(4)}}
	case "smallmoney":
		out.Kind = &irv1.Type_Decimal{Decimal: &irv1.Decimal{Precision: u32(10), Scale: u32(4)}}
	case "char", "nchar", "varchar", "nvarchar":
		textType(out, name, t.MaxLength)
	case "text", "ntext":
		out.Kind = &irv1.Type_Text{Text: &irv1.Text{}}
	case "binary", "varbinary", "timestamp":
		b := &irv1.Bytes{}
		if t.MaxLength >= 0 {
			b.MaxLength = u32(int(t.MaxLength))
		}
		out.Kind = &irv1.Type_Bytes{Bytes: b}
	case "image":
		out.Kind = &irv1.Type_Bytes{Bytes: &irv1.Bytes{}}
	case "uniqueidentifier":
		out.Kind = &irv1.Type_Uuid{Uuid: empty}
	case "date":
		out.Kind = &irv1.Type_Date{Date: empty}
	case "time":
		out.Kind = &irv1.Type_LocalTime{LocalTime: digits}
	case "datetime2":
		out.Kind = &irv1.Type_LocalDatetime{LocalDatetime: digits}
	case "datetime":
		// Rounded to 1/300 s, which three digits hold.
		out.Kind = &irv1.Type_LocalDatetime{LocalDatetime: &irv1.TimeType{Precision: u32(3)}}
	case "smalldatetime":
		out.Kind = &irv1.Type_LocalDatetime{LocalDatetime: &irv1.TimeType{Precision: u32(0)}}
	case "datetimeoffset":
		out.Kind = &irv1.Type_InstantWithOffset{InstantWithOffset: digits}
	case "xml":
		out.Kind = &irv1.Type_Xml{Xml: empty}
	case "json":
		out.Kind = &irv1.Type_Json{Json: empty}
	default:
		out.Kind = &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}
	}
	return out
}

// textType sets out to a char or varchar type; a (max) one is text.
func textType(out *irv1.Type, name string, maxLength int16) {
	if maxLength == -1 {
		out.Kind = &irv1.Type_Text{Text: &irv1.Text{}}
		return
	}
	unit, n := irv1.LengthUnit_LENGTH_UNIT_BYTES, uint32(maxLength)
	if name[0] == 'n' {
		unit, n = irv1.LengthUnit_LENGTH_UNIT_UTF16_CODE_UNITS, n/2
	}
	if strings.HasSuffix(name, "varchar") {
		out.Kind = &irv1.Type_Varchar{Varchar: &irv1.Varchar{Length: n, Unit: unit}}
		return
	}
	out.Kind = &irv1.Type_Char{Char: &irv1.Char{Length: n, Unit: unit}}
}
