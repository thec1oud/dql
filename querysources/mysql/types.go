package mysql

import (
	"github.com/pingcap/tidb/pkg/parser/charset"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/parser/types"
	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

var empty = &irv1.Empty{}

func tBool() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Bool{Bool: empty}} }
func tInt64() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Int64{Int64: empty}} }
func tUint64() *irv1.Type  { return &irv1.Type{Kind: &irv1.Type_Uint64{Uint64: empty}} }
func tFloat32() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Float32{Float32: empty}} }
func tFloat64() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Float64{Float64: empty}} }
func tDecimal() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}} }
func tText() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}} }
func tBytes() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Bytes{Bytes: &irv1.Bytes{}}} }
func tDate() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Date{Date: empty}} }
func tJSON() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Json{Json: empty}} }

func tLocalDatetime() *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_LocalDatetime{LocalDatetime: &irv1.TimeType{}}}
}

// tInterval is TIME, which holds a duration (§8.8 rule 2).
func tInterval() *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Interval{Interval: &irv1.TimeType{}}}
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

// rank orders the numeric types: the integers by width, then DECIMAL, then
// the floating-point types. MySQL computes integer arithmetic in BIGINT.
func rank(t *irv1.Type) int {
	switch t.GetKind().(type) {
	case *irv1.Type_Int8, *irv1.Type_Uint8:
		return 1
	case *irv1.Type_Int16, *irv1.Type_Uint16:
		return 2
	case *irv1.Type_Int32, *irv1.Type_Uint32:
		return 3
	case *irv1.Type_Int64, *irv1.Type_Uint64:
		return 4
	case *irv1.Type_Decimal:
		return 5
	case *irv1.Type_Float32:
		return 6
	case *irv1.Type_Float64:
		return 7
	}
	return 0
}

func isInteger(t *irv1.Type) bool { return rank(t) > 0 && rank(t) < 5 }

func isText(t *irv1.Type) bool {
	switch t.GetKind().(type) {
	case *irv1.Type_Text, *irv1.Type_Varchar, *irv1.Type_Char:
		return true
	}
	return false
}

// castType returns the type CAST or CONVERT gives, by the target the
// parser read: SIGNED, UNSIGNED, DECIMAL, CHAR, BINARY, DATE, DATETIME,
// TIME, DOUBLE, FLOAT or JSON. It returns nil for the rest, such as YEAR
// and the spatial types.
func castType(tp *types.FieldType) *irv1.Type {
	switch tp.GetType() {
	case mysql.TypeLonglong:
		if mysql.HasUnsignedFlag(tp.GetFlag()) {
			return tUint64()
		}
		return tInt64()
	case mysql.TypeNewDecimal:
		return tDecimal()
	case mysql.TypeVarString, mysql.TypeString:
		if tp.GetCharset() == charset.CharsetBin {
			return tBytes()
		}
		return tText()
	case mysql.TypeDate:
		return tDate()
	case mysql.TypeDatetime:
		return tLocalDatetime()
	case mysql.TypeDuration:
		return tInterval()
	case mysql.TypeDouble:
		return tFloat64()
	case mysql.TypeFloat:
		return tFloat32()
	case mysql.TypeJSON:
		return tJSON()
	}
	return nil
}
