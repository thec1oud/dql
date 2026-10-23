package catalog

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/annotate"
	"github.com/thec1oud/dql/core/diagnostics"
	"github.com/thec1oud/dql/core/facts"
	"github.com/thec1oud/dql/core/types"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Core keys this package applies (§7.2).
const (
	keyName                 = "core.name"
	keyNonNull              = "core.nonnull"
	keyArrayElementsNonNull = "core.array-elements-nonnull"
)

// site is one object annotations can be on, with the facts its checks need.
type site struct {
	// keys are the config keys that name the site (§1.3).
	keys   []string
	target annotate.Target
	anns   *[]*irv1.Annotation
	// col is set for a table, view or composite column and a result column.
	col *irv1.Column
	// table is set for a table or view, whose target lists its columns.
	table *irv1.Table
	// ret is set for an overload with a scalar or set-of result.
	ret *irv1.ReturnValue
}

// sites lists every object of cats that annotations can be on (§7.2), with
// its target facts as the source read them, before any annotation narrows
// them.
func sites(b *irv1.Bundle) []*site {
	ix := types.NewIndex(b)
	var out []*site
	add := func(s *site) { out = append(out, s) }
	for _, c := range b.GetCatalogs() {
		for _, ns := range c.GetNamespaces() {
			at := func(object, member string) *irv1.QualifiedName {
				return &irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: object, Member: member}
			}
			column := func(kind annotate.Kind, q *irv1.QualifiedName, col *irv1.Column) *site {
				return &site{keys: []string{diagnostics.Name(q)}, col: col, anns: &col.Annotations, target: annotate.Target{
					Kind: kind, Name: q, Words: col.GetName().GetWords(),
					// A copy, so the checks see the type as the source read it,
					// before array-elements-nonnull narrows it.
					Type:      proto.CloneOf(ix.Underlying(col.GetType())),
					NotNull:   !col.GetNullable(),
					Generated: col.GetGeneration().GetKind() != irv1.GenerationKind_GENERATION_UNSPECIFIED,
					OnUpdate:  col.GetDefault().GetOnUpdate(),
				}}
			}
			object := func(kind annotate.Kind, name *irv1.Identifier, anns *[]*irv1.Annotation) *site {
				q := at(name.GetStored(), "")
				return &site{keys: []string{diagnostics.Name(q)}, anns: anns, target: annotate.Target{Kind: kind, Name: q, Words: name.GetWords()}}
			}
			nsName := at("", "")
			add(&site{keys: []string{diagnostics.Name(nsName)}, anns: &ns.Annotations,
				target: annotate.Target{Kind: annotate.KindNamespace, Name: nsName, Words: ns.GetName().GetWords()}})

			for _, t := range ns.GetTables() {
				name := t.GetName().GetStored()
				tableKind, colKind := annotate.KindTable, annotate.KindColumn
				if t.GetKind() != irv1.TableKind_RELATION_KIND_TABLE {
					tableKind, colKind = annotate.KindView, annotate.KindViewColumn
				}
				s := object(tableKind, t.GetName(), &t.Annotations)
				s.table = t
				s.target.PrimaryKey = slices.ContainsFunc(t.GetUniqueness(), func(u *irv1.Uniqueness) bool {
					return u.GetKind() == irv1.UniquenessKind_UNIQUENESS_PRIMARY_KEY
				})
				add(s)
				for _, col := range t.GetColumns() {
					add(column(colKind, at(name, col.GetName().GetStored()), col))
				}
				for _, con := range t.GetConstraints() {
					add(constraint(at(name, con.GetName()), con))
				}
				for _, fk := range t.GetForeignKeys() {
					q := at(name, fk.GetName().GetStored())
					notNull := slices.ContainsFunc(fk.GetColumns(), func(n string) bool {
						i := slices.IndexFunc(t.GetColumns(), func(c *irv1.Column) bool { return c.GetName().GetStored() == n })
						return i >= 0 && !t.GetColumns()[i].GetNullable()
					})
					add(&site{keys: []string{diagnostics.Name(q)}, anns: &fk.Annotations, target: annotate.Target{
						Kind: annotate.KindForeignKey, Name: q, Words: fk.GetName().GetWords(), NotNull: notNull,
					}})
				}
			}
			for _, e := range ns.GetEnums() {
				add(object(annotate.KindEnum, e.GetName(), &e.Annotations))
			}
			for _, d := range ns.GetDomains() {
				add(object(annotate.KindDomain, d.GetName(), &d.Annotations))
				for _, con := range d.GetConstraints() {
					add(constraint(at(d.GetName().GetStored(), con.GetName()), con))
				}
			}
			for _, co := range ns.GetComposites() {
				add(object(annotate.KindComposite, co.GetName(), &co.Annotations))
				for _, attr := range co.GetAttributes() {
					add(column(annotate.KindAttribute, at(co.GetName().GetStored(), attr.GetName().GetStored()), attr))
				}
			}
			for _, sq := range ns.GetSequences() {
				add(object(annotate.KindSequence, sq.GetName(), &sq.Annotations))
			}
			for _, r := range ns.GetRoutines() {
				q := at(r.GetName().GetStored(), "")
				for _, o := range r.GetOverloads() {
					kind := annotate.KindFunction
					if o.GetKind() == irv1.RoutineKind_ROUTINE_KIND_PROCEDURE {
						kind = annotate.KindProcedure
					}
					// §4.6: an overload is its routine's name plus its argument
					// types; the name alone is enough when it is unambiguous.
					keys := []string{
						fmt.Sprintf("%s(%s)", diagnostics.Name(q), strings.Join(facts.Signature(o), ", ")),
						diagnostics.Name(q),
					}
					s := &site{keys: keys, anns: &o.Annotations, target: annotate.Target{Kind: kind, Name: q, Words: r.GetName().GetWords()}}
					// nonnull on an overload narrows its one result value (§7.2).
					if ret := cmp.Or(o.GetReturns().GetScalar(), o.GetReturns().GetSetOf()); ret != nil {
						s.ret = ret
						s.target.Type = proto.CloneOf(ix.Underlying(ret.GetType()))
						s.target.NotNull = !ret.GetNullable()
					}
					add(s)
					for _, col := range o.GetReturns().GetTable().GetColumns() {
						// Result columns are reached through their overload's
						// comments; config keys do not name them.
						s := column(annotate.KindResult, at(r.GetName().GetStored(), col.GetName().GetStored()), col)
						s.keys = nil
						add(s)
					}
				}
			}
		}
	}
	return out
}

func constraint(q *irv1.QualifiedName, con *irv1.Constraint) *site {
	kind := annotate.KindCheck
	if con.GetKind() == irv1.ConstraintKind_CONSTRAINT_KIND_EXCLUSION {
		kind = annotate.KindExclusion
	}
	return &site{keys: []string{diagnostics.Name(q)}, anns: &con.Annotations, target: annotate.Target{
		Kind: kind, Name: q,
		// §8.4: enum needs a single-column membership test; a domain's
		// constraint tests its one value.
		Membership: con.GetPortable().GetMembership() != nil && len(con.GetColumns()) <= 1,
	}}
}

// annotate attaches config annotations to their targets, merges every
// target's annotations (§7.1), applies the narrowing annotations and checks
// every target (§7.3).
func (a *assembler) annotate(b *irv1.Bundle, config map[string][]*irv1.Annotation) {
	all := sites(b)
	byKey := map[string][]*site{}
	for _, s := range all {
		for _, k := range s.keys {
			byKey[k] = append(byKey[k], s)
		}
	}
	var keys []string
	for k := range config {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		matched := byKey[k]
		switch len(matched) {
		case 0:
			a.errorf(CodeAnnotationTargetUnknown, keyTarget(k),
				"config annotations are keyed %s, which names no object of the bundle; write the key as the qualified name of a table, view, column, constraint, foreign key, type, sequence or overload, such as shop.app.users.password_hash (§1.3)", k)
		case 1:
			*matched[0].anns = append(*matched[0].anns, config[k]...)
		default:
			var what []string
			for _, s := range matched {
				what = append(what, s.target.Kind.String()+" "+s.keys[0])
			}
			slices.Sort(what)
			a.errorf(CodeAnnotationTargetAmbiguous, keyTarget(k),
				"config annotations are keyed %s, which names %s; key them by one object, and an overload by its signature (§4.6)", k, joinList(what))
		}
	}

	for _, s := range all {
		for _, ann := range *s.anns {
			splitWordValues(ann)
		}
		merged, ds := annotate.Merge(s.target.Name, *s.anns)
		*s.anns = merged
		a.add(ds...)
	}
	for _, s := range all {
		narrow(s)
	}
	for _, s := range all {
		for _, col := range s.table.GetColumns() {
			s.target.Columns = append(s.target.Columns, annotate.Column{Name: col.GetName().GetStored(), Nullable: col.GetNullable()})
		}
		a.add(annotate.Check(s.target, *s.anns)...)
	}
}

// narrow applies the annotations that narrow a column's facts (§13).
// Decoding enforces each one. logical-key is applied by core/facts, which
// fills Table.logical_key with the annotation's origin (§6.4, §11.1).
func narrow(s *site) {
	if s.ret != nil && find(*s.anns, keyNonNull) {
		s.ret.Nullable = false
	}
	if s.col == nil {
		return
	}
	// nonnull narrows a column, view column, composite attribute or result
	// column to non-null (§6.4, §8.5); a column the database already
	// guarantees keeps its nullability source, and annotate.Check warns.
	if find(*s.anns, keyNonNull) && s.col.GetNullable() {
		s.col.Nullable = false
		s.col.NullabilitySource = irv1.NullabilitySource_NULLABILITY_SOURCE_ANNOTATION
	}
	// array-elements-nonnull narrows the elements of an array column (§8.6).
	if arr := s.col.GetType().GetArray(); arr != nil && find(*s.anns, keyArrayElementsNonNull) {
		arr.ElementNullable = false
	}
}

func find(anns []*irv1.Annotation, key string) bool {
	return slices.ContainsFunc(anns, func(a *irv1.Annotation) bool { return a.GetKey() == key })
}

// keyTarget is the target of a diagnostic about a config key that names no
// one object: the key's dotted parts, which diagnostics.Name writes back as
// the key.
func keyTarget(k string) *irv1.QualifiedName {
	parts := strings.SplitN(k, ".", 4)
	parts = append(parts, "", "", "")
	return &irv1.QualifiedName{Catalog: parts[0], Namespace: parts[1], Object: parts[2], Member: parts[3]}
}
