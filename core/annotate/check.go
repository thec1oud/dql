package annotate

import (
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Target describes the object an annotation is on, with the facts the
// checks of §7.3 need. Source and catalog code fill in what applies to the
// kind and leave the rest zero.
type Target struct {
	Kind Kind
	Name *irv1.QualifiedName
	// Words are the object's own words (§4.2), as core/naming splits its
	// stored name, before any name annotation.
	Words []string
	// Type is the logical type of a column, attribute or result as the source
	// read it, before annotations narrow it, with a domain resolved to its
	// base type (§8.5). On a function it is the type of its scalar or set-of
	// result, and nil when it returns a table or nothing.
	Type *irv1.Type
	// NotNull is set when the database guarantees, or the source proves, that
	// a column, attribute or result is non-null, before any annotation
	// narrows it (§6.4, §6.5). On a foreign key it is set when a referencing
	// column is NOT NULL, so the key cannot be set to NULL (§14.6).
	NotNull bool
	// Generated is set for a GENERATED ALWAYS AS (expr) column (§11.3).
	Generated bool
	// OnUpdate is set when the source detected that the database sets the
	// column on every update, as MySQL's ON UPDATE CURRENT_TIMESTAMP (§11.3).
	OnUpdate bool
	// TinyIntOne is set for a MySQL TINYINT(1) column (§8.7). The source
	// plugin reports it, so the core holds no dialect knowledge.
	TinyIntOne bool
	// PrimaryKey is set for a table with a primary key (§11.1).
	PrimaryKey bool
	// Columns are a table's or view's columns. Their nullability is after
	// nonnull annotations, since those are what make a view key (§6.4).
	Columns []Column
	// Membership is set for a check constraint the source recognized as a
	// single-column membership test (§8.4, §10.1).
	Membership bool
}

// Column is a table or view column as logical-key sees it.
type Column struct {
	Name     string
	Nullable bool
}

// subsumedBy maps a key to the keys that already produce its every effect in
// the modality table of §14.2, where a forbidden result from any rule wins.
// immutable and db-updated only forbid Partial, which bound, read-only,
// soft-delete (§21.3) and version also do; db-filled only makes Draft
// optional, which version also does and which bound, read-only and
// soft-delete override by forbidding Draft; read-only forbids Draft and
// Partial, as bound and soft-delete do. Keys with effects beyond the
// modalities (write-only, bound, version, sensitive, soft-delete) are never
// subsumed.
var subsumedBy = map[string][]string{
	"db-filled":  {"bound", "read-only", "soft-delete", "version"},
	"db-updated": {"bound", "read-only", "soft-delete", "version"},
	"immutable":  {"bound", "read-only", "soft-delete", "version"},
	"read-only":  {"bound", "soft-delete"},
}

// Check checks one target's effective annotations (§7.3) and returns its
// diagnostics, sorted (§3.2, rule 5). Plugin keys pass untouched. Superseded
// annotations are not checked, because they have no effect.
func Check(t Target, anns []*irv1.Annotation) []*irv1.Diagnostic {
	c := &checker{t: t, at: diagnostics.Name(t.Name)}
	for _, a := range anns {
		c.has = append(c.has, a.GetKey())
	}
	for _, a := range anns {
		c.check(a)
	}
	if c.present("bound") && c.present("version") {
		// A bound column is supplied by the scope and forbidden in Partial,
		// while version is compared and incremented by every write (§14.2).
		c.errorf(CodeBoundWithVersion,
			"%s is annotated both bound and version; a scope-supplied column cannot be a version counter, so remove one of them", c.at)
	}
	diagnostics.Sort(c.ds)
	return c.ds
}

type checker struct {
	t   Target
	at  string
	has []string
	ds  []*irv1.Diagnostic
}

func (c *checker) present(name string) bool { return slices.Contains(c.has, CorePrefix+name) }

func (c *checker) errorf(code, format string, args ...any) {
	c.ds = append(c.ds, diagnostics.Errorf(code, c.t.Name, format, args...))
}

func (c *checker) redundant(format string, args ...any) {
	c.ds = append(c.ds, diagnostics.Warningf(CodeRedundant, c.t.Name, format, args...))
}

func (c *checker) wrongTarget(k Key, why string) {
	c.wrongTargetFix(k, why, "remove the annotation or move it to "+k.Target)
}

func (c *checker) wrongTargetFix(k Key, why, fix string) {
	c.errorf(CodeWrongTarget, "%s applies to %s, and %s %s; %s (%s)", k.IR(), k.Target, c.at, why, fix, k.See)
}

func (c *checker) check(a *irv1.Annotation) {
	key := a.GetKey()
	if strings.HasPrefix(key, PluginPrefix) {
		if !IsPlugin(key) {
			c.errorf(CodeKeyInvalid, "annotation key %q on %s names no plugin or no key; write plugin keys as x.<plugin>.<key> (§7.2)", key, c.at)
		}
		return // plugin keys pass through untouched (§7.3)
	}
	k, ok := Lookup(key)
	switch {
	case ok:
	case strings.HasPrefix(key, CorePrefix):
		c.errorf(CodeUnknown, "unknown core annotation %s on %s; check its spelling against the core vocabulary, or use x.<plugin>.<key> for a plugin's key (§7.2)", key, c.at)
		return
	default:
		c.errorf(CodeKeyInvalid, "annotation key %q on %s is neither core.<key> nor x.<plugin>.<key>; the source plugin must write it in one of those forms (§7.2)", key, c.at)
		return
	}
	if !k.Allows(c.t.Kind) {
		c.wrongTarget(k, "is "+article(c.t.Kind.String()))
		return
	}
	if want := valueProblem(k, a.GetValue()); want != "" {
		c.errorf(CodeValueInvalid, "%s on %s %s (%s)", k.IR(), c.at, want, k.See)
		return
	}
	c.rules(k, a.GetValue())
}

// valueProblem says what is wrong with v as a value of k, or "" when it is
// well formed.
func valueProblem(k Key, v *irv1.AnnotationValue) string {
	switch k.Form {
	case FormNone:
		if v.GetKind() != nil {
			return "takes no value; remove the value"
		}
	case FormString:
		if v.GetString_() == "" {
			return "needs a name as its value"
		}
	case FormChoice:
		if !slices.Contains(k.Choices, v.GetString_()) {
			return "must be one of " + strings.Join(k.Choices, ", ")
		}
	case FormWords:
		if _, ok := stringsOf(v.GetWords()); !ok {
			return "needs a non-empty word list as its value"
		}
	case FormList:
		if cols, ok := stringsOf(v.GetList()); !ok || hasDuplicates(cols) {
			return "needs a list of distinct column names as its value"
		}
	case FormStruct:
		return structProblem(k, v.GetStruct())
	}
	return ""
}

// structProblem checks a relation value: a required to, and optional name and
// inverse-name, as in §5.3. A name is a string or a word list.
func structProblem(k Key, s *structpb.Struct) string {
	fix := "needs a struct with to, and optionally name and inverse-name"
	if s == nil {
		return fix
	}
	if to, ok := s.GetFields()["to"].GetKind().(*structpb.Value_StringValue); !ok || to.StringValue == "" {
		return fix + "; to names the referenced column"
	}
	var fields []string
	for f := range s.GetFields() {
		fields = append(fields, f)
	}
	slices.Sort(fields)
	for _, f := range fields {
		v := s.GetFields()[f]
		switch {
		case !slices.Contains(k.Fields, f):
			return fix + "; remove field " + f
		case f == "to":
		case v.GetStringValue() != "":
		default:
			if _, ok := stringsOf(v.GetListValue()); !ok {
				return fix + "; " + f + " must be a name or a word list"
			}
		}
	}
	return ""
}

// stringsOf returns the non-empty strings of a non-empty list, or false.
func stringsOf(l *structpb.ListValue) ([]string, bool) {
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

func hasDuplicates(ss []string) bool {
	s := slices.Clone(ss)
	slices.Sort(s)
	return len(slices.Compact(s)) != len(ss)
}

// rules applies the target rules beyond the target kind, and the
// redundancy warnings, to a well-formed annotation.
func (c *checker) rules(k Key, v *irv1.AnnotationValue) {
	t := c.t
	typ := typeName(t.Type)
	switch k.Name {
	case "version":
		if !slices.Contains(integers, typ) {
			c.wrongTarget(k, "is "+article(describe(t.Type))+" column")
		}
	case "array-elements-nonnull":
		switch {
		case typ != "array":
			c.wrongTarget(k, "is "+article(describe(t.Type))+" column")
		case !t.Type.GetArray().GetElementNullable():
			c.redundant("%s on %s changes nothing: its elements are already non-null; remove the annotation (§8.6)", k.IR(), c.at)
		}
	case "bool":
		if !t.TinyIntOne {
			c.wrongTarget(k, "is not a TINYINT(1) column")
		}
	case "soft-delete":
		// A nullable timestamp is deleted when set; a boolean when true (§21.3).
		isBool := typ == "bool" || (t.TinyIntOne && c.present("bool"))
		switch {
		case isBool:
		case !slices.Contains(timestamps, typ):
			c.wrongTarget(k, "is "+article(describe(t.Type))+" column")
		case t.NotNull:
			c.wrongTargetFix(k, "is a NOT NULL timestamp, which can never mark a row as not deleted",
				"make the column nullable or remove the annotation")
		}
	case "enum":
		if !t.Membership {
			c.errorf(CodeEnumNotMembership,
				"%s on %s needs a check that tests one column for membership in a list of values, such as CHECK (status IN ('new', 'paid')); remove the annotation or rewrite the check (§8.4)", k.IR(), c.at)
		}
	case "logical-key":
		c.logicalKey(k, v)
	case "removal":
		// unlink sets the foreign key to NULL (§14.6).
		if v.GetString_() == "unlink" && t.NotNull {
			c.wrongTargetFix(k, "has a NOT NULL column, so unlink cannot set it to NULL",
				"use removal: delete, or make the foreign key's columns nullable")
		}
	case "nonnull":
		switch {
		case t.Kind == KindFunction && t.Type == nil:
			// A function's comment annotates its one result value; a table
			// result narrows its columns one by one (§6.5, §7.2).
			c.wrongTargetFix(k, "returns no single value", "annotate its result columns instead, or remove the annotation")
		case t.NotNull:
			c.redundant("%s on %s changes nothing: the database already guarantees it is non-null; remove the annotation (§7.3)", k.IR(), c.at)
		}
	case "name":
		if len(t.Words) > 0 && slices.Equal(t.Words, mustStrings(v.GetWords())) {
			c.redundant("%s on %s changes nothing: they are already its words; remove the annotation (§4.4)", k.IR(), c.at)
		}
	case "collection-policy":
		if v.GetString_() == "merge" {
			c.redundant("%s: merge on %s changes nothing: merge is the default policy; remove the annotation (§14.6)", k.IR(), c.at)
		}
	case "as":
		if v.GetString_() == typ {
			c.redundant("%s(%s) on %s changes nothing: the column already has that type; remove the annotation (§7.2)", k.IR(), typ, c.at)
		}
	case "db-updated":
		if t.OnUpdate {
			c.redundant("%s on %s changes nothing: the database already sets it on every update; remove the annotation (§11.3)", k.IR(), c.at)
			return
		}
	}
	c.subsumed(k)
}

// subsumed warns about a modality annotation whose effect other annotations,
// or the column's generation class, already produce (§14.2, §11.3).
func (c *checker) subsumed(k Key) {
	by, ok := subsumedBy[k.Name]
	if !ok {
		return
	}
	if c.t.Generated {
		// A generated column is forbidden in Draft and Partial by its class.
		c.redundant("%s on %s changes nothing: a generated column is already forbidden in Draft and Partial; remove the annotation (§11.3)", k.IR(), c.at)
		return
	}
	for _, other := range by {
		if c.present(other) {
			c.redundant("%s on %s changes nothing next to %s%s, which already has its effect; remove it (§14.2)", k.IR(), c.at, CorePrefix, other)
			return
		}
	}
}

func (c *checker) logicalKey(k Key, v *irv1.AnnotationValue) {
	t := c.t
	if t.Kind == KindTable && t.PrimaryKey {
		c.wrongTargetFix(k, "has a primary key, which is already its key", "remove the annotation")
		return
	}
	for _, name := range mustStrings(v.GetList()) {
		i := slices.IndexFunc(t.Columns, func(col Column) bool { return col.Name == name })
		switch {
		case i < 0:
			c.errorf(CodeValueInvalid, "%s on %s names %s, which is not one of its columns; name existing columns (§11.1)", k.IR(), c.at, name)
		case t.Columns[i].Nullable:
			c.errorf(CodeLogicalKeyNullable,
				"%s on %s includes nullable column %s, and a key with a nullable column cannot identify a row; add nonnull to the column or key on non-null columns (§11.1)", k.IR(), c.at, name)
		}
	}
}

// article puts "a" or "an" before a noun phrase.
func article(s string) string {
	if strings.ContainsRune("aeiou", rune(s[0])) {
		return "an " + s
	}
	return "a " + s
}

// mustStrings returns a list already checked by valueProblem.
func mustStrings(l *structpb.ListValue) []string {
	s, _ := stringsOf(l)
	return s
}
