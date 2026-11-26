package gen

import (
	"slices"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// redacted is what a sensitive value prints as (§7.5).
const redacted = `"[REDACTED]"`

// redaction generates redact<Shape> for every shape and union that holds a
// sensitive field, itself or through what it nests, as the record's
// redactUserDraft (§7.5): logging hooks print what it returns, so they never
// print a sensitive value. A type that holds none gets no function.
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
	// Declared first, as a shape's function calls those of its nests.
	for _, s := range shapes {
		if sensitive[shapeKey(s.GetId())] {
			g.declare(g.shapeFile(s), "redact"+TypeName(s.GetHostName().GetWords()), "the redaction of shape "+g.dotted(s.GetId()), s.GetEntity())
		}
	}
	for _, u := range unions {
		if sensitive["union "+unionKey(u.GetId())] {
			g.declare(g.entityFile(u.GetId().GetEntity()), "redact"+TypeName(u.GetHostName().GetWords()),
				"the redaction of relation union "+strings.Join(u.GetHostName().GetWords(), " "), u.GetId().GetEntity())
		}
	}
	for _, s := range shapes {
		if sensitive[shapeKey(s.GetId())] {
			g.redactShape(s, sensitive)
		}
	}
	for _, u := range unions {
		if sensitive["union "+unionKey(u.GetId())] {
			g.redactUnion(u, sensitive)
		}
	}
}

// holdsSensitive reports whether s has a sensitive field, or nests a shape or
// union known to hold one.
func (g *generator) holdsSensitive(s *irv1.Shape, sensitive map[string]bool) bool {
	return slices.ContainsFunc(s.GetFields(), (*irv1.Field).GetRedact) || slices.ContainsFunc(s.GetNests(), func(n *irv1.Nest) bool {
		return sensitive[nestKey(n)]
	})
}

// nestKey is the sensitivity key of what a nest holds.
func nestKey(n *irv1.Nest) string {
	if n.GetUnion() != nil {
		return "union " + unionKey(n.GetUnion())
	}
	return shapeKey(n.GetShape())
}

// redactorOf is the redaction function of what a nest holds.
func (g *generator) redactorOf(f *file, n *irv1.Nest) string {
	if n.GetUnion() != nil {
		return g.ref(f, "redact"+TypeName(g.ix.unions[unionKey(n.GetUnion())].GetHostName().GetWords()), true)
	}
	return g.ref(f, "redact"+TypeName(g.ix.shapes[shapeKey(n.GetShape())].GetHostName().GetWords()), true)
}

// redactShape generates a shape's redaction: a copy with each sensitive
// field it holds as "[REDACTED]", and each nest that holds one redacted in
// turn.
func (g *generator) redactShape(s *irv1.Shape, sensitive map[string]bool) {
	f := g.shapeFile(s)
	typ := g.shapeType(f, s.GetId(), nil, "")
	name := "redact" + TypeName(s.GetHostName().GetWords())
	recv := receiver(s.GetHostName().GetWords())
	t := g.ix.tables[keyOf(s.GetEntity())]
	members := []string{"..." + recv}
	set := func(member, value string, optional bool) {
		if optional {
			members = append(members, "...("+recv+"."+member+" === undefined ? {} : { "+member+": "+value+" })")
		} else {
			members = append(members, member+": "+value)
		}
	}
	for _, fld := range s.GetFields() {
		if fld.GetRedact() {
			set(g.memberOf(s, fld.GetColumn()), redacted, isOptional(fld))
		}
	}
	for _, n := range s.GetNests() {
		if !sensitive[nestKey(n)] {
			continue
		}
		member := MemberName(relation(t, n.GetRelation()).GetName().GetWords())
		redact := g.redactorOf(f, n)
		value := recv + "." + member
		switch {
		case toMany(relation(t, n.GetRelation())) && n.GetNullable():
			value += ".map((x) => (x === null ? null : " + redact + "(x)))"
		case toMany(relation(t, n.GetRelation())):
			value += ".map((x) => " + redact + "(x))"
		case n.GetNullable():
			value = value + " === null ? null : " + redact + "(" + value + ")"
		default:
			value = redact + "(" + value + ")"
		}
		set(member, value, n.GetPresence() == irv1.Presence_PRESENCE_OPTIONAL)
	}
	f.printf("/**\n * %s returns %s with its sensitive fields as\n * \"[REDACTED]\", for logging hooks to print (§7.5).\n */\n", name, recv)
	f.printf("export function %s(%s: %s): Record<string, unknown> {\n  return %s;\n}\n\n", name, recv, typ, objectLiteral(members, "  "))
}

// redactUnion generates a union's redaction: the change with the key and
// payload it holds redacted where they hold a sensitive field.
func (g *generator) redactUnion(u *irv1.RelationUnion, sensitive map[string]bool) {
	f := g.entityFile(u.GetId().GetEntity())
	typ := TypeName(u.GetHostName().GetWords())
	name := "redact" + typ
	r := receiver(u.GetHostName().GetWords())
	f.printf("/**\n * %s returns %s with the key and payload it holds\n * redacted, for logging hooks to print (§7.5).\n */\n", name, r)
	f.printf("export function %s(%s: %s): Record<string, unknown> {\n", name, r, typ)
	for _, v := range u.GetVariants() {
		members := []string{"..." + r}
		if v.GetKey() != nil && sensitive[shapeKey(v.GetKey())] {
			members = append(members, "key: "+g.ref(f, "redact"+TypeName(g.ix.shapes[shapeKey(v.GetKey())].GetHostName().GetWords()), true)+"("+r+".key)")
		}
		if v.GetPayload() != nil && sensitive[shapeKey(v.GetPayload())] {
			payload := "draft"
			if v.GetChange() == irv1.RelationChange_RELATION_CHANGE_UPDATE {
				payload = "partial"
			}
			members = append(members, payload+": "+g.ref(f, "redact"+TypeName(g.ix.shapes[shapeKey(v.GetPayload())].GetHostName().GetWords()), true)+"("+r+"."+payload+")")
		}
		if len(members) > 1 {
			f.printf("  if (%s.kind === %s) return { %s };\n", r, jsString(changeKinds[v.GetChange()]), strings.Join(members, ", "))
		}
	}
	f.printf("  return { ...%s };\n}\n\n", r)
}
