package facts

import (
	"slices"
	"strings"

	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// overloads fills every overload's signature and host name and fails host
// names that collide (§4.6). Every routine in a namespace is public (§6.5),
// so every overload listed counts as a public overload.
func (d *deriver) overloads(catalogs []*irv1.Catalog) {
	type named struct {
		id    string // shop.app.post_score(int64)
		at    *irv1.QualifiedName
		words []string
	}
	seen := map[string][]named{}
	var keys []string
	for _, c := range catalogs {
		for _, ns := range c.GetNamespaces() {
			prefix := d.prefix(catalogs, c, ns)
			for _, rt := range ns.GetRoutines() {
				at := &irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: rt.GetName().GetStored()}
				for _, o := range rt.GetOverloads() {
					o.Signature = nil
					for _, a := range inputs(o) {
						o.Signature = append(o.Signature, d.typeName(a.GetType()))
					}
				}
				slices.SortFunc(rt.Overloads, func(a, b *irv1.Overload) int { return slices.Compare(a.GetSignature(), b.GetSignature()) })
				for _, o := range rt.GetOverloads() {
					words := d.overloadWords(at, rt, o)
					words = append(slices.Clone(prefix), words...)
					o.HostName = &irv1.Identifier{Words: words}
					k := wordsKey(words)
					if seen[k] == nil {
						keys = append(keys, k)
					}
					seen[k] = append(seen[k], named{
						id: diagnostics.Name(at) + "(" + strings.Join(o.GetSignature(), ", ") + ")", at: at, words: words,
					})
				}
			}
		}
	}
	for _, k := range keys {
		group := seen[k]
		if len(group) < 2 {
			continue
		}
		var ids []string
		for _, n := range group {
			ids = append(ids, "`"+n.id+"`")
		}
		for _, n := range group {
			d.errorf(CodeOverloadNameCollision, n.at,
				"overload `%s` gets the host name [%s], the same as %s; give it a distinct name annotation",
				n.id, strings.Join(n.words, " "), strings.Join(slices.DeleteFunc(slices.Clone(ids), func(s string) bool { return s == "`"+n.id+"`" }), ", "))
		}
	}
}

// overloadWords is an overload's name before prefixes: its name annotation,
// else the routine's words, followed by its arguments' logical-type words
// when the routine has several public overloads (§4.6).
func (d *deriver) overloadWords(at *irv1.QualifiedName, rt *irv1.Routine, o *irv1.Overload) []string {
	if a := find(o.GetAnnotations(), keyName); a != nil {
		if w, ok := valueWords(a.GetValue()); ok {
			return w
		}
		d.errorf(CodeAnnotationValueInvalid, at,
			"name on overload `%s(%s)` needs a word list, such as name: [post, score, recent]",
			diagnostics.Name(at), strings.Join(o.GetSignature(), ", "))
	}
	words := slices.Clone(rt.GetName().GetWords())
	if len(rt.GetOverloads()) > 1 {
		for _, a := range inputs(o) {
			words = append(words, d.typeWords(a.GetType())...)
		}
	}
	return words
}

// inputs are the arguments that identify an overload: every one but OUT.
func inputs(o *irv1.Overload) []*irv1.Argument {
	var in []*irv1.Argument
	for _, a := range o.GetArguments() {
		if a.GetMode() != irv1.ArgMode_ARG_MODE_OUT {
			in = append(in, a)
		}
	}
	return in
}

// prefix is the catalog and namespace prefix of §4.3.
func (d *deriver) prefix(catalogs []*irv1.Catalog, c *irv1.Catalog, ns *irv1.Namespace) []string {
	var p []string
	if len(catalogs) > 1 && c.GetName().GetStored() != d.opts.DefaultCatalog {
		p = append(p, c.GetName().GetWords()...)
	}
	if ns.GetName().GetStored() != c.GetDefaultNamespace() {
		p = append(p, ns.GetName().GetWords()...)
	}
	return p
}

// typeName is a logical type's name in a signature (§8.1): int64,
// local_time, array<int64>, a named type's qualified name, or opaque<native>.
func (d *deriver) typeName(t *irv1.Type) string {
	switch k := t.GetKind().(type) {
	case *irv1.Type_Array:
		return "array<" + d.typeName(k.Array.GetElement()) + ">"
	case *irv1.Type_Range:
		return "range<" + d.typeName(k.Range.GetElement()) + ">"
	case *irv1.Type_Multirange:
		return "multirange<" + d.typeName(k.Multirange.GetElement()) + ">"
	case *irv1.Type_Weak:
		return "weak<" + d.typeName(k.Weak.GetAffinity()) + ">"
	case *irv1.Type_Named:
		return diagnostics.Name(namedRef(k.Named))
	case *irv1.Type_Opaque:
		return "opaque<" + t.GetNative().GetRaw() + ">"
	}
	return kindName(t)
}

// typeWords are a logical type's words, the same in every dialect (§4.6):
// int64, local time, array int64, a named type's own words.
func (d *deriver) typeWords(t *irv1.Type) []string {
	switch k := t.GetKind().(type) {
	case *irv1.Type_Array:
		return append([]string{"array"}, d.typeWords(k.Array.GetElement())...)
	case *irv1.Type_Range:
		return append([]string{"range"}, d.typeWords(k.Range.GetElement())...)
	case *irv1.Type_Multirange:
		return append([]string{"multirange"}, d.typeWords(k.Multirange.GetElement())...)
	case *irv1.Type_Weak:
		return append([]string{"weak"}, d.typeWords(k.Weak.GetAffinity())...)
	case *irv1.Type_Named:
		if w := d.named[qnKey(namedRef(k.Named))]; len(w) > 0 {
			return w
		}
	}
	return strings.Split(kindName(t), "_")
}

func namedRef(n *irv1.NamedRef) *irv1.QualifiedName {
	return cmpOr(n.GetEnum(), n.GetDomain(), n.GetComposite())
}

func cmpOr(qns ...*irv1.QualifiedName) *irv1.QualifiedName {
	for _, q := range qns {
		if q != nil {
			return q
		}
	}
	return &irv1.QualifiedName{}
}

// kindName is the name of the type's logical kind as the IR spells it.
func kindName(t *irv1.Type) string {
	m := t.ProtoReflect()
	if f := m.WhichOneof(m.Descriptor().Oneofs().ByName("kind")); f != nil {
		return string(f.Name())
	}
	return "unspecified"
}
