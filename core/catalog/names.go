package catalog

import (
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/naming"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// hostNames sets host_name on every table, view, enum, domain, composite
// type, sequence and column, and fails host names that collide (§4.3).
//
// An object's host name is its name annotation's words, else its own words
// (§4.4), behind the prefixes of §4.3: its catalog's words when the bundle
// has more than one catalog and the object is outside default-catalog, then
// its namespace's words when it is outside its catalog's default namespace.
// A column's host name is its name annotation's words, else its own; columns
// take no prefix. Overload host names are core/facts' (§4.6).
//
// Tables, views, enums, domains and composite types become host types in
// the one generated package, so their host names must differ across every
// catalog (§4.3, §5.2). The columns of a table or view must differ too, which
// core/facts checks together with relation names; the attributes of a
// composite type and the result columns of an overload are the fields of one
// host struct, so they are checked here.
func (a *assembler) hostNames(cats []*irv1.Catalog) {
	var hosts []naming.Item
	at := map[string]*irv1.QualifiedName{}
	for _, c := range cats {
		for _, ns := range c.GetNamespaces() {
			p := naming.Placement{
				CatalogWords:     c.GetName().GetWords(),
				NamespaceWords:   ns.GetName().GetWords(),
				MultiCatalog:     len(cats) > 1,
				DefaultCatalog:   c.GetName().GetStored() == a.opts.DefaultCatalog,
				DefaultNamespace: ns.GetName().GetStored() == c.GetDefaultNamespace(),
			}
			hostType := func(name *irv1.Identifier, anns []*irv1.Annotation) *irv1.Identifier {
				q := &irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: name.GetStored()}
				words := naming.EntityWords(nameWords(name, anns), p)
				hosts = append(hosts, naming.Item{Stored: diagnostics.Name(q), Words: words})
				at[diagnostics.Name(q)] = q
				return &irv1.Identifier{Words: words}
			}
			for _, t := range ns.GetTables() {
				t.HostName = hostType(t.GetName(), t.GetAnnotations())
				fieldNames(t.GetColumns())
			}
			for _, e := range ns.GetEnums() {
				e.HostName = hostType(e.GetName(), e.GetAnnotations())
			}
			for _, d := range ns.GetDomains() {
				d.HostName = hostType(d.GetName(), d.GetAnnotations())
			}
			for _, co := range ns.GetComposites() {
				co.HostName = hostType(co.GetName(), co.GetAnnotations())
				a.fields(&irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: co.GetName().GetStored()},
					"attributes", "composite type", "rename all but one with a name annotation", co.GetAttributes())
			}
			for _, s := range ns.GetSequences() {
				// A sequence is reached through functions, not a host type of
				// its own, so its host name takes part in no collision check.
				s.HostName = &irv1.Identifier{Words: naming.EntityWords(nameWords(s.GetName(), s.GetAnnotations()), p)}
			}
			for _, r := range ns.GetRoutines() {
				q := &irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: r.GetName().GetStored()}
				for _, o := range r.GetOverloads() {
					// A result column takes no name annotation (§7.2: a function
					// result is not a named object), so the fix is in the database.
					a.fields(q, "result columns", "an overload of", "rename all but one in the function's definition", o.GetReturns().GetTable().GetColumns())
				}
			}
		}
	}
	for _, col := range naming.Collisions(hosts) {
		for _, name := range col.Stored {
			var others []string
			for _, o := range col.Stored {
				if o != name {
					others = append(others, o)
				}
			}
			a.errorf(CodeHostNameCollision, at[name],
				"%s gets the host name [%s], the same as %s, and every catalog generates into one package (§4.3, §5.2); give one of them a name annotation",
				name, strings.Join(col.Words, " "), joinList(others))
		}
	}
}

// fields sets the host names of a host struct's columns and fails equal ones.
func (a *assembler) fields(owner *irv1.QualifiedName, what, of, fix string, cols []*irv1.Column) {
	fieldNames(cols)
	var items []naming.Item
	for _, col := range cols {
		items = append(items, naming.Item{Stored: col.GetName().GetStored(), Words: col.GetHostName().GetWords()})
	}
	for _, col := range naming.Collisions(items) {
		q := &irv1.QualifiedName{Catalog: owner.GetCatalog(), Namespace: owner.GetNamespace(), Object: owner.GetObject(), Member: col.Stored[0]}
		a.errorf(CodeColumnNameCollision, q,
			"%s %s of %s %s all have the words [%s] (§4.3); %s",
			what, joinList(col.Stored), of, diagnostics.Name(owner), strings.Join(col.Words, " "), fix)
	}
}

func fieldNames(cols []*irv1.Column) {
	for _, col := range cols {
		col.HostName = &irv1.Identifier{Words: nameWords(col.GetName(), col.GetAnnotations())}
	}
}

// nameWords is an object's words after its name annotation (§4.4). A name
// annotation that is not a word list is annotate.Check's to report; the
// object then keeps its own words.
func nameWords(name *irv1.Identifier, anns []*irv1.Annotation) []string {
	for _, a := range anns {
		if a.GetKey() == keyName {
			if words, ok := stringList(a.GetValue().GetWords()); ok {
				return words
			}
		}
	}
	return name.GetWords()
}

func stringList(l *structpb.ListValue) ([]string, bool) {
	var out []string
	for _, v := range l.GetValues() {
		s, ok := v.GetKind().(*structpb.Value_StringValue)
		if !ok || s.StringValue == "" {
			return nil, false
		}
		out = append(out, s.StringValue)
	}
	return out, len(out) > 0
}
