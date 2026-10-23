package fidelity

import (
	"bytes"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"

	dpgextv1 "github.com/thec1oud/dql/proto/dql/ext/dpg/v1"
	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Normalize returns a copy of c with the differences that limits explain
// removed. Whatever the limits, it also removes what is not about the
// database itself: the provenance, the plugin named in annotation origins,
// and dpg records, which carry what only dpg knows (dql.ext.dpg.v1.Record).
// Lists keep their order, except overloads under RoutineArguments, so
// normalized catalogs compare with proto.Equal or as canonical JSON.
func Normalize(c *irv1.Catalog, limits []string) *irv1.Catalog {
	c = proto.CloneOf(c)
	n := normalizer{limits: limits, domains: map[string]*irv1.Type{}}
	c.Provenance = nil
	for _, ns := range c.GetNamespaces() {
		for _, d := range ns.GetDomains() {
			n.domains[ns.GetName().GetStored()+"."+d.GetName().GetStored()] = d.GetBase()
		}
	}
	if n.has(UnmanagedObjects) {
		for _, ns := range c.GetNamespaces() {
			unmanaged(ns)
		}
	}
	walk(c.ProtoReflect(), n.message)
	return c
}

type normalizer struct {
	limits []string
	// domains maps each domain, namespace.name, to its base type.
	domains map[string]*irv1.Type
}

func (n normalizer) has(limit string) bool { return slices.Contains(n.limits, limit) }

// unmanaged drops the objects of ns that belong to an installed extension.
func unmanaged(ns *irv1.Namespace) {
	ns.Tables = slices.DeleteFunc(ns.Tables, func(t *irv1.Table) bool { return member(t.Extensions) })
	ns.Enums = slices.DeleteFunc(ns.Enums, func(e *irv1.Enum) bool { return member(e.Extensions) })
	ns.Domains = slices.DeleteFunc(ns.Domains, func(d *irv1.Domain) bool { return member(d.Extensions) })
	ns.Composites = slices.DeleteFunc(ns.Composites, func(ct *irv1.Composite) bool { return member(ct.Extensions) })
	for _, r := range ns.Routines {
		r.Overloads = slices.DeleteFunc(r.Overloads, func(o *irv1.Overload) bool { return member(o.Extensions) })
	}
	ns.Routines = slices.DeleteFunc(ns.Routines, func(r *irv1.Routine) bool { return len(r.Overloads) == 0 })
}

// member reports whether extensions mark an object as an extension's.
func member(exts []*anypb.Any) bool {
	return slices.ContainsFunc(exts, func(a *anypb.Any) bool { return a.MessageIs(&pgextv1.ExtensionMember{}) })
}

// message normalizes one message of the catalog, or of an extension.
func (n normalizer) message(m protoreflect.Message) {
	switch v := m.Interface().(type) {
	case *irv1.Origin:
		v.Plugin = ""
	case *irv1.Table:
		n.table(v)
	case *irv1.Column:
		if n.has(CollationFacts) && !n.textual(v.Type) {
			v.Collation = nil
		}
	case *irv1.Domain:
		if n.has(CollationFacts) && !n.textual(v.Base) {
			v.Collation = nil
		}
	case *irv1.Collation:
		if n.has(CollationFacts) && strings.Contains(v.Name, ".") {
			v.CaseSensitive, v.AccentSensitive, v.PadSpace = 0, 0, 0
			v.Deterministic, v.ClientFold = 0, 0
		}
	case *irv1.Type:
		if n.has(RangeSubtypes) && (v.GetRange() != nil || v.GetMultirange() != nil) &&
			v.GetNative().GetName().GetNamespace() != "pg_catalog" {
			v.Kind = &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}
		}
	case *irv1.Routine:
		if n.has(RoutineArguments) {
			routineArguments(v)
		}
	case *irv1.Overload:
		if n.has(RoutineBodies) && v.Kind == irv1.RoutineKind_ROUTINE_KIND_PROCEDURE {
			procedureLanguage(v)
		}
	}
	n.text(m.Interface())
	if f := m.Descriptor().Fields().ByName("extensions"); f != nil && f.IsList() && m.Has(f) {
		list := m.Get(f).List()
		var keep []*anypb.Any
		for i := range list.Len() {
			if a := n.extension(list.Get(i).Message().Interface().(*anypb.Any)); a != nil {
				keep = append(keep, a)
			}
		}
		list.Truncate(0)
		for _, a := range keep {
			list.Append(protoreflect.ValueOfMessage(a.ProtoReflect()))
		}
	}
}

func (n normalizer) table(t *irv1.Table) {
	view := t.Kind == irv1.TableKind_RELATION_KIND_VIEW || t.Kind == irv1.TableKind_RELATION_KIND_MATERIALIZED_VIEW
	if view && n.has(ViewColumnComments) {
		for _, col := range t.Columns {
			col.Annotations = slices.DeleteFunc(col.Annotations, func(a *irv1.Annotation) bool {
				return a.GetOrigin().GetKind() == irv1.OriginKind_ORIGIN_DB_COMMENT
			})
			col.Extensions = slices.DeleteFunc(col.Extensions, func(a *anypb.Any) bool { return a.MessageIs(&pgextv1.Comment{}) })
		}
	}
	if t.Kind == irv1.TableKind_RELATION_KIND_MATERIALIZED_VIEW && n.has(MaterializedViews) {
		t.Kind = irv1.TableKind_RELATION_KIND_VIEW
	}
}

// textTypes are the built-in types a collation applies to.
var textTypes = []string{"text", "varchar", "bpchar", "name"}

// textual reports whether t, with domains resolved, is a built-in text type
// or an array of one.
func (n normalizer) textual(t *irv1.Type) bool {
	for seen := map[string]bool{}; t.GetNamed().GetDomain() != nil; {
		d := t.GetNamed().GetDomain()
		q := d.Namespace + "." + d.Object
		if seen[q] || n.domains[q] == nil {
			return false
		}
		seen[q] = true
		t = n.domains[q]
	}
	name := t.GetNative().GetName()
	object := name.GetObject()
	if t.GetArray() != nil {
		object = strings.TrimPrefix(object, "_")
	}
	return name.GetNamespace() == "pg_catalog" && slices.Contains(textTypes, object)
}

// routineArguments keeps what a type-only signature says of each overload's
// arguments, and orders the overloads by it.
func routineArguments(r *irv1.Routine) {
	for _, o := range r.Overloads {
		o.Arguments = slices.DeleteFunc(o.Arguments, func(a *irv1.Argument) bool { return a.Mode == irv1.ArgMode_ARG_MODE_OUT })
		for _, a := range o.Arguments {
			a.Name, a.Mode, a.HasDefault = nil, irv1.ArgMode_ARG_MODE_UNSPECIFIED, false
		}
	}
	slices.SortStableFunc(r.Overloads, func(a, b *irv1.Overload) int { return bytes.Compare(argumentTypes(a), argumentTypes(b)) })
}

func argumentTypes(o *irv1.Overload) []byte {
	var out []byte
	for _, a := range o.Arguments {
		b, _ := proto.MarshalOptions{Deterministic: true}.Marshal(a.Type)
		out = append(append(out, b...), 0)
	}
	return out
}

// procedureLanguage clears the language of a procedure's RoutineSource.
func procedureLanguage(o *irv1.Overload) {
	for i, a := range o.Extensions {
		var src pgextv1.RoutineSource
		if a.UnmarshalTo(&src) == nil {
			src.Language = ""
			o.Extensions[i], _ = anypb.New(&src)
			o.Extensions[i].TypeUrl = a.TypeUrl
		}
	}
}

// text clears what the limits leave unknown in one message: SQL text sources
// spell differently, bodies, owners, grants and argument details.
func (n normalizer) text(m proto.Message) {
	exprs, owners, grants := n.has(ExpressionText), n.has(Owners), n.has(Grants)
	args, bodies := n.has(RoutineArguments), n.has(RoutineBodies)
	switch v := m.(type) {
	case *irv1.Default:
		v.Native = when(exprs, "", v.Native)
	case *irv1.Generation:
		v.Native = when(exprs, "", v.Native)
	case *irv1.NativeText:
		v.Native = when(exprs, "", v.Native)
	case *irv1.IndexExpression:
		v.Native = when(exprs, "", v.Native)
	case *pgextv1.Policy:
		v.Using, v.WithCheck = when(exprs, "", v.Using), when(exprs, "", v.WithCheck)
	case *pgextv1.Index:
		v.Definition = when(exprs, "", v.Definition)
	case *pgextv1.Trigger:
		v.Definition = when(exprs, "", v.Definition)
	case *pgextv1.PartitionKey:
		v.Definition = when(exprs, "", v.Definition)
	case *pgextv1.PartitionOf:
		v.Bound = when(exprs, "", v.Bound)
	case *pgextv1.ViewDefinition:
		v.Definition = when(exprs, "", v.Definition)
	case *pgextv1.RoutineSource:
		v.Body = when(bodies, "", v.Body)
	case *pgextv1.RoutineProperties:
		v.ArgumentDefaults = when(args, nil, v.ArgumentDefaults)
	case *pgextv1.Routine:
		v.Body = when(bodies, "", v.Body)
		v.Language = when(bodies && v.Kind == "PROCEDURE", "", v.Language)
		v.IdentityArguments = when(args, "", v.IdentityArguments)
		v.ArgumentDefaults = when(args, nil, v.ArgumentDefaults)
		v.Owner, v.Grants = when(owners, "", v.Owner), when(grants, nil, v.Grants)
	case *pgextv1.RangeType:
		v.Owner, v.Grants = when(owners, "", v.Owner), when(grants, nil, v.Grants)
	}
}

// when is value if cond holds, else otherwise.
func when[T any](cond bool, value, otherwise T) T {
	if cond {
		return value
	}
	return otherwise
}

// extension returns a normalized, or nil to drop it. An extension of a type
// this package does not know is kept unchanged.
func (n normalizer) extension(a *anypb.Any) *anypb.Any {
	m, err := a.UnmarshalNew()
	if err != nil {
		return a
	}
	if n.drop(m) {
		return nil
	}
	walk(m.ProtoReflect(), n.message)
	b, _ := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	return &anypb.Any{TypeUrl: a.TypeUrl, Value: b}
}

// drop reports whether the limits remove an extension altogether.
func (n normalizer) drop(m proto.Message) bool {
	switch v := m.(type) {
	case *dpgextv1.Record:
		return true
	case *pgextv1.Grant:
		return n.has(Grants)
	case *pgextv1.Owner:
		return n.has(Owners)
	case *pgextv1.Database:
		return n.has(DatabaseSettings)
	case *pgextv1.InstalledExtension:
		return n.has(UnmanagedObjects)
	case *pgextv1.Partition:
		return n.has(Partitions)
	case *pgextv1.RangeType:
		return n.has(RangeSubtypes) || n.has(UnmanagedObjects) && v.Extension != ""
	case *pgextv1.Statistics, *pgextv1.Collation:
		return n.has(BodyHashedObjects)
	case *pgextv1.BaseType:
		return n.has(BodyHashedObjects) || n.has(UnmanagedObjects) && v.Extension != ""
	case *pgextv1.Routine:
		return n.has(BodyHashedObjects) && v.Kind == "AGGREGATE" || n.has(UnmanagedObjects) && v.Extension != ""
	}
	return false
}

// walk calls fn on m and then on every message below it, outside maps.
func walk(m protoreflect.Message, fn func(protoreflect.Message)) {
	fn(m)
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.Message() == nil, fd.IsMap():
			// Only struct values have maps, and nothing in them is normalized.
		case fd.IsList():
			for i := range v.List().Len() {
				walk(v.List().Get(i).Message(), fn)
			}
		default:
			walk(v.Message(), fn)
		}
		return true
	})
}
