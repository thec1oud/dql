package pgsql

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Native is a Postgres type as a source found it. pg-live fills it from
// pg_type, pg_attribute and DecodeTypmod; dpg-snapshot from ParseTypeText.
type Native struct {
	// Namespace and Name are the type's pg_type namespace and name, such as
	// pg_catalog and varchar. For an array they name the element type. An
	// empty Namespace means the name was written unqualified.
	Namespace string
	Name      string
	// Modifiers are the declared type modifiers, as DecodeTypmod and
	// ParseTypeText produce them: varchar(80) has [80], numeric(12,2) [12, 2].
	Modifiers []*irv1.TypeModifier
	// Array marks an array of the named element type. Dimensions is the
	// declared number of dimensions; an array always has at least one (§8.6).
	Array      bool
	Dimensions uint32
	// ArrayName is the array type's own pg_type name. Empty means Postgres's
	// usual name, the element name prefixed with an underscore.
	ArrayName string
	// Raw is the type's text as the source printed it (§8.2).
	Raw string
	// Serial is set by ParseTypeText for the serial pseudo-types, which name
	// an integer type whose column defaults to nextval of its own sequence.
	Serial bool
}

// TypeKind says what a type outside the built-in table is.
type TypeKind int

const (
	// KindOther is a base type the core cannot classify, such as an
	// extension's type; it becomes opaque (§8.3).
	KindOther TypeKind = iota
	KindEnum
	KindDomain
	KindComposite
	KindRange
	KindMultirange
)

// Resolved is what a Resolver knows about a type name.
type Resolved struct {
	// Namespace is where the name resolved. It qualifies a name written
	// without a namespace.
	Namespace string
	Kind      TypeKind
	// Subtype is the element type of a range or multirange.
	Subtype *Native
}

// Resolver looks a type up in the catalog being read. namespace is empty for
// a name written unqualified, which the resolver resolves as the search path
// would. ok is false for a name the catalog does not have.
type Resolver func(namespace, name string) (r Resolved, ok bool)

// Types maps the Postgres types of one catalog to logical types.
type Types struct {
	// Catalog names the catalog in references to named types.
	Catalog string
	// Resolve answers for every name outside the built-in table. Nil means
	// such names are opaque.
	Resolve Resolver
}

// LogicalType returns n's logical type with its native type attached
// (§8.1, §8.2). Built-in types map by the tables of §8.1 and §8.8; enums,
// domains and composites become named references; anything else is opaque
// and keeps its native type (§8.3, §6.3).
func (t Types) LogicalType(n Native) (*irv1.Type, error) {
	ns, mods := n.Namespace, n.Modifiers
	if ns == "" && catalogTypes[n.Name] {
		ns = "pg_catalog"
	}
	elem, ns, err := t.element(ns, n.Name, mods)
	if err != nil {
		return nil, err
	}
	name := n.Name
	if n.Array {
		name = n.ArrayName
		if name == "" {
			name = "_" + n.Name
		}
		// Array elements carry no native type of their own (§8.6), except an
		// opaque element, whose native type is all that says what it is (§8.3).
		if elem.GetOpaque() == nil {
			elem.Native = nil
		}
		elem = &irv1.Type{Kind: &irv1.Type_Array{Array: &irv1.ArrayType{
			Element:         elem,
			ElementNullable: true,
			Dimensions:      max(n.Dimensions, 1),
		}}}
	}
	elem.Native = &irv1.NativeType{
		Name:      &irv1.QualifiedName{Namespace: ns, Object: name},
		Modifiers: mods,
		Raw:       n.Raw,
	}
	return elem, nil
}

// element maps a non-array type and returns the namespace the name resolved
// to. An opaque result carries its native type, so LogicalType can keep it on
// an opaque array element.
func (t Types) element(ns, name string, mods []*irv1.TypeModifier) (*irv1.Type, string, error) {
	if ns == "pg_catalog" {
		if b, ok := builtins[name]; ok {
			ints, err := intMods(name, mods, b.maxMods)
			if err != nil {
				return nil, "", err
			}
			typ := b.build(ints)
			if typ.GetOpaque() != nil {
				typ.Native = &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: ns, Object: name}, Modifiers: mods}
			}
			return typ, ns, nil
		}
	}
	var r Resolved
	ok := false
	if t.Resolve != nil {
		r, ok = t.Resolve(ns, name)
	}
	if ok && r.Namespace != "" {
		ns = r.Namespace
	}
	ref := &irv1.QualifiedName{Catalog: t.Catalog, Namespace: ns, Object: name}
	switch {
	case ok && r.Kind == KindEnum:
		return named(&irv1.NamedRef{Kind: &irv1.NamedRef_Enum{Enum: ref}}), ns, nil
	case ok && r.Kind == KindDomain:
		return named(&irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: ref}}), ns, nil
	case ok && r.Kind == KindComposite:
		return named(&irv1.NamedRef{Kind: &irv1.NamedRef_Composite{Composite: ref}}), ns, nil
	case ok && (r.Kind == KindRange || r.Kind == KindMultirange) && r.Subtype != nil:
		sub, err := t.LogicalType(*r.Subtype)
		if err != nil {
			return nil, "", err
		}
		if sub.GetOpaque() == nil {
			sub.Native = nil
		}
		if r.Kind == KindRange {
			return rangeOf(sub), ns, nil
		}
		return multirangeOf(sub), ns, nil
	}
	return &irv1.Type{
		Kind:   &irv1.Type_Opaque{Opaque: &irv1.Opaque{}},
		Native: &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: ns, Object: name}, Modifiers: mods},
	}, ns, nil
}

func named(r *irv1.NamedRef) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Named{Named: r}}
}

func rangeOf(e *irv1.Type) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Range{Range: &irv1.RangeType{Element: e}}}
}

func multirangeOf(e *irv1.Type) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Multirange{Multirange: &irv1.RangeType{Element: e}}}
}

// intMods checks that a built-in type has at most maxMods modifiers, all of
// them integers, and returns them. Only a numeric scale may be negative.
func intMods(name string, mods []*irv1.TypeModifier, maxMods int) ([]int32, error) {
	if len(mods) > maxMods {
		return nil, fmt.Errorf("pgsql: %s takes at most %d modifiers, got %d", name, maxMods, len(mods))
	}
	out := make([]int32, len(mods))
	for i, m := range mods {
		v, ok := m.GetKind().(*irv1.TypeModifier_Int)
		if !ok || v.Int < 0 && (name != "numeric" || i != 1) {
			return nil, fmt.Errorf("pgsql: %s modifier %d is not a non-negative integer", name, i+1)
		}
		out[i] = v.Int
	}
	return out, nil
}

// builtin maps one pg_catalog type. build receives at most maxMods modifiers.
type builtin struct {
	maxMods int
	build   func(mods []int32) *irv1.Type
}

func fixed(t func() *irv1.Type) builtin {
	return builtin{0, func([]int32) *irv1.Type { return t() }}
}

func empty() *irv1.Empty { return &irv1.Empty{} }

// timeType is a time type's precision: the declared one, else Postgres's
// default of microseconds (§8.8 rule 5).
func timeType(mods []int32) *irv1.TimeType {
	p := uint32(6)
	if len(mods) == 1 {
		p = uint32(mods[0])
	}
	return &irv1.TimeType{Precision: &p}
}

func instant(mods []int32) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Instant{Instant: timeType(mods)}}
}

func localDatetime(mods []int32) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_LocalDatetime{LocalDatetime: timeType(mods)}}
}

func date() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Date{Date: empty()}} }

func int32Type() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Int32{Int32: empty()}} }

func int64Type() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Int64{Int64: empty()}} }

// decimal maps numeric. numeric(p) has scale 0. A negative scale (Postgres
// 15 rounds to the left of the point) has no logical counterpart, so such a
// type is opaque.
func decimal(mods []int32) *irv1.Type {
	d := &irv1.Decimal{}
	if len(mods) > 0 {
		p, s := uint32(mods[0]), int32(0)
		if len(mods) == 2 {
			s = mods[1]
		}
		if s < 0 {
			return opaque()
		}
		d.Precision, d.Scale = &p, proto.Uint32(uint32(s))
	}
	return &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: d}}
}

func bit(varying bool) builtin {
	return builtin{1, func(mods []int32) *irv1.Type {
		b := &irv1.Bit{Varying: varying}
		if len(mods) == 1 {
			b.Length = proto.Uint32(uint32(mods[0]))
		}
		return &irv1.Type{Kind: &irv1.Type_Bit{Bit: b}}
	}}
}

func opaque() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}} }

// builtins maps pg_catalog types by name (§8.1, §8.8). Types missing here,
// such as name, oid, "char", tsvector and the geometric types, are opaque.
var builtins = map[string]builtin{
	"bool":    fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Bool{Bool: empty()}} }),
	"int2":    fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Int16{Int16: empty()}} }),
	"int4":    fixed(int32Type),
	"int8":    fixed(int64Type),
	"float4":  fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Float32{Float32: empty()}} }),
	"float8":  fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Float64{Float64: empty()}} }),
	"numeric": {2, decimal},
	"money":   fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Money{Money: empty()}} }),
	"text":    fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}} }),
	// varchar without a length has no limit, so it is text; the native type
	// still says varchar.
	"varchar": {1, func(mods []int32) *irv1.Type {
		if len(mods) == 0 {
			return &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}
		}
		return &irv1.Type{Kind: &irv1.Type_Varchar{Varchar: &irv1.Varchar{Length: uint32(mods[0]), Unit: irv1.LengthUnit_LENGTH_UNIT_CHARACTERS}}}
	}},
	// bpchar without a length (only ever spelled bpchar) pads to no fixed
	// length, which no logical type describes, so it is opaque.
	"bpchar": {1, func(mods []int32) *irv1.Type {
		if len(mods) == 0 {
			return opaque()
		}
		return &irv1.Type{Kind: &irv1.Type_Char{Char: &irv1.Char{Length: uint32(mods[0]), Unit: irv1.LengthUnit_LENGTH_UNIT_CHARACTERS}}}
	}},
	"bytea":       fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Bytes{Bytes: &irv1.Bytes{}}} }),
	"timestamptz": {1, instant},
	"timestamp":   {1, localDatetime},
	"date":        fixed(date),
	"time": {1, func(mods []int32) *irv1.Type {
		return &irv1.Type{Kind: &irv1.Type_LocalTime{LocalTime: timeType(mods)}}
	}},
	"timetz": {1, func(mods []int32) *irv1.Type {
		return &irv1.Type{Kind: &irv1.Type_TimeWithOffset{TimeWithOffset: timeType(mods)}}
	}},
	// interval's modifiers are its field mask and, optionally, its precision.
	"interval": {2, func(mods []int32) *irv1.Type {
		return &irv1.Type{Kind: &irv1.Type_Interval{Interval: timeType(mods[min(1, len(mods)):])}}
	}},
	"uuid":    fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Uuid{Uuid: empty()}} }),
	"json":    fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Json{Json: empty()}} }),
	"jsonb":   fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Jsonb{Jsonb: empty()}} }),
	"xml":     fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Xml{Xml: empty()}} }),
	"inet":    fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Inet{Inet: empty()}} }),
	"cidr":    fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Cidr{Cidr: empty()}} }),
	"macaddr": fixed(func() *irv1.Type { return &irv1.Type{Kind: &irv1.Type_Macaddr{Macaddr: empty()}} }),
	// macaddr8 holds EUI-64 addresses, which the logical macaddr (six-byte
	// addresses) does not describe, so it is opaque like any other type
	// without a logical counterpart (§8.3).
	"bit":    bit(false),
	"varbit": bit(true),

	"int4range":      fixed(func() *irv1.Type { return rangeOf(int32Type()) }),
	"int8range":      fixed(func() *irv1.Type { return rangeOf(int64Type()) }),
	"numrange":       fixed(func() *irv1.Type { return rangeOf(decimal(nil)) }),
	"tsrange":        fixed(func() *irv1.Type { return rangeOf(localDatetime(nil)) }),
	"tstzrange":      fixed(func() *irv1.Type { return rangeOf(instant(nil)) }),
	"daterange":      fixed(func() *irv1.Type { return rangeOf(date()) }),
	"int4multirange": fixed(func() *irv1.Type { return multirangeOf(int32Type()) }),
	"int8multirange": fixed(func() *irv1.Type { return multirangeOf(int64Type()) }),
	"nummultirange":  fixed(func() *irv1.Type { return multirangeOf(decimal(nil)) }),
	"tsmultirange":   fixed(func() *irv1.Type { return multirangeOf(localDatetime(nil)) }),
	"tstzmultirange": fixed(func() *irv1.Type { return multirangeOf(instant(nil)) }),
	"datemultirange": fixed(func() *irv1.Type { return multirangeOf(date()) }),
}

// catalogTypes holds the pg_catalog type names that an unqualified type name
// resolves to before any user namespace, because pg_catalog is searched first.
var catalogTypes = func() map[string]bool {
	m := map[string]bool{}
	for n := range builtins {
		m[n] = true
	}
	for _, n := range []string{
		"char", "name", "oid", "tid", "xid", "xid8", "cid", "macaddr8",
		"regclass", "regtype", "regproc", "regprocedure", "regoper", "regoperator",
		"regnamespace", "regrole", "regconfig", "regdictionary", "regcollation",
		"tsvector", "tsquery", "jsonpath", "pg_lsn", "pg_snapshot", "txid_snapshot",
		"point", "line", "lseg", "box", "path", "polygon", "circle",
		"int2vector", "oidvector", "record",
	} {
		m[n] = true
	}
	return m
}()

// Postgres's typmod encodings (src/include/utils/*.h).
const (
	varhdrsz          = 4
	intervalFullPrec  = 0xFFFF
	numericScaleMask  = 0x7FF
	numericScaleSign  = 1024
	intervalRangeMask = 0x7FFF
)

// DecodeTypmod turns a pg_attribute typmod of a built-in type into the
// modifiers its declaration spelled, as ParseTypeText produces them from the
// same declaration: varchar(80) is stored as 84 and decodes to [80]. For an
// array, name is the element type's name, whose typmod the column has. A typmod
// of -1 means none. ok is false for a typmod whose encoding this package
// does not know (any type but the character, numeric, time and bit types);
// such a type's modifiers come from ParseTypeText over format_type instead.
func DecodeTypmod(name string, typmod int32) (mods []*irv1.TypeModifier, ok bool) {
	if typmod < 0 {
		return nil, true
	}
	switch name {
	case "varchar", "bpchar":
		return intMod(typmod - varhdrsz), true
	case "numeric":
		t := typmod - varhdrsz
		scale := ((t & numericScaleMask) ^ numericScaleSign) - numericScaleSign
		return append(intMod((t>>16)&0xFFFF), intMod(scale)...), true
	case "timestamp", "timestamptz", "time", "timetz", "bit", "varbit":
		return intMod(typmod), true
	case "interval":
		mods := intMod((typmod >> 16) & intervalRangeMask)
		if p := typmod & 0xFFFF; p != intervalFullPrec {
			mods = append(mods, intMod(p)...)
		}
		return mods, true
	}
	return nil, false
}

func intMod(v int32) []*irv1.TypeModifier {
	return []*irv1.TypeModifier{{Kind: &irv1.TypeModifier_Int{Int: v}}}
}
