package gen

import (
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/runtime/go/civil"
)

// CodeValidationUnsupported: a client-validated constraint the emitter
// cannot check on its field's type.
const CodeValidationUnsupported = "go-validation-unsupported"

// family groups the base types a check compares the same way.
type family int

const (
	famNone family = iota
	famString
	famOrdered // integers and floats: cmp.Compare
	famDecimal
	famDate
	famTime
	famDateTime
	famBool
)

// baseOf follows domains to the type a value is stored as, and returns it
// with its Go type and family. A domain's value converts to the Go type.
func (g *generator) baseOf(f *file, t *irv1.Type) (*irv1.Type, string, family) {
	for t.GetNamed().GetDomain() != nil {
		t = g.named[keyOf(t.GetNamed().GetDomain())].base
	}
	name := ""
	if k := t.ProtoReflect().WhichOneof(t.ProtoReflect().Descriptor().Oneofs().ByName("kind")); k != nil {
		name = string(k.Name())
	}
	switch name {
	case "text", "varchar", "char", "xml":
		return t, "string", famString
	case "int8", "int16", "int32", "int64", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
		return t, scalars[name], famOrdered
	case "decimal", "money":
		return t, f.use(pkgDec, "dec") + ".Decimal", famDecimal
	case "date":
		return t, f.use(pkgCiv, "civil") + ".Date", famDate
	case "local_time":
		return t, f.use(pkgCiv, "civil") + ".Time", famTime
	case "local_datetime":
		return t, f.use(pkgCiv, "civil") + ".DateTime", famDateTime
	case "bool":
		return t, "bool", famBool
	case "opaque":
		_, mapped := g.o.Types[t.GetNative().GetName().GetNamespace()+"."+t.GetNative().GetName().GetObject()]
		if !mapped && t.GetOpaque().GetFallback() == irv1.OpaqueFallback_OPAQUE_FALLBACK_STRING {
			return t, "string", famString
		}
	}
	return t, "", famNone
}

// constraintOf finds a field's constraint by name: on the domains of its
// type, outermost first, then on the shape's table, as the core lists them
// (§10.5). It returns the constraint and its owner.
func (g *generator) constraintOf(t *irv1.Type, table *irv1.Table, owner *irv1.QualifiedName, name string) (*irv1.Constraint, *irv1.QualifiedName) {
	for t.GetNamed().GetDomain() != nil {
		n := g.named[keyOf(t.GetNamed().GetDomain())]
		for _, c := range n.constraints {
			if c.GetName() == name {
				return c, n.q
			}
		}
		t = n.base
	}
	for _, c := range table.GetConstraints() {
		if c.GetName() == name {
			return c, owner
		}
	}
	return nil, nil
}

var unitWords = map[irv1.LengthUnit]string{
	irv1.LengthUnit_LENGTH_UNIT_CHARACTERS:       "UnitCharacters",
	irv1.LengthUnit_LENGTH_UNIT_BYTES:            "UnitBytes",
	irv1.LengthUnit_LENGTH_UNIT_UTF16_CODE_UNITS: "UnitUTF16CodeUnits",
}

// check returns the variable of a constraint's check, declaring it in
// dql-checks.dql.go the first time (§10.2, §10.3), and the Go type the
// check takes. It fails a kind the field's type cannot be checked by.
func (g *generator) check(c *irv1.Constraint, owner *irv1.QualifiedName, t *irv1.Type, at *irv1.QualifiedName) (string, string, bool) {
	qualified := displayName(owner) + "." + c.GetName()
	if g.checkFile == nil {
		g.checkFile = g.newFile("dql-checks")
	}
	f := g.checkFile
	base, goType, fam := g.baseOf(f, t)
	if name, ok := g.checks[qualified]; ok {
		return name, goType, name != ""
	}
	dql := f.use(pkgDQL, "dql")
	fail := func(why string) (string, string, bool) {
		// Once per constraint, though its domain and each field check it.
		g.checks[qualified] = ""
		g.errorf(CodeValidationUnsupported, at, "constraint %s is client-validated, but the go emitter cannot check it: %s (§10.2)", qualified, why)
		return "", "", false
	}
	var decl string
	p := c.GetPortable()
	switch k := p.GetKind().(type) {
	case *irv1.PortableConstraint_Length:
		unit, ok := unitWords[k.Length.GetUnit()]
		if fam != famString || !ok {
			return fail("a length needs text and a unit")
		}
		decl = fmt.Sprintf("%s.LengthCheck{Constraint: %q", dql, qualified)
		if k.Length.Min != nil {
			decl += fmt.Sprintf(", Min: %s.Ptr(%d)", dql, k.Length.GetMin())
		}
		if k.Length.Max != nil {
			decl += fmt.Sprintf(", Max: %s.Ptr(%d)", dql, k.Length.GetMax())
		}
		decl += fmt.Sprintf(", Unit: %s.%s}", dql, unit)
	case *irv1.PortableConstraint_NotEmpty:
		if fam != famString {
			return fail("not_empty needs text")
		}
		decl = fmt.Sprintf("%s.NotEmptyCheck{Constraint: %q}", dql, qualified)
	case *irv1.PortableConstraint_Pattern:
		if fam != famString {
			return fail("a pattern needs text")
		}
		if _, err := regexp.Compile(k.Pattern.GetRegex()); err != nil {
			return fail("its regex does not compile: " + err.Error())
		}
		decl = fmt.Sprintf("%s.PatternCheck{Constraint: %q, Regex: %s.MustCompile(%q)}", dql, qualified, f.use("regexp", "regexp"), k.Pattern.GetRegex())
	case *irv1.PortableConstraint_Digits:
		if fam != famDecimal {
			return fail("digits needs a decimal")
		}
		decl = fmt.Sprintf("%s.DigitsCheck{Constraint: %q, Precision: %d, Scale: %d}", dql, qualified, k.Digits.GetPrecision(), k.Digits.GetScale())
	case *irv1.PortableConstraint_Range:
		compare := map[family]string{famDecimal: goType + ".Cmp", famDate: goType + ".Compare", famTime: goType + ".Compare", famDateTime: goType + ".Compare"}[fam]
		if fam == famOrdered {
			compare = f.use("cmp", "cmp") + ".Compare[" + goType + "]"
		}
		if compare == "" {
			return fail("a range needs a number, a date or a time")
		}
		decl = fmt.Sprintf("%s.RangeCheck[%s]{Constraint: %q", dql, goType, qualified)
		for _, b := range []struct {
			name string
			v    *irv1.Value
		}{{"Min", k.Range.GetMin()}, {"Max", k.Range.GetMax()}} {
			if b.v == nil {
				continue
			}
			lit, ok := g.literal(f, base, fam, b.v)
			if !ok {
				return fail("its bound " + quoted(b.v) + " is not a value of its type")
			}
			decl += fmt.Sprintf(", %s: %s.Ptr[%s](%s)", b.name, dql, goType, lit)
		}
		decl += fmt.Sprintf(", MinInclusive: %t, MaxInclusive: %t, Compare: %s}", k.Range.GetMinInclusive(), k.Range.GetMaxInclusive(), compare)
	case *irv1.PortableConstraint_Membership:
		if fam == famNone || fam == famDecimal {
			return fail("membership needs a comparable value")
		}
		var values []string
		for _, v := range k.Membership.GetValues() {
			lit, ok := g.literal(f, base, fam, v)
			if !ok {
				return fail("its value " + quoted(v) + " is not a value of its type")
			}
			values = append(values, lit)
		}
		decl = fmt.Sprintf("%s.MembershipCheck[%s]{Constraint: %q, Values: []%s{%s}}", dql, goType, qualified, goType, strings.Join(values, ", "))
	default:
		return fail("it is not a portable kind this emitter knows")
	}
	// The constraint's words, after its owner's when the name does not
	// start with them: posts_title_check is dqlPostsTitleCheck.
	words := splitWords(c.GetName())
	if prefix := splitWords(owner.GetObject()); !slices.Equal(prefix, words[:min(len(prefix), len(words))]) {
		words = append(prefix, words...)
	}
	name := fresh("dql"+Exported(words), g.checkNames)
	g.checks[qualified] = name
	f.printf("// %s checks %s.\nvar %s = %s\n\n", name, qualified, name, decl)
	return name, goType, true
}

// splitWords splits a stored name at underscores and other separators.
func splitWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	})
}

// valueText is a constraint's value as text: a string, a number or a bool.
func valueText(v *irv1.Value) string {
	switch k := v.GetKind().(type) {
	case *irv1.Value_String_:
		return k.String_
	case *irv1.Value_Int:
		return strconv.FormatInt(k.Int, 10)
	case *irv1.Value_Uint:
		return strconv.FormatUint(k.Uint, 10)
	case *irv1.Value_Decimal:
		return k.Decimal
	case *irv1.Value_Float:
		return strconv.FormatFloat(k.Float, 'g', -1, 64)
	case *irv1.Value_Bool:
		return strconv.FormatBool(k.Bool)
	}
	return ""
}

// quoted is valueText, quoted when v is a string.
func quoted(v *irv1.Value) string {
	if v.GetKind() == nil {
		return "(unset)"
	}
	if _, ok := v.GetKind().(*irv1.Value_String_); ok {
		return strconv.Quote(valueText(v))
	}
	return valueText(v)
}

// literal spells a constraint's value as a Go value of the family, or fails
// one the family cannot hold.
func (g *generator) literal(f *file, t *irv1.Type, fam family, v *irv1.Value) (string, bool) {
	text := valueText(v)
	_, isString := v.GetKind().(*irv1.Value_String_)
	_, isBool := v.GetKind().(*irv1.Value_Bool)
	switch fam {
	case famString:
		return strconv.Quote(text), isString
	case famBool:
		return text, isBool
	case famOrdered:
		kind := t.ProtoReflect().WhichOneof(t.ProtoReflect().Descriptor().Oneofs().ByName("kind")).TextName()
		integer := strings.HasPrefix(kind, "int") || strings.HasPrefix(kind, "uint")
		r, ok := new(big.Rat).SetString(text)
		if !ok || isString || isBool || (integer && !r.IsInt()) {
			return "", false
		}
		if integer {
			return r.Num().String(), true
		}
		return text, true
	case famDecimal:
		if _, ok := new(big.Rat).SetString(text); !ok || isBool {
			return "", false
		}
		return f.use(pkgDec, "dec") + ".MustParse(" + strconv.Quote(text) + ")", true
	}
	// A date or time, which the emitter parses now so the generated code
	// cannot panic.
	var err error
	fn := "ParseDateTime"
	switch fam {
	case famDate:
		fn = "ParseDate"
		_, err = civil.ParseDate(text)
	case famTime:
		fn = "ParseTime"
		_, err = civil.ParseTime(text)
	default:
		_, err = civil.ParseDateTime(text)
	}
	if err != nil || !isString {
		return "", false
	}
	return fmt.Sprintf("%s.Must(%s.%s(%q))", f.use(pkgDQL, "dql"), f.use(pkgCiv, "civil"), fn, text), true
}

// checkCalls returns the statements that check value, a non-null value of
// type t, against the named constraints: each check runs on the value
// converted to the Go type it takes.
func (g *generator) checkCalls(names []string, t *irv1.Type, table *irv1.Table, owner, at *irv1.QualifiedName, v, shape, field, value string) []string {
	var out []string
	for _, n := range names {
		c, cOwner := g.constraintOf(t, table, owner, n)
		if c == nil {
			g.errorf(CodeReferenceUnknown, at, "field %s is checked against constraint %s, which neither its domains nor %s hold", field, n, displayName(owner))
			continue
		}
		name, goType, ok := g.check(c, cOwner, t, at)
		if !ok {
			continue
		}
		arg := value
		if t.GetNamed().GetDomain() != nil {
			arg = goType + "(" + value + ")"
		}
		out = append(out, fmt.Sprintf("%s.Check(&%s, %q, %q, %s)", name, v, shape, field, arg))
	}
	return out
}

// domainParse generates the domain's constructor, which runs the domain's
// client-validated constraints, outermost domain first (§8.5, §10.5).
func (g *generator) domainParse(f *file, q *irv1.QualifiedName, d *irv1.Domain) {
	name := Exported(d.GetHostName().GetWords())
	parse := "Parse" + name
	g.declare(parse, "the constructor of domain "+displayName(q), q)
	t := &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: q}}}}
	_, goType, _ := g.baseOf(f, t)
	if goType == "" {
		goType = g.goType(f, d.GetBase(), q, "domain "+displayName(q))
	}
	dql := f.use(pkgDQL, "dql")
	var body []string
	for typ := t; typ.GetNamed().GetDomain() != nil; typ = g.named[keyOf(typ.GetNamed().GetDomain())].base {
		n := g.named[keyOf(typ.GetNamed().GetDomain())]
		for _, c := range n.constraints {
			if !c.GetClientValidated() {
				continue
			}
			if check, _, ok := g.check(c, n.q, t, q); ok {
				body = append(body, fmt.Sprintf("%s.Check(&issues, %q, \"\", x)", check, displayName(n.q)))
			}
		}
	}
	f.printf("// %s converts x to %s when it passes the\n// domain's constraints (§8.5, §10.5), and returns %s.ValidationErrors\n// otherwise.\n", parse, name, dql)
	f.printf("func %s(x %s) (%s, error) {\n\tvar issues %s.ValidationErrors\n", parse, goType, name, dql)
	for _, b := range body {
		f.printf("\t%s\n", b)
	}
	f.printf("\tif err := issues.Err(); err != nil {\n\t\treturn *new(%s), err\n\t}\n\treturn %s(x), nil\n}\n\n", name, name)
}

// writeShape reports whether s is a write shape, which gets a validator
// (§10.5): a Draft or a Partial, or a custom shape derived from one.
func writeShape(s *irv1.Shape) bool {
	p := rootPreset(s)
	return p == irv1.Preset_PRESET_DRAFT || p == irv1.Preset_PRESET_PARTIAL
}

// validator generates a write shape's Validate, which checks every field it
// holds against its client-validated constraints and every nested shape or
// change in turn, with each issue's path from s (§10.5). It returns nil or
// dql.ValidationErrors.
func (g *generator) validator(s *irv1.Shape) {
	f := g.shapeFile(s)
	typ := Exported(s.GetHostName().GetWords())
	shape := g.dotted(s.GetId())
	if !g.method(s, "Validate", "the validator of "+shape) {
		return
	}
	q := s.GetEntity()
	t := g.ix.tables[keyOf(q)]
	recv := receiver(s.GetHostName().GetWords())
	used := map[string]bool{recv: true, "ok": true}
	v, x, xs, i := fresh("v", used), fresh("x", used), fresh("xs", used), fresh("i", used)
	dql := f.use(pkgDQL, "dql")
	var body []string
	for _, fld := range s.GetFields() {
		if len(fld.GetValidation()) == 0 {
			continue
		}
		at := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: fld.GetColumn()}
		field := recv + "." + g.fieldName(s, fld.GetColumn())
		optional := fld.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL
		value := field
		if optional {
			value = x
		}
		if fld.GetNullable() {
			value = "*" + value
		}
		calls := g.checkCalls(fld.GetValidation(), fld.GetType(), t, q, at, v, shape, fld.GetColumn(), value)
		block := "{\n\t\t" + strings.Join(calls, "\n\t\t") + "\n\t}"
		switch {
		case len(calls) == 0:
		case optional && fld.GetNullable():
			body = append(body, "if "+x+", ok := "+field+".Get(); ok && "+x+" != nil "+block)
		case optional:
			body = append(body, "if "+x+", ok := "+field+".Get(); ok "+block)
		case fld.GetNullable():
			body = append(body, "if "+field+" != nil "+block)
		default:
			body = append(body, calls...)
		}
	}
	for _, n := range s.GetNests() {
		if !g.validates(n) {
			continue
		}
		r := relation(t, n.GetRelation())
		field := recv + "." + Exported(r.GetName().GetWords())
		one := func(e, path string) string {
			call := v + " = append(" + v + ", " + e + ".validate().At(" + path + ")...)"
			if n.GetNullable() {
				return "if " + e + " != nil {\n\t\t" + call + "\n\t}"
			}
			return call
		}
		optional := n.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL && (!toMany(r) || rootPreset(s) == irv1.Preset_PRESET_PARTIAL)
		var stmt string
		if toMany(r) {
			list := field
			if optional {
				list = xs
			}
			path := strconv.Quote(n.GetRelation()+"[") + "+" + f.use("strconv", "strconv") + ".Itoa(" + i + ")+\"]\""
			stmt = "for " + i + ", " + x + " := range " + list + " {\n\t\t" + strings.ReplaceAll(one(x, path), "\n", "\n\t") + "\n\t}"
			if optional {
				stmt = "if " + xs + ", ok := " + field + ".Get(); ok {\n\t" + strings.ReplaceAll(stmt, "\n", "\n\t") + "\n\t}"
			}
		} else if optional {
			stmt = "if " + x + ", ok := " + field + ".Get(); ok {\n\t\t" + strings.ReplaceAll(one(x, strconv.Quote(n.GetRelation())), "\n", "\n\t") + "\n\t}"
		} else {
			stmt = one(field, strconv.Quote(n.GetRelation()))
		}
		body = append(body, stmt)
	}
	f.printf("// Validate checks %s against the client-validated constraints of its fields\n// and of what it nests (§10.5): nil, or %s.ValidationErrors with each\n// issue's path.\nfunc (%s %s) Validate() error { return %s.validate().Err() }\n\n",
		recv, dql, recv, typ, recv)
	f.printf("func (%s %s) validate() %s.ValidationErrors {\n\tvar %s %s.ValidationErrors\n", recv, typ, dql, v, dql)
	for _, b := range body {
		f.printf("\t%s\n", b)
	}
	f.printf("\treturn %s\n}\n\n", v)
}

// validates reports whether a nest's elements have a validator: a write
// shape, or a reference or change, which validates what it creates or
// updates.
func (g *generator) validates(n *irv1.Nest) bool {
	if n.GetUnion() != nil {
		return true
	}
	return writeShape(g.ix.shapes[shapeKey(n.GetShape())])
}

// unionValidator generates a union's validate: the Draft it creates or the
// Partial it updates, and nothing for a link, delete or unlink (§10.5).
func (g *generator) unionValidator(f *file, u *irv1.RelationUnion, name, draft, partial string) {
	r := receiver(u.GetHostName().GetWords())
	dql := f.use(pkgDQL, "dql")
	f.printf("func (%s %s) validate() %s.ValidationErrors {\n", r, name, dql)
	if draft != "" {
		f.printf("\tif %s.change == %s.ChangeCreate {\n\t\treturn %s.draft.validate()\n\t}\n", r, dql, r)
	}
	if partial != "" {
		f.printf("\tif %s.change == %s.ChangeUpdate {\n\t\treturn %s.partial.validate()\n\t}\n", r, dql, r)
	}
	f.printf("\treturn nil\n}\n\n")
}

// validators generates the validator of every write shape (§10.5).
func (g *generator) validators() {
	for _, s := range g.b.GetShapes().GetShapes() {
		if writeShape(s) {
			g.validator(s)
		}
	}
}
