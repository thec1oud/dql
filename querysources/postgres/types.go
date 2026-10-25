package postgres

import (
	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

var empty = &irv1.Empty{}

func tBool() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Bool{Bool: empty}} }
func tInt16() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Int16{Int16: empty}} }
func tInt32() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Int32{Int32: empty}} }
func tInt64() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Int64{Int64: empty}} }
func tFloat32() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Float32{Float32: empty}} }
func tFloat64() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Float64{Float64: empty}} }
func tDecimal() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}} }
func tText() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}} }
func tBytes() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Bytes{Bytes: &irv1.Bytes{}}} }
func tDate() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Date{Date: empty}} }
func tUUID() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Uuid{Uuid: empty}} }
func tJSON() *irv1.Type    { return &irv1.Type{Kind: &irv1.Type_Json{Json: empty}} }
func tJSONB() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Jsonb{Jsonb: empty}} }
func tMoney() *irv1.Type   { return &irv1.Type{Kind: &irv1.Type_Money{Money: empty}} }

func tInstant() *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Instant{Instant: &irv1.TimeType{}}}
}

func tLocalDatetime() *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_LocalDatetime{LocalDatetime: &irv1.TimeType{}}}
}

func tLocalTime() *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_LocalTime{LocalTime: &irv1.TimeType{}}}
}

func tTimeWithOffset() *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_TimeWithOffset{TimeWithOffset: &irv1.TimeType{}}}
}

func tInterval() *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Interval{Interval: &irv1.TimeType{}}}
}

func tArray(elem *irv1.Type, elemNullable bool) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{Element: bare(elem), ElementNullable: elemNullable, Dimensions: 1}}}
}

// bare returns t without its native type, as array elements carry none
// (§8.6) and two uses of a type compare by their logical type.
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

// builtinTypes are the pg_catalog types a cast may name, by every name
// Postgres accepts for them, with their modifiers applied by castType.
var builtinTypes = map[string]func() *irv1.Type{
	"bool": tBool, "boolean": tBool,
	"int2": tInt16, "smallint": tInt16,
	"int4": tInt32, "int": tInt32, "integer": tInt32,
	"int8": tInt64, "bigint": tInt64,
	"float4": tFloat32, "real": tFloat32,
	"float8": tFloat64, "double precision": tFloat64,
	"numeric": tDecimal, "decimal": tDecimal,
	"money": tMoney,
	"text":  tText, "varchar": tText, "bpchar": tText, "char": tText,
	"bytea":       tBytes,
	"date":        tDate,
	"timestamptz": tInstant, "timestamp": tLocalDatetime,
	"time": tLocalTime, "timetz": tTimeWithOffset,
	"interval": tInterval,
	"uuid":     tUUID,
	"json":     tJSON, "jsonb": tJSONB,
}

// castType returns the type a cast names. A built-in name may be written
// with or without pg_catalog; other names resolve in the catalog. Modifiers
// such as varchar(80) are dropped: the value keeps its logical type, and
// the database applies the length.
func (a *analyzer) castType(tn *pg.TypeName) (*irv1.Type, error) {
	ns, ok := nameList(tn.GetNames())
	if !ok || len(ns) == 0 || len(ns) > 2 {
		return nil, a.errAt("query-unsupported", tn.GetLocation(), "this type name is not supported; name a type as name or namespace.name")
	}
	var t *irv1.Type
	name := ns[len(ns)-1]
	if build, ok := builtinTypes[name]; ok && (len(ns) == 1 || ns[0] == "pg_catalog") {
		t = build()
	} else {
		namespace := ""
		if len(ns) == 2 {
			namespace = ns[0]
		}
		ts := a.ix.namedType(namespace, name)
		if len(ts) != 1 {
			return nil, a.errAt("query-unknown-type", tn.GetLocation(), "type %s is not in the catalog; cast to a built-in type or to an enum, domain or composite of the catalog", joinDots(ns))
		}
		t = ts[0]
	}
	if len(tn.GetArrayBounds()) > 0 {
		t = tArray(t, true)
	}
	return t, nil
}

// nameList returns the strings of a name list, or false when an element is
// not a string, such as the * of a column reference.
func nameList(list []*pg.Node) ([]string, bool) {
	out := make([]string, len(list))
	for i, n := range list {
		s := n.GetString_()
		if s == nil {
			return nil, false
		}
		out[i] = s.GetSval()
	}
	return out, true
}

func joinDots(ns []string) string {
	s := ""
	for i, n := range ns {
		if i > 0 {
			s += "."
		}
		s += n
	}
	return s
}

// rank orders the numeric types for arithmetic: the result of two operands
// has the higher rank's type, as Postgres's implicit casts give.
func rank(t *irv1.Type) int {
	switch t.GetKind().(type) {
	case *irv1.Type_Int16:
		return 1
	case *irv1.Type_Int32:
		return 2
	case *irv1.Type_Int64:
		return 3
	case *irv1.Type_Decimal:
		return 4
	case *irv1.Type_Float32:
		return 5
	case *irv1.Type_Float64:
		return 6
	}
	return 0
}

func isText(t *irv1.Type) bool {
	switch t.GetKind().(type) {
	case *irv1.Type_Text, *irv1.Type_Varchar, *irv1.Type_Char:
		return true
	}
	return false
}
