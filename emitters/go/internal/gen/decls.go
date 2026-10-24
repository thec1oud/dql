package gen

import (
	"fmt"
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
// (§8.4, §8.5, §15.7) into dql-types.dql.go. The fixed files are named
// with a hyphen, which no entity file has.
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

// enum generates a string type, a constant per label in declared order named
// from the label's words, Valid and the Values function (§8.4).
func (g *generator) enum(f *file, q *irv1.QualifiedName, e *irv1.Enum) {
	name := Exported(e.GetHostName().GetWords())
	g.declare(name, "enum "+displayName(q), q)
	values := name + "Values"
	g.declare(values, "the values function of enum "+displayName(q), q)
	var consts []string
	for _, l := range e.GetLabelNames() {
		c := name + Exported(l.GetWords())
		lq := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: l.GetStored()}
		g.declare(c, fmt.Sprintf("label %q of enum %s", l.GetStored(), displayName(q)), lq)
		consts = append(consts, c)
	}
	r := receiver(e.GetHostName().GetWords())
	f.printf("// %s is the enum %s.\ntype %s string\n\n", name, displayName(q), name)
	if len(consts) > 0 {
		f.printf("// The labels of %s, in declared order.\nconst (\n", name)
		for i, l := range e.GetLabelNames() {
			f.printf("\t%s %s = %s\n", consts[i], name, strconv.Quote(l.GetStored()))
		}
		f.printf(")\n\n")
	}
	f.printf("// Valid reports whether %s is one of the enum's labels.\nfunc (%s %s) Valid() bool {\n", r, r, name)
	if len(consts) > 0 {
		f.printf("\tswitch %s {\n\tcase %s:\n\t\treturn true\n\t}\n", r, strings.Join(consts, ", "))
	}
	f.printf("\treturn false\n}\n\n")
	f.printf("// %s returns the enum's labels in declared order.\nfunc %s() []%s {\n\treturn []%s{%s}\n}\n\n",
		values, values, name, name, strings.Join(consts, ", "))
}

// domain generates a distinct type over the domain's base type (§8.5).
func (g *generator) domain(f *file, q *irv1.QualifiedName, d *irv1.Domain) {
	name := Exported(d.GetHostName().GetWords())
	g.declare(name, "domain "+displayName(q), q)
	base := g.goType(f, d.GetBase(), q, "domain "+displayName(q))
	f.printf("// %s is the domain %s.\ntype %s %s\n\n", name, displayName(q), name, base)
}

// composite generates a struct of the attributes in their order (§8.5).
// Attributes are nullable unless the core narrowed them (nonnull).
func (g *generator) composite(f *file, q *irv1.QualifiedName, c *irv1.Composite) {
	name := Exported(c.GetHostName().GetWords())
	g.declare(name, "composite type "+displayName(q), q)
	f.printf("// %s is the composite type %s.\ntype %s struct {\n", name, displayName(q), name)
	fields := map[string]string{}
	for _, a := range c.GetAttributes() {
		aq := &irv1.QualifiedName{Catalog: q.GetCatalog(), Namespace: q.GetNamespace(), Object: q.GetObject(), Member: a.GetName().GetStored()}
		fn := Exported(a.GetHostName().GetWords())
		if prev, ok := fields[fn]; ok {
			g.errorf(CodeNameCollision, aq, "attributes %s and %s of composite type %s both get the Go name %s (§4.3, §15.2); give one of them a name annotation",
				prev, a.GetName().GetStored(), displayName(q), fn)
		}
		fields[fn] = a.GetName().GetStored()
		f.printf("\t%s %s\n", fn, g.field(f, a.GetType(), a.GetNullable(), aq, "attribute "+displayName(aq)))
	}
	f.printf("}\n\n")
}
