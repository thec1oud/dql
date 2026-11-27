package gen

import (
	"encoding/json"
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
const CodeValidationUnsupported = "ts-validation-unsupported"

// family groups the base types a check compares the same way.
type family int

const (
	famNone family = iota
	famString
	famNumber // integers below 64 bits and floats
	famBigint // 64-bit integers
	famDecimal
	famDate
	famTime
	famDateTime
	famBool
)

// temporalTypes are the Temporal types of the date and time families.
var temporalTypes = map[family]string{famDate: "PlainDate", famTime: "PlainTime", famDateTime: "PlainDateTime"}

// jsString spells s as a JavaScript string literal.
func jsString(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s) // a string always encodes
	return strings.TrimSuffix(b.String(), "\n")
}

// baseOf follows domains to the type a value is stored as, and returns it
// with its TypeScript type and family. A domain's value is a value of that
// type, so a check takes it as it is.
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
	case "int8", "int16", "int32", "uint8", "uint16", "uint32", "float32", "float64":
		return t, "number", famNumber
	case "int64", "uint64":
		return t, "bigint", famBigint
	case "decimal", "money":
		return t, f.rt("Decimal", true), famDecimal
	case "date", "local_time", "local_datetime":
		fam := map[string]family{"date": famDate, "local_time": famTime, "local_datetime": famDateTime}[name]
		return t, f.rt("Temporal", true) + "." + temporalTypes[fam], fam
	case "bool":
		return t, "boolean", famBool
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
	irv1.LengthUnit_LENGTH_UNIT_CHARACTERS:       "characters",
	irv1.LengthUnit_LENGTH_UNIT_BYTES:            "bytes",
	irv1.LengthUnit_LENGTH_UNIT_UTF16_CODE_UNITS: "utf16_code_units",
}

// check returns the name of a constraint's check, declaring it in
// dql-checks.dql.ts the first time (§10.2, §10.3). It fails a kind the
// field's type cannot be checked by, once per constraint.
func (g *generator) check(c *irv1.Constraint, owner *irv1.QualifiedName, t *irv1.Type, at *irv1.QualifiedName) (string, bool) {
	// The constraint's words, after its owner's when the name does not
	// start with them: posts_title_check is dqlPostsTitleCheck.
	words := splitWords(c.GetName())
	if prefix := splitWords(owner.GetObject()); !slices.Equal(prefix, words[:min(len(prefix), len(words))]) {
		words = append(prefix, words...)
	}
	qualified := displayName(owner) + "." + c.GetName()
	return g.checkAs(c, qualified, qualified, words, t, at)
}

// checkAs is check for a constraint named qualified in issues, cached under
// key, whose check is named from words.
func (g *generator) checkAs(c *irv1.Constraint, qualified, key string, words []string, t *irv1.Type, at *irv1.QualifiedName) (string, bool) {
	if name, ok := g.checks[key]; ok {
		return name, name != ""
	}
	if g.checkFile == nil {
		g.checkFile = g.newFile("dql-checks")
		g.checkFile.internal = true
	}
	f := g.checkFile
	base, tsType, fam := g.baseOf(f, t)
	fail := func(why string) (string, bool) {
		// Once per constraint, though its domain and each field check it.
		g.checks[key] = ""
		g.errorf(CodeValidationUnsupported, at, "constraint %s is client-validated, but the ts emitter cannot check it: %s (§10.2)", qualified, why)
		return "", false
	}
	cq := jsString(qualified)
	var decl string
	p := c.GetPortable()
	switch k := p.GetKind().(type) {
	case *irv1.PortableConstraint_Length:
		unit, ok := unitWords[k.Length.GetUnit()]
		if fam != famString || !ok {
			return fail("a length needs text and a unit")
		}
		args := []string{cq, jsString(unit)}
		switch {
		case k.Length.Max != nil:
			min := "undefined"
			if k.Length.Min != nil {
				min = strconv.FormatUint(uint64(k.Length.GetMin()), 10)
			}
			args = append(args, min, strconv.FormatUint(uint64(k.Length.GetMax()), 10))
		case k.Length.Min != nil:
			args = append(args, strconv.FormatUint(uint64(k.Length.GetMin()), 10))
		}
		decl = fmt.Sprintf("new %s(%s)", f.rt("LengthCheck", true), strings.Join(args, ", "))
	case *irv1.PortableConstraint_NotEmpty:
		if fam != famString {
			return fail("not_empty needs text")
		}
		decl = fmt.Sprintf("new %s(%s)", f.rt("NotEmptyCheck", true), cq)
	case *irv1.PortableConstraint_Pattern:
		if fam != famString {
			return fail("a pattern needs text")
		}
		if _, err := regexp.Compile(k.Pattern.GetRegex()); err != nil {
			return fail("its regex does not compile: " + err.Error())
		}
		re, err := jsRegex(k.Pattern.GetRegex())
		if err != nil {
			return fail("its regex is not in the portable subset: " + err.Error())
		}
		decl = fmt.Sprintf("new %s(%s, %s, %s)", f.rt("PatternCheck", true), cq, jsString(k.Pattern.GetRegex()), re)
	case *irv1.PortableConstraint_Digits:
		if fam != famDecimal {
			return fail("digits needs a decimal")
		}
		decl = fmt.Sprintf("new %s(%s, %d, %d)", f.rt("DigitsCheck", true), cq, k.Digits.GetPrecision(), k.Digits.GetScale())
	case *irv1.PortableConstraint_Range:
		var compare string
		switch fam {
		case famNumber, famBigint:
			compare = "(a, b) => (a < b ? -1 : a > b ? 1 : 0)"
		case famDecimal:
			compare = "(a, b) => a.cmp(b)"
		case famDate, famTime, famDateTime:
			compare = tsType + ".compare"
		default:
			return fail("a range needs a number, a date or a time")
		}
		var bounds []string
		for _, b := range []struct {
			name string
			v    *irv1.Value
		}{{"min", k.Range.GetMin()}, {"max", k.Range.GetMax()}} {
			if b.v == nil {
				continue
			}
			lit, ok := g.literal(f, base, fam, tsType, b.v)
			if !ok {
				return fail("its bound " + quoted(b.v) + " is not a value of its type")
			}
			bounds = append(bounds, b.name+": "+lit)
		}
		bounds = append(bounds, fmt.Sprintf("minInclusive: %t", k.Range.GetMinInclusive()), fmt.Sprintf("maxInclusive: %t", k.Range.GetMaxInclusive()))
		decl = fmt.Sprintf("new %s<%s>(%s, { %s }, %s)", f.rt("RangeCheck", true), tsType, cq, strings.Join(bounds, ", "), compare)
	case *irv1.PortableConstraint_Membership:
		if fam == famNone || fam == famDecimal {
			return fail("membership needs a comparable value")
		}
		var values []string
		for _, v := range k.Membership.GetValues() {
			lit, ok := g.literal(f, base, fam, tsType, v)
			if !ok {
				return fail("its value " + quoted(v) + " is not a value of its type")
			}
			values = append(values, lit)
		}
		decl = fmt.Sprintf("new %s<%s>(%s, [%s]", f.rt("MembershipCheck", true), tsType, cq, strings.Join(values, ", "))
		if temporalTypes[fam] != "" {
			decl += ", (a, b) => a.equals(b)"
		}
		decl += ")"
	default:
		return fail("it is not a portable kind this emitter knows")
	}
	name := fresh("dql"+TypeName(words), g.checkNames)
	g.checks[key] = name
	g.declare(f, name, "the check of constraint "+qualified, at)
	f.printf("/** %s checks %s. */\nexport const %s = %s;\n\n", name, qualified, name, decl)
	return name, true
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

// plainNumber is the decimal syntax the runtime's Decimal.parse and JS
// number literals share.
var plainNumber = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+)?$`)

// literal spells a constraint's value as a TypeScript value of the family,
// or fails one the family cannot hold.
func (g *generator) literal(f *file, t *irv1.Type, fam family, tsType string, v *irv1.Value) (string, bool) {
	text := valueText(v)
	_, isString := v.GetKind().(*irv1.Value_String_)
	_, isBool := v.GetKind().(*irv1.Value_Bool)
	switch fam {
	case famString:
		return jsString(text), isString
	case famBool:
		return text, isBool
	case famNumber, famBigint:
		kind := t.ProtoReflect().WhichOneof(t.ProtoReflect().Descriptor().Oneofs().ByName("kind")).TextName()
		integer := strings.HasPrefix(kind, "int") || strings.HasPrefix(kind, "uint")
		r, ok := new(big.Rat).SetString(text)
		if !ok || isString || isBool || !plainNumber.MatchString(text) || (integer && !r.IsInt()) {
			return "", false
		}
		switch {
		case fam == famBigint:
			return r.Num().String() + "n", true
		case integer:
			return r.Num().String(), true
		}
		return text, true
	case famDecimal:
		if !plainNumber.MatchString(text) || isBool {
			return "", false
		}
		return f.rt("Decimal", true) + ".parse(" + jsString(text) + ")", true
	}
	// A date or time, which the emitter parses now so the generated module
	// cannot throw when it loads.
	var err error
	switch fam {
	case famDate:
		_, err = civil.ParseDate(text)
	case famTime:
		_, err = civil.ParseTime(text)
	default:
		_, err = civil.ParseDateTime(text)
	}
	if err != nil || !isString {
		return "", false
	}
	return tsType + ".from(" + jsString(text) + ")", true
}

// fact returns the check of the structured fact of t's base type (§10.1): a
// varchar(n) or char(n) length in the type's unit (§10.3), or a decimal's
// precision and scale. The fact is named for the innermost domain whose
// base type it is, or for the column at. It returns false when the type
// states no such fact.
func (g *generator) fact(t *irv1.Type, at *irv1.QualifiedName) (string, bool) {
	owner := at
	for t.GetNamed().GetDomain() != nil {
		owner = t.GetNamed().GetDomain()
		t = g.named[keyOf(owner)].base
	}
	var p *irv1.PortableConstraint
	kind := "length"
	switch k := t.GetKind().(type) {
	case *irv1.Type_Varchar:
		p = lengthFact(k.Varchar.GetLength(), k.Varchar.GetUnit())
	case *irv1.Type_Char:
		p = lengthFact(k.Char.GetLength(), k.Char.GetUnit())
	case *irv1.Type_Decimal:
		if k.Decimal.Precision != nil {
			kind = "digits"
			p = &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Digits{Digits: &irv1.DigitsConstraint{Precision: k.Decimal.GetPrecision(), Scale: k.Decimal.GetScale()}}}
		}
	}
	if p == nil {
		return "", false
	}
	qualified := displayName(owner)
	words := append(splitWords(owner.GetObject()), splitWords(owner.GetMember())...)
	return g.checkAs(&irv1.Constraint{Form: &irv1.Constraint_Portable{Portable: p}}, qualified, "fact "+qualified, append(words, kind), t, at)
}

// lengthFact is the length fact of a bounded text type, or nil when it has
// no length or no unit.
func lengthFact(n uint32, unit irv1.LengthUnit) *irv1.PortableConstraint {
	if n == 0 || unit == irv1.LengthUnit_LENGTH_UNIT_UNSPECIFIED {
		return nil
	}
	return &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Length{Length: &irv1.LengthConstraint{Max: &n, Unit: unit}}}
}

// checkCalls returns the statements that check value, a non-null value of
// type t, against the fact of its type and then the named constraints,
// pushing issues to v.
func (g *generator) checkCalls(f *file, names []string, t *irv1.Type, table *irv1.Table, owner, at *irv1.QualifiedName, v, shape, field, value string) []string {
	var out []string
	if name, ok := g.fact(t, at); ok {
		out = append(out, fmt.Sprintf("%s.check(%s, %s, %s, %s);", g.ref(f, name, true), v, jsString(shape), jsString(field), value))
	}
	for _, n := range names {
		c, cOwner := g.constraintOf(t, table, owner, n)
		if c == nil {
			g.errorf(CodeReferenceUnknown, at, "field %s is checked against constraint %s, which neither its domains nor %s hold", field, n, displayName(owner))
			continue
		}
		name, ok := g.check(c, cOwner, t, at)
		if !ok {
			continue
		}
		out = append(out, fmt.Sprintf("%s.check(%s, %s, %s, %s);", g.ref(f, name, true), v, jsString(shape), jsString(field), value))
	}
	return out
}

// domainParse generates the domain's constructor, which runs the domain's
// client-validated constraints, outermost domain first, and throws a
// ValidationError with every issue (§8.5, §10.5).
func (g *generator) domainParse(f *file, q *irv1.QualifiedName, d *irv1.Domain) {
	name := TypeName(d.GetHostName().GetWords())
	parse := "parse" + name
	g.declare(f, parse, "the constructor of domain "+displayName(q), q)
	t := &irv1.Type{Kind: &irv1.Type_Named{Named: &irv1.NamedRef{Kind: &irv1.NamedRef_Domain{Domain: q}}}}
	base := d.GetBase()
	for base.GetNamed().GetDomain() != nil {
		base = g.named[keyOf(base.GetNamed().GetDomain())].base
	}
	baseType := g.tsType(f, base, q, "domain "+displayName(q))
	var body []string
	if check, ok := g.fact(t, q); ok {
		body = append(body, fmt.Sprintf("%s.check(issues, %s, \"\", x);", g.ref(f, check, true), jsString(displayName(q))))
	}
	for typ := t; typ.GetNamed().GetDomain() != nil; typ = g.named[keyOf(typ.GetNamed().GetDomain())].base {
		n := g.named[keyOf(typ.GetNamed().GetDomain())]
		for _, c := range n.constraints {
			if !c.GetClientValidated() {
				continue
			}
			if check, ok := g.check(c, n.q, t, q); ok {
				body = append(body, fmt.Sprintf("%s.check(issues, %s, \"\", x);", g.ref(f, check, true), jsString(displayName(n.q))))
			}
		}
	}
	f.printf("/**\n * %s returns x as %s when it passes the domain's\n * constraints (§8.5, §10.5), and throws a ValidationError otherwise.\n */\n", parse, name)
	f.printf("export function %s(x: %s): %s {\n", parse, baseType, name)
	if len(body) > 0 {
		f.printf("  const issues: %s[] = [];\n", f.rt("ValidationIssue", false))
		for _, b := range body {
			f.printf("  %s\n", b)
		}
		f.printf("  %s(issues);\n", f.rt("throwIfIssues", true))
	}
	f.printf("  return x as %s;\n}\n\n", name)
}

// writeShape reports whether s is a write shape, which gets a validator
// (§10.5): a Draft or a Partial, or a custom shape derived from one.
func writeShape(s *irv1.Shape) bool {
	p := rootPreset(s)
	return p == irv1.Preset_PRESET_DRAFT || p == irv1.Preset_PRESET_PARTIAL
}

// validatorName names the validator of a shape or union, as the record
// names validatePostDraft (§10.5).
func validatorName(words []string) string {
	return "validate" + TypeName(words)
}

// validates reports whether a union validates: whether it can create or
// update, which validates the Draft or Partial it carries.
func validates(u *irv1.RelationUnion) bool {
	return slices.ContainsFunc(u.GetVariants(), func(v *irv1.RelationVariant) bool { return v.GetPayload() != nil })
}

// validators generates the validator of every write shape and of every
// union that creates or updates (§10.5). Every name is declared first, as a
// validator calls those of what it nests.
func (g *generator) validators() {
	var shapes []*irv1.Shape
	for _, s := range g.b.GetShapes().GetShapes() {
		if writeShape(s) && g.ix.tables[keyOf(s.GetEntity())] != nil {
			g.declare(g.shapeFile(s), validatorName(s.GetHostName().GetWords()), "the validator of "+g.dotted(s.GetId()), s.GetEntity())
			shapes = append(shapes, s)
		}
	}
	var unions []*irv1.RelationUnion
	for _, u := range g.b.GetShapes().GetUnions() {
		if validates(u) && g.ix.tables[keyOf(u.GetId().GetEntity())] != nil {
			g.declare(g.entityFile(u.GetId().GetEntity()), validatorName(u.GetHostName().GetWords()),
				"the validator of relation union "+strings.Join(u.GetHostName().GetWords(), " "), u.GetId().GetEntity())
			unions = append(unions, u)
		}
	}
	for _, s := range shapes {
		g.validator(s)
	}
	for _, u := range unions {
		g.unionValidator(u)
	}
}

// nestValidator is the validator of what a nest holds, or "" when it holds
// nothing a write checks: a read shape, or a union that only links.
func (g *generator) nestValidator(f *file, n *irv1.Nest) string {
	if n.GetUnion() != nil {
		u := g.ix.unions[unionKey(n.GetUnion())]
		if u == nil || !validates(u) {
			return ""
		}
		return g.ref(f, validatorName(u.GetHostName().GetWords()), true)
	}
	s := g.ix.shapes[shapeKey(n.GetShape())]
	if s == nil || !writeShape(s) {
		return ""
	}
	return g.ref(f, validatorName(s.GetHostName().GetWords()), true)
}

// validator generates a write shape's validator, which checks every field
// it holds against its client-validated constraints and every nested shape
// or change in turn, with each issue's path from the shape (§10.5). A
// Partial checks only the fields it holds.
func (g *generator) validator(s *irv1.Shape) {
	f := g.shapeFile(s)
	typ := g.shapeType(f, s.GetId(), nil, "")
	shape := g.dotted(s.GetId())
	name := validatorName(s.GetHostName().GetWords())
	q := s.GetEntity()
	t := g.ix.tables[keyOf(q)]
	recv := receiver(s.GetHostName().GetWords())
	used := map[string]bool{recv: true}
	v, x, i := fresh("v", used), fresh("x", used), fresh("i", used)
	issue := f.rt("ValidationIssue", false)
	var body []string
	for _, fld := range s.GetFields() {
		at := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: fld.GetColumn()}
		col := column(t, fld.GetColumn())
		if col == nil {
			continue // reported with the shape
		}
		value := recv + "." + MemberName(col.GetHostName().GetWords())
		calls := g.checkCalls(f, fld.GetValidation(), fld.GetType(), t, q, at, v, shape, fld.GetColumn(), value)
		var conds []string
		if isOptional(fld) {
			conds = append(conds, value+" !== undefined")
		}
		if fld.GetNullable() {
			conds = append(conds, value+" !== null")
		}
		switch {
		case len(calls) == 0:
		case len(conds) == 0:
			body = append(body, calls...)
		default:
			body = append(body, "if ("+strings.Join(conds, " && ")+") {\n  "+strings.Join(calls, "\n  ")+"\n}")
		}
	}
	for _, n := range s.GetNests() {
		validate := g.nestValidator(f, n)
		r := relation(t, n.GetRelation())
		if validate == "" || r == nil {
			continue
		}
		field := recv + "." + MemberName(r.GetName().GetWords())
		push := func(e, path string) string {
			call := v + ".push(..." + f.rt("issuesAt", true) + "(" + validate + "(" + e + "), " + path + "));"
			if n.GetNullable() {
				return "if (" + e + " !== null) " + call
			}
			return call
		}
		var stmt string
		if toMany(r) {
			stmt = "for (const [" + i + ", " + x + "] of " + field + ".entries()) {\n  " + push(x, jsString(n.GetRelation()+"[")+" + "+i+" + \"]\"") + "\n}"
		} else {
			stmt = push(field, jsString(n.GetRelation()))
		}
		if n.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL {
			stmt = "if (" + field + " !== undefined) {\n" + indent(stmt, "  ") + "\n}"
		}
		body = append(body, stmt)
	}
	f.printf("/**\n * %s checks %s against the client-validated constraints\n * of its fields and of what it nests (§10.5), and returns every issue with\n * its path. Absent fields are not checked.\n */\n", name, recv)
	f.printf("export function %s(%s: %s): %s[] {\n  const %s: %s[] = [];\n", name, recv, typ, issue, v, issue)
	for _, b := range body {
		f.printf("%s\n", indent(b, "  "))
	}
	f.printf("  return %s;\n}\n\n", v)
}

// unionValidator generates a union's validator: that of the Draft it
// creates or the Partial it updates, and no issues for a link, delete or
// unlink (§10.5).
func (g *generator) unionValidator(u *irv1.RelationUnion) {
	f := g.entityFile(u.GetId().GetEntity())
	typ := TypeName(u.GetHostName().GetWords())
	name := validatorName(u.GetHostName().GetWords())
	r := receiver(u.GetHostName().GetWords())
	issue := f.rt("ValidationIssue", false)
	f.printf("/** %s checks the Draft %s creates or the Partial it updates (§10.5). */\n", name, r)
	f.printf("export function %s(%s: %s): %s[] {\n", name, r, typ, issue)
	for _, v := range u.GetVariants() {
		p := g.ix.shapes[shapeKey(v.GetPayload())]
		if p == nil {
			continue // reported with the union
		}
		payload := "draft"
		if v.GetChange() == irv1.RelationChange_RELATION_CHANGE_UPDATE {
			payload = "partial"
		}
		f.printf("  if (%s.kind === %s) return %s(%s.%s);\n", r, jsString(changeKinds[v.GetChange()]), g.ref(f, validatorName(p.GetHostName().GetWords()), true), r, payload)
	}
	f.printf("  return [];\n}\n\n")
}
