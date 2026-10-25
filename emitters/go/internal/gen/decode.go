package gen

import (
	"fmt"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// CodeDecodeUnsupported: a read shape the emitter cannot decode a row into:
// one that nests a write shape or a change.
const CodeDecodeUnsupported = "go-decode-unsupported"

// decoders generates Decode<Shape> for every read shape: Full, Key and the
// custom shapes derived from them (§13). Write shapes are never decoded.
func (g *generator) decoders() {
	for _, s := range g.b.GetShapes().GetShapes() {
		if !writeShape(s) {
			g.decoder(s)
		}
	}
}

// decoder generates a shape's decoder, which reads a dql.Row of its fields
// in order, then its nests, and enforces every fact they declare or assert:
// NULL in a non-null field, an unknown enum label, an array of other
// dimensions and an off-type value are each a *dql.DecodeError (§13).
func (g *generator) decoder(s *irv1.Shape) {
	f := g.shapeFile(s)
	typ := Exported(s.GetHostName().GetWords())
	name := "Decode" + typ
	shape := g.dotted(s.GetId())
	q := s.GetEntity()
	t := g.ix.tables[keyOf(q)]
	dql := f.use(pkgDQL, "dql")
	var body []string
	for i, fld := range s.GetFields() {
		at := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: fld.GetColumn()}
		conv := g.conv(f, fld.GetType(), at, "field "+fld.GetColumn()+" of shape "+shape)
		get := fmt.Sprintf("%s.Field(d, %d, %q, %q, %s)", dql, i, fld.GetColumn(), g.assertion(s, fld, column(t, fld.GetColumn())), conv)
		if fld.GetNullable() {
			get = fmt.Sprintf("%s.Nullable(d, %d, %q, %s)", dql, i, fld.GetColumn(), conv)
		}
		body = append(body, "out."+g.fieldName(s, fld.GetColumn())+" = "+g.present(f, fld.GetPresence(), get))
	}
	for j, n := range s.GetNests() {
		i := len(s.GetFields()) + j
		r := relation(t, n.GetRelation())
		at := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: n.GetRelation()}
		target := g.ix.shapes[shapeKey(n.GetShape())]
		if n.GetUnion() != nil || writeShape(target) {
			g.errorf(CodeDecodeUnsupported, at, "read shape %s nests a write shape or change through %s, which no row holds", shape, n.GetRelation())
			return
		}
		decode := "Decode" + Exported(target.GetHostName().GetWords())
		var get string
		switch {
		case toMany(r):
			get = fmt.Sprintf("%s.Many(d, %d, %q, %s)", dql, i, n.GetRelation(), decode)
		case n.GetNullable():
			get = g.present(f, n.GetPresence(), fmt.Sprintf("%s.Nullable(d, %d, %q, %s.Nest(%s))", dql, i, n.GetRelation(), dql, decode))
		default:
			get = g.present(f, n.GetPresence(), fmt.Sprintf("%s.Field(d, %d, %q, \"\", %s.Nest(%s))", dql, i, n.GetRelation(), dql, decode))
		}
		body = append(body, "out."+Exported(r.GetName().GetWords())+" = "+get)
	}
	g.declare(name, "the decoder of shape "+shape, q)
	f.printf("// %s decodes r, a row of %s. The row holds the shape's\n// fields in order, then its nests. A value that breaks what a field declares\n// or asserts is a *%s.DecodeError, never a zero value (§13).\n", name, shape, dql)
	f.printf("func %s(r %s.Row) (%s, error) {\n\td := %s.NewDecoder(%q, r, %d)\n\tvar out %s\n", name, dql, typ, dql, shape, len(s.GetFields())+len(s.GetNests()), typ)
	for _, b := range body {
		f.printf("\t%s\n", b)
	}
	f.printf("\treturn out, d.Err()\n}\n\n")
}

// present wraps a decoded value of an optional field or to-one nest in
// opt.Some: a row always holds it, so a decoded read shape has it present.
func (g *generator) present(f *file, p irv1.Presence, get string) string {
	if p == irv1.Presence_PRESENCE_OPTIONAL {
		return f.use(pkgOpt, "opt") + ".Some(" + get + ")"
	}
	return get
}

// assertion names where a field's non-null fact came from when something
// asserted it rather than the database: the shape whose nonnull operator
// narrowed it, or the nonnull annotation on its column (§13). It is empty
// for a fact the database proves.
func (g *generator) assertion(s *irv1.Shape, fld *irv1.Field, c *irv1.Column) string {
	switch fld.GetNullabilitySource() {
	case irv1.NullabilitySource_NULLABILITY_SOURCE_SHAPE_OPERATOR:
		id := fld.GetNullabilityShape()
		if id == nil {
			id = s.GetId()
		}
		return "nonnull of shape " + g.dotted(id)
	case irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION:
		e := s.GetEntity()
		return annotated(c.GetAnnotations(), "core.nonnull",
			&irv1.QualifiedName{Catalog: e.GetCatalog(), Namespace: e.GetNamespace(), Object: e.GetObject(), Member: fld.GetColumn()})
	}
	return ""
}

// annotated names the annotation with the key as the record's example does:
// core.nonnull from COMMENT ON COLUMN app.published_posts.title, or, where
// its origin records no location, core.nonnull on the column.
func annotated(annotations []*irv1.Annotation, key string, at *irv1.QualifiedName) string {
	for _, a := range annotations {
		if a.GetKey() == key && a.GetOrigin().GetLocation() != "" {
			return key + " from " + a.GetOrigin().GetLocation()
		}
	}
	return key + " on " + displayName(at)
}

// conv returns the dql converter of a non-null value of t: an expression of
// type func(any) (T, error), where T is t's Go type (§13).
func (g *generator) conv(f *file, t *irv1.Type, at *irv1.QualifiedName, what string) string {
	dql := f.use(pkgDQL, "dql")
	goType := g.goType(f, t, at, what)
	switch k := t.GetKind().(type) {
	case *irv1.Type_Bool:
		return dql + ".Bool"
	case *irv1.Type_Int8, *irv1.Type_Int16, *irv1.Type_Int32, *irv1.Type_Int64:
		return dql + ".Int[" + goType + "]"
	case *irv1.Type_Uint8, *irv1.Type_Uint16, *irv1.Type_Uint32, *irv1.Type_Uint64:
		return dql + ".Uint[" + goType + "]"
	case *irv1.Type_Float32, *irv1.Type_Float64:
		return dql + ".Float[" + goType + "]"
	case *irv1.Type_Array:
		a := k.Array
		elem := g.conv(f, a.GetElement(), at, what)
		if a.GetElementNullable() {
			elem = dql + ".NullableElem(" + elem + ")"
		} else {
			elem = dql + ".Elem(" + elem + ")"
		}
		for range max(1, int(a.GetDimensions())) {
			elem = dql + ".Array(" + elem + ")"
		}
		return elem
	case *irv1.Type_Named:
		if k.Named.GetEnum() != nil {
			return dql + ".Enum(" + goType + ".Valid)"
		}
		return g.namedDecoder(firstName(k.Named.GetDomain(), k.Named.GetComposite()))
	case *irv1.Type_Weak:
		return dql + ".Weak(" + g.conv(f, k.Weak.GetAffinity(), at, what) + ")"
	}
	switch goType {
	case "string":
		return dql + ".String"
	case "[]byte":
		return dql + ".Bytes"
	}
	return dql + ".As[" + goType + "]"
}

// firstName returns the first of two names that is set.
func firstName(a, b *irv1.QualifiedName) *irv1.QualifiedName {
	if a != nil {
		return a
	}
	return b
}

// namedDecoder returns the decoder of a domain or composite type, declaring
// it in dql-types.dql.go the first time: a domain's converts its base type,
// and a composite type's decodes a dql.Row of its attributes in order (§13).
func (g *generator) namedDecoder(q *irv1.QualifiedName) string {
	k := keyOf(q)
	if name, ok := g.namedDecoders[k]; ok {
		return name
	}
	n := g.named[k]
	typ := Exported(n.words)
	name := "dqlDecode" + typ
	g.namedDecoders[k] = name
	f := g.typesFile
	dql := f.use(pkgDQL, "dql")
	if n.composite == nil {
		f.printf("// %s decodes a value of domain %s (§13).\nfunc %s(v any) (%s, error) {\n\tx, err := %s(v)\n\treturn %s(x), err\n}\n\n",
			name, displayName(q), name, typ, g.conv(f, n.base, q, "domain "+displayName(q)), typ)
		return name
	}
	attrs := n.composite.GetAttributes()
	var body []string
	for i, a := range attrs {
		aq := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: a.GetName().GetStored()}
		conv := g.conv(f, a.GetType(), aq, "attribute "+displayName(aq))
		get := fmt.Sprintf("%s.Nullable(d, %d, %q, %s)", dql, i, a.GetName().GetStored(), conv)
		if !a.GetNullable() {
			get = fmt.Sprintf("%s.Field(d, %d, %q, %q, %s)", dql, i, a.GetName().GetStored(), annotated(a.GetAnnotations(), "core.nonnull", aq), conv)
		}
		body = append(body, "out."+Exported(a.GetHostName().GetWords())+" = "+get)
	}
	f.printf("// %s decodes a value of composite type %s:\n// a dql.Row of its attributes in order (§13).\nfunc %s(v any) (%s, error) {\n\td := %s.NewDecoder(\"\", v, %d)\n\tvar out %s\n",
		name, displayName(q), name, typ, dql, len(attrs), typ)
	f.printf("\t%s\n", strings.Join(body, "\n\t"))
	f.printf("\treturn out, d.Err()\n}\n\n")
	return name
}
