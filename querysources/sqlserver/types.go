package sqlserver

import (
	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

var empty = &irv1.Empty{}

func u32(n int) *uint32 {
	v := uint32(n)
	return &v
}

func tBool() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Bool{Bool: empty}} }
func tUint8() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Uint8{Uint8: empty}} }
func tInt16() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Int16{Int16: empty}} }
func tInt32() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Int32{Int32: empty}} }
func tInt64() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Int64{Int64: empty}} }
func tFloat32() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Float32{Float32: empty}} }
func tFloat64() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Float64{Float64: empty}} }
func tDecimal() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}} }
func tText() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}} }
func tBytes() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Bytes{Bytes: &irv1.Bytes{}}} }
func tUUID() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Uuid{Uuid: empty}} }
func tDate() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Date{Date: empty}} }
func tJSON() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Json{Json: empty}} }
func tXML() *irv1.Type     { return &irv1.Type{Kind: &irv1.Type_Xml{Xml: empty}} }

// tDecimalOf is decimal(p, s), as money and smallmoney are.
func tDecimalOf(p, s int) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{Precision: u32(p), Scale: u32(s)}}}
}

func tLocalDatetime(digits int) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_LocalDatetime{LocalDatetime: &irv1.TimeType{Precision: u32(digits)}}}
}

func tLocalTime(digits int) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_LocalTime{LocalTime: &irv1.TimeType{Precision: u32(digits)}}}
}

// tOffsetDatetime is datetimeoffset: an instant with its offset (§8.8).
func tOffsetDatetime(digits int) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_InstantWithOffset{InstantWithOffset: &irv1.TimeType{Precision: u32(digits)}}}
}

// bare returns t without its native type: two uses of a type compare by
// their logical type.
func bare(t *irv1.Type) *irv1.Type {
	if t.GetNative() == nil {
		return t
	}
	t = proto.Clone(t).(*irv1.Type)
	t.Native = nil
	return t
}

// sameType reports whether a and b are the same logical type.
func sameType(a, b *irv1.Type) bool { return proto.Equal(bare(a), bare(b)) }

// rank orders the numeric types by SQL Server's precedence, lowest first:
// bit, the integers by width, decimal (money too), real and float. An
// operator's result has the type of its operand of higher precedence.
func rank(t *irv1.Type) int {
	switch t.GetKind().(type) {
	case *irv1.Type_Bool:
		return 1
	case *irv1.Type_Uint8:
		return 2
	case *irv1.Type_Int16:
		return 3
	case *irv1.Type_Int32:
		return 4
	case *irv1.Type_Int64:
		return 5
	case *irv1.Type_Decimal:
		return 6
	case *irv1.Type_Float32:
		return 7
	case *irv1.Type_Float64:
		return 8
	}
	return 0
}

const rankDecimal = 6

func isInteger(t *irv1.Type) bool { return rank(t) > 0 && rank(t) < rankDecimal }

// isText reports whether t is character data: char, varchar or a (max)
// type.
func isText(t *irv1.Type) bool {
	switch t.GetKind().(type) {
	case *irv1.Type_Text, *irv1.Type_Varchar, *irv1.Type_Char:
		return true
	}
	return false
}

func isMax(t *irv1.Type) bool {
	switch k := t.GetKind().(type) {
	case *irv1.Type_Text:
		return true
	case *irv1.Type_Bytes:
		return k.Bytes.MaxLength == nil
	}
	return false
}

// textOf is the text type of a length a cast names: char(n), varchar(n),
// or text for (max). A cast that names no length takes 30 (§8.2).
func textOf(name string, args []int) *irv1.Type {
	n := 30
	if len(args) > 0 {
		n = args[0]
	}
	if n < 0 {
		return tText()
	}
	unit := irv1.LengthUnit_LENGTH_UNIT_BYTES
	if name[0] == 'n' {
		unit = irv1.LengthUnit_LENGTH_UNIT_UTF16_CODE_UNITS
	}
	if name == "char" || name == "nchar" {
		return &irv1.Type{Kind: &irv1.Type_Char{Char: &irv1.Char{Length: uint32(n), Unit: unit}}}
	}
	return &irv1.Type{Kind: &irv1.Type_Varchar{Varchar: &irv1.Varchar{Length: uint32(n), Unit: unit}}}
}

// castType returns the type a cast to d gives, as sqlserver-live maps the
// same type of a column, or nil for one query files do not support, such
// as sql_variant.
func castType(d dataType) *irv1.Type {
	arg := func(i, def int) int {
		if i < len(d.args) {
			return d.args[i]
		}
		return def
	}
	switch d.name {
	case "bit":
		return tBool()
	case "tinyint":
		return tUint8()
	case "smallint":
		return tInt16()
	case "int":
		return tInt32()
	case "bigint":
		return tInt64()
	case "real":
		return tFloat32()
	case "float":
		if arg(0, 53) <= 24 {
			return tFloat32()
		}
		return tFloat64()
	case "decimal", "numeric", "dec":
		return tDecimalOf(arg(0, 18), arg(1, 0))
	case "money":
		return tDecimalOf(19, 4)
	case "smallmoney":
		return tDecimalOf(10, 4)
	case "char", "varchar", "nchar", "nvarchar":
		return textOf(d.name, d.args)
	case "binary", "varbinary":
		n := arg(0, 30)
		if n < 0 {
			return tBytes()
		}
		return &irv1.Type{Kind: &irv1.Type_Bytes{Bytes: &irv1.Bytes{MaxLength: u32(n)}}}
	case "uniqueidentifier":
		return tUUID()
	case "date":
		return tDate()
	case "time":
		return tLocalTime(arg(0, 7))
	case "datetime2":
		return tLocalDatetime(arg(0, 7))
	case "datetime":
		return tLocalDatetime(3)
	case "smalldatetime":
		return tLocalDatetime(0)
	case "datetimeoffset":
		return tOffsetDatetime(arg(0, 7))
	case "xml":
		return tXML()
	}
	return nil
}
