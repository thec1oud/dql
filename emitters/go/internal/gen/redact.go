package gen

import (
	"fmt"
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// redaction generates LogValue, String and GoString for every shape and
// union that holds a sensitive field, itself or through what it nests, so
// slog and fmt print the field as [REDACTED] (§7.5, implementation guide
// §7 rule 12). A type that holds none prints as Go prints it.
func (g *generator) redaction() {
	shapes := g.b.GetShapes().GetShapes()
	unions := g.b.GetShapes().GetUnions()
	sensitive := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, s := range shapes {
			if !sensitive[shapeKey(s.GetId())] && g.holdsSensitive(s, sensitive) {
				sensitive[shapeKey(s.GetId())], changed = true, true
			}
		}
		for _, u := range unions {
			if k := "union " + unionKey(u.GetId()); !sensitive[k] && slices.ContainsFunc(u.GetVariants(), func(v *irv1.RelationVariant) bool {
				return v.GetKey() != nil && sensitive[shapeKey(v.GetKey())] || v.GetPayload() != nil && sensitive[shapeKey(v.GetPayload())]
			}) {
				sensitive[k], changed = true, true
			}
		}
	}
	for _, s := range shapes {
		if sensitive[shapeKey(s.GetId())] {
			g.redactShape(s)
		}
	}
	for _, u := range unions {
		if sensitive["union "+unionKey(u.GetId())] {
			g.redactUnion(u)
		}
	}
}

// holdsSensitive reports whether s has a sensitive field, or nests a shape or
// union known to hold one.
func (g *generator) holdsSensitive(s *irv1.Shape, sensitive map[string]bool) bool {
	return slices.ContainsFunc(s.GetFields(), (*irv1.Field).GetRedact) || slices.ContainsFunc(s.GetNests(), func(n *irv1.Nest) bool {
		if n.GetUnion() != nil {
			return sensitive["union "+unionKey(n.GetUnion())]
		}
		return sensitive[shapeKey(n.GetShape())]
	})
}

// redactShape generates a shape's printing methods: each field and nest by
// its Go name, a sensitive field as [REDACTED].
func (g *generator) redactShape(s *irv1.Shape) {
	f := g.shapeFile(s)
	typ := Exported(s.GetHostName().GetWords())
	shape := g.dotted(s.GetId())
	for _, m := range []string{"LogValue", "String", "GoString"} {
		if !g.method(s, m, "the redacting "+m+" of "+shape) {
			return
		}
	}
	recv := receiver(s.GetHostName().GetWords())
	slog := f.use("log/slog", "slog")
	t := g.ix.tables[keyOf(s.GetEntity())]
	var attrs []string
	for _, fld := range s.GetFields() {
		name := g.fieldName(s, fld.GetColumn())
		if fld.GetRedact() {
			attrs = append(attrs, fmt.Sprintf("%s.String(%q, %q)", slog, name, "[REDACTED]"))
		} else {
			attrs = append(attrs, fmt.Sprintf("%s.Any(%q, %s.%s)", slog, name, recv, name))
		}
	}
	for _, n := range s.GetNests() {
		name := Exported(relation(t, n.GetRelation()).GetName().GetWords())
		attrs = append(attrs, fmt.Sprintf("%s.Any(%q, %s.%s)", slog, name, recv, name))
	}
	g.printers(f, recv, typ, slog, attrs)
}

// redactUnion generates a union's printing methods: its change and the key
// and payload it holds.
func (g *generator) redactUnion(u *irv1.RelationUnion) {
	f := g.entityFile(u.GetId().GetEntity())
	typ := Exported(u.GetHostName().GetWords())
	recv := receiver(u.GetHostName().GetWords())
	slog := f.use("log/slog", "slog")
	attrs := []string{fmt.Sprintf("%s.Any(\"Change\", %s.change)", slog, recv)}
	var key, draft, partial bool
	for _, v := range u.GetVariants() {
		key = key || v.GetKey() != nil
		draft = draft || v.GetChange() == irv1.RelationChange_RELATION_CHANGE_CREATE
		partial = partial || v.GetChange() == irv1.RelationChange_RELATION_CHANGE_UPDATE
	}
	for _, fld := range []struct {
		held        bool
		name, field string
	}{{key, "Key", "key"}, {draft, "Draft", "draft"}, {partial, "Partial", "partial"}} {
		if fld.held {
			attrs = append(attrs, fmt.Sprintf("%s.Any(%q, %s.%s)", slog, fld.name, recv, fld.field))
		}
	}
	g.printers(f, recv, typ, slog, attrs)
}

// printers writes LogValue, String and GoString over the given attributes.
func (g *generator) printers(f *file, recv, typ, slog string, attrs []string) {
	f.printf("// LogValue logs %s with its sensitive fields as [REDACTED] (§7.5).\nfunc (%s %s) LogValue() %s.Value {\n\treturn %s.GroupValue(\n\t\t%s,\n\t)\n}\n\n",
		recv, recv, typ, slog, slog, strings.Join(attrs, ",\n\t\t"))
	f.printf("// String formats %s as LogValue does, so fmt never prints a sensitive\n// field (§7.5).\nfunc (%s %s) String() string { return %s.LogValue().String() }\n\n", recv, recv, typ, recv)
	f.printf("// GoString formats %s as String does, for %%#v.\nfunc (%s %s) GoString() string { return %s.String() }\n\n", recv, recv, typ, recv)
}
