package gen

import (
	"strconv"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// displayName spells a qualified name for comments and messages, as
// catalog.namespace.object.member with empty parts left out.
func displayName(q *irv1.QualifiedName) string {
	var parts []string
	for _, p := range []string{q.GetCatalog(), q.GetNamespace(), q.GetObject(), q.GetMember()} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ".")
}

// types generates the enums, domains and composite types of every catalog
// (§8.4, §8.5, §15.7) into dql-types.dql.ts. The fixed files are named with
// a hyphen, which no entity file has.
func (g *generator) types() {
	f := g.newFile("dql-types")
	g.eachNamespace(func(q func(string) *irv1.QualifiedName, ns *irv1.Namespace) {
		for _, e := range ns.GetEnums() {
			g.enum(f, q(e.GetName().GetStored()), e)
		}
	})
	g.eachNamespace(func(q func(string) *irv1.QualifiedName, ns *irv1.Namespace) {
		for _, d := range ns.GetDomains() {
			g.domain(f, q(d.GetName().GetStored()), d)
		}
	})
	g.eachNamespace(func(q func(string) *irv1.QualifiedName, ns *irv1.Namespace) {
		for _, c := range ns.GetComposites() {
			g.composite(f, q(c.GetName().GetStored()), c)
		}
	})
}

// enum generates the labels in declared order as a constant array, and the
// union of its elements as a type of the same name (§8.4):
//
//	export const PostStatus = ["draft", "published", "archived"] as const;
//	export type PostStatus = (typeof PostStatus)[number];
func (g *generator) enum(f *file, q *irv1.QualifiedName, e *irv1.Enum) {
	name := TypeName(e.GetHostName().GetWords())
	g.declare(f, name, "enum "+displayName(q), q)
	labels := make([]string, len(e.GetLabelNames()))
	for i, l := range e.GetLabelNames() {
		labels[i] = strconv.Quote(l.GetStored())
	}
	f.printf("/** %s is the enum %s. Its labels are in declared order. */\n", name, displayName(q))
	f.printf("export const %s = [%s] as const;\n", name, strings.Join(labels, ", "))
	f.printf("export type %s = (typeof %s)[number];\n\n", name, name)
}

// domain generates a branded type over the domain's base type, so a value of
// the base type is never one of the domain by accident (§8.5):
//
//	export type EmailAddress = string & { readonly __domain: "shop.app.email_address" };
func (g *generator) domain(f *file, q *irv1.QualifiedName, d *irv1.Domain) {
	name := TypeName(d.GetHostName().GetWords())
	g.declare(f, name, "domain "+displayName(q), q)
	base := g.tsType(f, d.GetBase(), q, "domain "+displayName(q))
	base = parenthesize(base)
	f.printf("/** %s is the domain %s. */\n", name, displayName(q))
	f.printf("export type %s = %s & { readonly __domain: %s };\n\n", name, base, strconv.Quote(displayName(q)))
	g.domainParse(f, q, d)
}

// composite generates an interface of the attributes in their order (§8.5).
// Attributes are nullable unless the core narrowed them (nonnull).
func (g *generator) composite(f *file, q *irv1.QualifiedName, c *irv1.Composite) {
	name := TypeName(c.GetHostName().GetWords())
	g.declare(f, name, "composite type "+displayName(q), q)
	f.printf("/** %s is the composite type %s. */\nexport interface %s {\n", name, displayName(q), name)
	fields := map[string]string{}
	for _, a := range c.GetAttributes() {
		aq := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: a.GetName().GetStored()}
		fn := MemberName(a.GetHostName().GetWords())
		if prev, ok := fields[fn]; ok {
			g.errorf(CodeNameCollision, aq, "attributes %s and %s of composite type %s both get the TypeScript name %s (§4.3, §15.2); give one of them a name annotation",
				prev, a.GetName().GetStored(), displayName(q), fn)
		}
		fields[fn] = a.GetName().GetStored()
		f.printf("  %s: %s;\n", fn, g.field(f, a.GetType(), a.GetNullable(), aq, "attribute "+displayName(aq)))
	}
	f.printf("}\n\n")
}
