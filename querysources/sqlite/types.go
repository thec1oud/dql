package sqlite

import (
	"strings"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

var empty = &irv1.Empty{}

func tBool() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Bool{Bool: empty}} }
func tInt64() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Int64{Int64: empty}} }
func tFloat64() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Float64{Float64: empty}} }
func tDecimal() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}} }
func tText() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}} }
func tBytes() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Bytes{Bytes: &irv1.Bytes{}}} }
func tDate() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Date{Date: empty}} }
func tJSON() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Json{Json: empty}} }

func tLocalDatetime() *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_LocalDatetime{LocalDatetime: &irv1.TimeType{}}}
}

func tLocalTime() *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_LocalTime{LocalTime: &irv1.TimeType{}}}
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

// logical is the type a weak column's values are used as: its affinity's
// (§8.10). Other types are their own.
func logical(t *irv1.Type) *irv1.Type {
	if w := t.GetWeak(); w != nil {
		return w.GetAffinity()
	}
	return t
}

// rank orders the numeric types: the integers, then DECIMAL, which a
// NUMERIC column's affinity is, then REAL.
func rank(t *irv1.Type) int {
	switch logical(t).GetKind().(type) {
	case *irv1.Type_Bool, *irv1.Type_Int64:
		return 1
	case *irv1.Type_Decimal:
		return 2
	case *irv1.Type_Float64:
		return 3
	}
	return 0
}

func isText(t *irv1.Type) bool {
	_, ok := logical(t).GetKind().(*irv1.Type_Text)
	return ok
}

// castType returns the type CAST gives, by the target's affinity (§8.10):
// INTEGER, TEXT, BLOB or REAL. It returns nil for NUMERIC, whose result's
// storage class depends on the value.
func castType(declared string) *irv1.Type {
	switch {
	case strings.Contains(declared, "INT"):
		return tInt64()
	case strings.Contains(declared, "CHAR"), strings.Contains(declared, "CLOB"), strings.Contains(declared, "TEXT"):
		return tText()
	case strings.Contains(declared, "BLOB"), declared == "":
		return tBytes()
	case strings.Contains(declared, "REAL"), strings.Contains(declared, "FLOA"), strings.Contains(declared, "DOUB"):
		return tFloat64()
	}
	return nil
}
