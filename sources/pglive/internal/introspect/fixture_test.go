package introspect

import (
	"reflect"
	"slices"
	"testing"
)

// find returns the item of items whose key is want, failing t if none is.
func find[T any](t *testing.T, items []T, key func(T) string, want string) T {
	t.Helper()
	i := slices.IndexFunc(items, func(v T) bool { return key(v) == want })
	if i < 0 {
		var zero T
		t.Fatalf("no %T named %q", zero, want)
		return zero
	}
	return items[i]
}

func check(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got  %+v\n want %+v", what, got, want)
	}
}

func relation(t *testing.T, s *Snapshot, name string) Relation {
	t.Helper()
	return find(t, s.Relations, func(r Relation) string { return r.Name }, name)
}

func column(t *testing.T, r Relation, name string) Column {
	t.Helper()
	return find(t, r.Columns, func(c Column) string { return c.Name }, name)
}

func constraint(t *testing.T, r Relation, name string) Constraint {
	t.Helper()
	return find(t, r.Constraints, func(c Constraint) string { return c.Name }, name)
}

func index(t *testing.T, r Relation, name string) Index {
	t.Helper()
	return find(t, r.Indexes, func(i Index) string { return i.Name }, name)
}

func routine(t *testing.T, s *Snapshot, identity string) Routine {
	t.Helper()
	return find(t, s.Routines, func(r Routine) string { return r.Schema + "." + r.Name + "(" + r.IdentityArguments + ")" }, identity)
}

var ci = &CollationRef{Schema: "fx", Name: "ci", Provider: "i", Deterministic: false, ICULocale: "und-u-ks-level2"}

func builtin(name, formatted string, category string) TypeRef {
	return TypeRef{Schema: "pg_catalog", Name: name, Kind: "b", Category: category, Typmod: -1, Formatted: formatted}
}

func TestDatabaseAndExtensions(t *testing.T) {
	s := read(t, fixtureDB)
	check(t, "database", s.Database, Database{Encoding: "UTF8", LocaleProvider: "c", Collate: "C", Ctype: "C"})
	check(t, "extensions", s.Extensions, []Extension{
		{Name: "citext", Version: "1.6", Schema: "ext"},
		{Name: "plpgsql", Version: "1.0", Schema: "pg_catalog"},
	})
	if s.Schemas != nil || s.Relations != nil || s.Routines != nil {
		t.Error("no schemas asked for, yet objects were read")
	}
}

func TestMissingSchemaIsAbsent(t *testing.T) {
	if s := read(t, fixtureDB, "nope"); s.Schemas != nil {
		t.Errorf("schemas = %+v", s.Schemas)
	}
}

func TestSchemasAndCollations(t *testing.T) {
	s := read(t, fixtureDB, "fx", "ext")
	check(t, "schema names", []string{s.Schemas[0].Name, s.Schemas[1].Name}, []string{"ext", "fx"})
	check(t, "fx", s.Schemas[1], Schema{Name: "fx", Owner: fixtureOwner, Comment: "fixture objects", Grants: []Grant{
		{Grantee: fixtureOwner, Privilege: "CREATE", Grantor: fixtureOwner},
		{Grantee: fixtureOwner, Privilege: "USAGE", Grantor: fixtureOwner},
		{Grantee: fixtureReader, Privilege: "USAGE", Grantor: fixtureOwner},
	}})
	check(t, "collations", s.Collations, []Collation{{CollationRef: *ci, Owner: fixtureOwner, Comment: "case-insensitive"}})
}

func TestColumns(t *testing.T) {
	s := read(t, fixtureDB, "fx")
	b := relation(t, s, "bookings")
	check(t, "column order", func() (names []string) {
		for _, c := range b.Columns {
			names = append(names, c.Name)
		}
		return
	}(), []string{"id", "room_id", "code", "label", "nick", "during", "readings", "spans", "grid", "ticket"})
	check(t, "label", column(t, b, "label"), Column{
		Name: "label", Ordinal: 4, Type: builtin("text", "text", "S"), Collation: ci,
		Grants: []Grant{{Grantee: fixtureReader, Privilege: "UPDATE", Grantor: fixtureOwner}},
	})
	check(t, "nick type", column(t, b, "nick").Type,
		TypeRef{Schema: "ext", Name: "citext", Kind: "b", Category: "S", Typmod: -1, Formatted: "ext.citext"})
	check(t, "spans type", column(t, b, "spans").Type,
		TypeRef{Schema: "fx", Name: "float_multirange", Kind: "m", Category: "R", Typmod: -1, Formatted: "fx.float_multirange"})
	check(t, "grid type", column(t, b, "grid").Type, TypeRef{
		Schema: "pg_catalog", Name: "_int4", Kind: "b", Category: "A", Typmod: -1, Formatted: "integer[]",
		Element: &ElementRef{Schema: "pg_catalog", Name: "int4", Kind: "b"}, Dimensions: 2,
	})
	check(t, "readings element", column(t, b, "readings").Type.Element, &ElementRef{Schema: "fx", Name: "float_range", Kind: "r"})
	check(t, "serial default", column(t, b, "id").Default, "nextval('fx.bookings_id_seq'::regclass)")
	check(t, "domain column", column(t, b, "code").Type.Kind, "d")
}

func TestConstraints(t *testing.T) {
	b := relation(t, read(t, fixtureDB, "fx"), "bookings")
	check(t, "foreign key", constraint(t, b, "bookings_room_id_fkey"), Constraint{
		Name: "bookings_room_id_fkey", Kind: "f", Columns: []string{"room_id"},
		Definition: "FOREIGN KEY (room_id) REFERENCES fx.rooms(id) MATCH FULL ON UPDATE CASCADE ON DELETE SET NULL (room_id) DEFERRABLE INITIALLY DEFERRED",
		References: &ForeignKey{Schema: "fx", Table: "rooms", Columns: []string{"id"}, Match: "f",
			OnUpdate: "c", OnDelete: "n", OnDeleteColumns: []string{"room_id"}},
		Deferrable: true, InitiallyDeferred: true, NoInherit: true,
	})
	check(t, "unique nulls not distinct", constraint(t, b, "bookings_code_key"), Constraint{
		Name: "bookings_code_key", Kind: "u", Columns: []string{"code"}, Definition: "UNIQUE NULLS NOT DISTINCT (code)",
		NoInherit: true, Index: "bookings_code_key", NullsNotDistinct: true,
	})
	check(t, "exclusion", constraint(t, b, "bookings_no_overlap"), Constraint{
		Name: "bookings_no_overlap", Kind: "x", Columns: []string{"during"}, Definition: "EXCLUDE USING gist (during WITH &&)",
		NoInherit: true, Index: "bookings_no_overlap", ExclusionOperators: []string{"&&(anyrange,anyrange)"},
		Comment: "no double booking",
	})
	check(t, "not valid check", constraint(t, b, "bookings_room_check"), Constraint{
		Name: "bookings_room_check", Kind: "c", Columns: []string{"room_id"}, Definition: "CHECK ((room_id > 0)) NOT VALID",
		NotValid: true,
	})
}

func TestIndexes(t *testing.T) {
	s := read(t, fixtureDB, "fx")
	check(t, "expression index", index(t, relation(t, s, "bookings"), "bookings_lower_idx"), Index{
		Name: "bookings_lower_idx", Method: "btree",
		Keys:       []IndexKey{{Expression: "lower(during)"}, {Column: "room_id"}, {Column: "label", Included: true}},
		Definition: "CREATE INDEX bookings_lower_idx ON fx.bookings USING btree (lower(during), room_id DESC) INCLUDE (label) WITH (fillfactor='70')",
		Options:    []string{"fillfactor=70"}, Comment: "expression index",
	})
	check(t, "materialized view index", index(t, relation(t, s, "room_counts"), "room_counts_id").Unique, true)
}

func TestTriggersAndPolicies(t *testing.T) {
	s := read(t, fixtureDB, "fx")
	b := relation(t, s, "bookings")
	check(t, "triggers", b.Triggers, []Trigger{
		{Name: "bookings_audit", Timing: "AFTER", Events: []string{"DELETE", "INSERT", "TRUNCATE"}, Level: "STATEMENT",
			Function: ObjectName{Schema: "fx", Name: "touch"}, Enabled: "D",
			Definition: "CREATE TRIGGER bookings_audit AFTER INSERT OR DELETE OR TRUNCATE ON fx.bookings FOR EACH STATEMENT EXECUTE FUNCTION fx.touch()"},
		{Name: "bookings_touch", Timing: "BEFORE", Events: []string{"UPDATE"}, Level: "ROW",
			Function: ObjectName{Schema: "fx", Name: "touch"}, Enabled: "O",
			Definition: "CREATE TRIGGER bookings_touch BEFORE UPDATE ON fx.bookings FOR EACH ROW EXECUTE FUNCTION fx.touch()",
			Comment:    "keeps rows fresh"},
	})
	check(t, "instead of", relation(t, s, "room_names").Triggers[0].Timing, "INSTEAD OF")
	check(t, "policies", b.Policies, []Policy{
		{Name: "bookings_limit", Command: "*", Permissive: false, Roles: []string{"PUBLIC"}, Using: "true", WithCheck: "(room_id < 1000)"},
		{Name: "bookings_read", Command: "r", Permissive: true, Roles: []string{fixtureReader}, Using: "(room_id > 0)", Comment: "readers"},
	})
	check(t, "row security", []bool{b.RowSecurity, b.ForceRowSecurity}, []bool{true, true})
	check(t, "options", b.Options, []string{"fillfactor=90"})
	check(t, "reader grants", b.Grants[len(b.Grants)-2:], []Grant{
		{Grantee: fixtureReader, Privilege: "INSERT", Grantor: fixtureOwner, Grantable: true},
		{Grantee: fixtureReader, Privilege: "SELECT", Grantor: fixtureOwner, Grantable: true},
	})
}

func TestPartitionsInheritanceAndViews(t *testing.T) {
	s := read(t, fixtureDB, "fx")
	check(t, "partitioned", relation(t, s, "events").PartitionKey, "RANGE (happened)")
	child := relation(t, s, "events_2026")
	check(t, "partition", []any{child.PartitionOf, child.PartitionBound, child.Inherits},
		[]any{&ObjectName{Schema: "fx", Name: "events"}, "FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')", []ObjectName(nil)})
	derived := relation(t, s, "derived")
	check(t, "inheritance", []any{derived.PartitionOf, derived.Inherits}, []any{(*ObjectName)(nil), []ObjectName{{Schema: "fx", Name: "base"}}})
	check(t, "unlogged", relation(t, s, "base").Persistence, "u")
	mv := relation(t, s, "room_counts")
	check(t, "materialized view", []any{mv.Kind, mv.DependsOn}, []any{"m", []ObjectName{{Schema: "fx", Name: "bookings"}, {Schema: "fx", Name: "rooms"}}})
}

func TestTypes(t *testing.T) {
	s := read(t, fixtureDB, "fx", "ext")
	check(t, "enum labels in sort order", s.Enums[0].Labels, []string{"sad", "neutral", "happy"})
	check(t, "enum grants", len(s.Enums[0].Grants), 3)
	check(t, "domain", s.Domains[0], Domain{
		Schema: "fx", Name: "code", Owner: fixtureOwner, Comment: "a short code",
		Base: builtin("text", "text", "S"), NotNull: true, Default: "'none'::text", Collation: ci,
		Checks: []DomainCheck{
			{Name: "code_ascii", Definition: "CHECK ((VALUE ~ '^[ -~]*$'::text)) NOT VALID", NotValid: true},
			{Name: "code_length", Definition: "CHECK ((char_length(VALUE) <= 8))", Comment: "@length"},
			{Name: "code_lower", Definition: "CHECK ((VALUE = lower(VALUE)))"},
		},
	})
	check(t, "composite", s.Composites[0].Attributes[2], Attribute{
		Name: "label", Ordinal: 3, Type: builtin("text", "text", "S"), Collation: ci, Comment: "attribute comment",
	})
	check(t, "range", s.Ranges[0], Range{
		Schema: "fx", Name: "float_range", Owner: fixtureOwner, Comment: "a range",
		Subtype:    builtin("float8", "double precision", "N"),
		Multirange: ObjectName{Schema: "fx", Name: "float_multirange"}, SubtypeDiff: "float8mi",
	})
	citext := find(t, s.BaseTypes, func(b BaseType) string { return b.Name }, "citext")
	check(t, "extension type", []string{citext.Schema, citext.Kind, citext.Extension}, []string{"ext", "b", "citext"})
	check(t, "extension routine", routine(t, s, "ext.citext(boolean)").Extension, "citext")
}

func TestSequences(t *testing.T) {
	s := read(t, fixtureDB, "fx")
	check(t, "sequence", s.Sequences[1], Sequence{
		Schema: "fx", Name: "tickets", Owner: fixtureOwner,
		DataType: builtin("int4", "integer", "N"),
		Start:    100, Increment: 5, Min: 10, Max: 1000, Cache: 3, Cycle: true,
		OwnedBy: &ColumnName{Schema: "fx", Table: "bookings", Column: "ticket"},
	})
	check(t, "serial is not identity", s.Sequences[0].Identity, false)
}

func TestRoutines(t *testing.T) {
	s := read(t, fixtureDB, "fx")
	reset := routine(t, s, "fx.reset(INOUT n integer, IN reason text)")
	check(t, "procedure", []any{reset.Kind, reset.Comment, reset.DefaultCount, reset.Result}, []any{"p", "@public", int16(1), ""})
	check(t, "procedure arguments", reset.Arguments, []Argument{
		{Name: "n", Mode: "b", Type: builtin("int4", "integer", "N")},
		{Name: "reason", Mode: "i", Type: builtin("text", "text", "S"), HasDefault: true, Default: "'manual'::text"},
	})
	total := routine(t, s, "fx.total(a integer, OUT total bigint, VARIADIC rest integer[])")
	check(t, "argument modes", []string{total.Arguments[0].Mode, total.Arguments[1].Mode, total.Arguments[2].Mode}, []string{"i", "o", "v"})
	check(t, "variadic default", total.Arguments[2].Default, "'{}'::integer[]")
	list := routine(t, s, "fx.room_list(min_id integer)")
	check(t, "returns table", []any{list.Result, list.ReturnsSet, list.Arguments[1].Mode, list.Arguments[2].Name},
		[]any{"TABLE(id integer, name text)", true, "t", "name"})
	ids := routine(t, s, "fx.room_ids()")
	check(t, "atomic body", []any{ids.Source, ids.SQLBody, ids.SecurityDefiner, ids.Parallel},
		[]any{"", "BEGIN ATOMIC\n SELECT rooms.id\n    FROM fx.rooms;\nEND", true, "s"})
	check(t, "routine grants", ids.Grants, []Grant{
		{Grantee: fixtureOwner, Privilege: "EXECUTE", Grantor: fixtureOwner},
		{Grantee: fixtureReader, Privilege: "EXECUTE", Grantor: fixtureOwner},
	})
}

func TestStatistics(t *testing.T) {
	s := read(t, fixtureDB, "fx")
	check(t, "statistics", s.Statistics, []Statistics{
		{Schema: "fx", Name: "bookings_expr", Owner: fixtureOwner, Table: ObjectName{Schema: "fx", Name: "bookings"},
			Kinds: []string{"e"}, Expressions: []string{"lower(during)"},
			Definition: "CREATE STATISTICS fx.bookings_expr ON lower(during) FROM fx.bookings"},
		{Schema: "fx", Name: "bookings_stats", Owner: fixtureOwner, Comment: "correlated",
			Table: ObjectName{Schema: "fx", Name: "bookings"}, Kinds: []string{"d", "f"}, Columns: []string{"room_id", "code"},
			Definition: "CREATE STATISTICS fx.bookings_stats (ndistinct, dependencies) ON room_id, code FROM fx.bookings"},
	})
}
