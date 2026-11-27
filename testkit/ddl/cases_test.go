package ddl

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	mysqlextv1 "github.com/thec1oud/dql/proto/dql/ext/mysql/v1"
	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	sqliteextv1 "github.com/thec1oud/dql/proto/dql/ext/sqlite/v1"
	sqlserverextv1 "github.com/thec1oud/dql/proto/dql/ext/sqlserver/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// ext packs m.
func ext(m proto.Message) *anypb.Any {
	a, err := anypb.New(m)
	if err != nil {
		panic(err)
	}
	return a
}

// alien is an extension no writer writes, and broken one no binary links.
var (
	alien  = ext(&irv1.Empty{})
	broken = &anypb.Any{TypeUrl: "type.dql.dev/dql.nope"}
)

// table returns the table of c's first namespace named name.
func table(c *irv1.Catalog, name string) *irv1.Table {
	for _, t := range c.GetNamespaces()[0].GetTables() {
		if t.GetName().GetStored() == name {
			return t
		}
	}
	panic(name)
}

// column returns t's column named name.
func column(t *irv1.Table, name string) *irv1.Column {
	for _, c := range t.GetColumns() {
		if c.GetName().GetStored() == name {
			return c
		}
	}
	panic(name)
}

// check returns t's check constraint named name.
func check(t *irv1.Table, name string) *irv1.Constraint {
	for _, c := range t.GetConstraints() {
		if c.GetName() == name {
			return c
		}
	}
	panic(name)
}

// key returns t's uniqueness named name.
func key(t *irv1.Table, name string) *irv1.Uniqueness {
	for _, u := range t.GetUniqueness() {
		if u.GetName() == name {
			return u
		}
	}
	panic(name)
}

// overload returns the first overload of c's first routine.
func overload(c *irv1.Catalog) *irv1.Overload {
	return c.GetNamespaces()[0].GetRoutines()[0].GetOverloads()[0]
}

// portable returns a portable check of kind p on column.
func portable(name, column string, p *irv1.PortableConstraint) *irv1.Constraint {
	return &irv1.Constraint{Name: name, Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK, Columns: []string{column},
		Form: &irv1.Constraint_Portable{Portable: p}}
}

func ptr(n uint32) *uint32 { return &n }

func str(s string) *irv1.Value { return &irv1.Value{Kind: &irv1.Value_String_{String_: s}} }

// A case changes a golden catalog and expects an error, or text in the DDL.
type writeCase struct {
	name   string
	golden string
	change func(c *irv1.Catalog)
	err    string
	want   []string
}

func TestCases(t *testing.T) {
	var cases []writeCase
	cases = append(cases, postgresCases...)
	cases = append(cases, mysqlCases...)
	cases = append(cases, sqliteCases...)
	cases = append(cases, sqlserverCases...)
	for _, c := range cases {
		t.Run(c.golden+"/"+c.name, func(t *testing.T) {
			b := bundle(t, c.golden)
			cat := b.GetCatalogs()[0]
			c.change(cat)
			got, err := Write(b.GetDialect(), cat)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("got error %v, want one holding %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("the DDL lacks %q:\n%s", w, got)
				}
			}
		})
	}
}

func TestNoWriter(t *testing.T) {
	if _, err := Write(irv1.Dialect_DIALECT_UNSPECIFIED, &irv1.Catalog{}); err == nil || err.Error() != "ddl: no writer for DIALECT_UNSPECIFIED" {
		t.Error(err)
	}
}

// Changes every dialect fails the same way for.
func failures(golden string, tableName, viewName string) []writeCase {
	return []writeCase{
		{"broken catalog extension", golden, func(c *irv1.Catalog) { c.Extensions = append(c.Extensions, broken) }, "extension type.dql.dev/dql.nope", nil},
		{"alien catalog extension", golden, func(c *irv1.Catalog) { c.Extensions = append(c.Extensions, alien) }, "catalog: no writer for extension dql.ir.v1.Empty", nil},
		{"alien table extension", golden, func(c *irv1.Catalog) { t := table(c, tableName); t.Extensions = append(t.Extensions, alien) }, "no writer for extension", nil},
		{"alien view extension", golden, func(c *irv1.Catalog) { t := table(c, viewName); t.Extensions = append(t.Extensions, alien) }, "no writer for extension", nil},
		{"view without definition", golden, func(c *irv1.Catalog) { table(c, viewName).Extensions = nil }, "no definition", nil},
		{"alien view column extension", golden, func(c *irv1.Catalog) {
			col := table(c, viewName).GetColumns()[0]
			col.Extensions = append(col.Extensions, alien)
		}, "no writer for extension", nil},
		{"alien column extension", golden, func(c *irv1.Catalog) {
			col := table(c, tableName).GetColumns()[0]
			col.Extensions = append(col.Extensions, alien)
		}, "no writer for extension", nil},
		{"alien key extension", golden, func(c *irv1.Catalog) {
			u := table(c, tableName).GetUniqueness()[0]
			u.Extensions = append(u.Extensions, alien)
		}, "no writer for extension", nil},
		{"alien foreign key extension", golden, func(c *irv1.Catalog) {
			fk := table(c, "posts").GetForeignKeys()[0]
			fk.Extensions = append(fk.Extensions, alien)
		}, "no writer for extension", nil},
		{"alien check extension", golden, func(c *irv1.Catalog) {
			k := check(table(c, "posts"), "posts_title_check")
			k.Extensions = append(k.Extensions, alien)
		}, "no writer for extension", nil},
		{"exclusion", golden, func(c *irv1.Catalog) {
			check(table(c, "posts"), "posts_published_check").Kind = irv1.ConstraintKind_CONSTRAINT_KIND_EXCLUSION
		}, "of kind CONSTRAINT_KIND_EXCLUSION", nil},
		{"a type with no native text", golden, func(c *irv1.Catalog) {
			table(c, tableName).GetColumns()[0].Type = &irv1.Type{}
		}, "a type with no native text", nil},
		{"a narrowed type", golden, func(c *irv1.Catalog) {
			col := table(c, tableName).GetColumns()[0]
			col.Type = &irv1.Type{Narrowed: &irv1.Narrowing{Key: "uuid", Stored: col.GetType()}}
		}, "", nil},
	}
}

var postgresCases = append(failures("shop", "users", "published_posts"), []writeCase{
	{"collation", "shop", func(c *irv1.Catalog) {
		column(table(c, "users"), "email").Collation = &irv1.Collation{Name: "C"}
	}, "", []string{`"email" app.email_address COLLATE "C" NOT NULL`}},
	{"not-empty, range and membership checks", "shop", func(c *irv1.Catalog) {
		t := table(c, "users")
		t.Constraints = append(t.Constraints,
			portable("a", "email", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_NotEmpty{NotEmpty: &irv1.NotEmptyConstraint{}}}),
			portable("b", "version", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Range{Range: &irv1.RangeConstraint{
				Min: &irv1.Value{Kind: &irv1.Value_Uint{Uint: 1}}, Max: &irv1.Value{Kind: &irv1.Value_Decimal{Decimal: "9.5"}}, MinInclusive: true}}}),
			portable("c", "version", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Range{Range: &irv1.RangeConstraint{
				Max: &irv1.Value{Kind: &irv1.Value_Float{Float: 2.5}}, MaxInclusive: true}}}),
			portable("d", "version", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Membership{Membership: &irv1.MembershipConstraint{
				Values: []*irv1.Value{{Kind: &irv1.Value_Bool{Bool: true}}, {Kind: &irv1.Value_Int{Int: -1}}}}}}),
			portable("e", "email", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Length{Length: &irv1.LengthConstraint{
				Min: ptr(2), Unit: irv1.LengthUnit_LENGTH_UNIT_CHARACTERS}}}),
		)
	}, "", []string{
		`CONSTRAINT "a" CHECK ("email" <> '')`,
		`CONSTRAINT "b" CHECK ("version" >= 1 AND "version" < 9.5)`,
		`CONSTRAINT "c" CHECK ("version" <= 2.5)`,
		`CONSTRAINT "d" CHECK ("version" IN (true, -1))`,
		`CONSTRAINT "e" CHECK (char_length("email") >= 2)`,
	}},
	{"a range bounded below only", "shop", func(c *irv1.Catalog) {
		t := table(c, "users")
		t.Constraints = append(t.Constraints, portable("b", "version", &irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Range{
			Range: &irv1.RangeConstraint{Min: &irv1.Value{Kind: &irv1.Value_Int{Int: 0}}}}}))
	}, "", []string{`CONSTRAINT "b" CHECK ("version" > 0)`}},
	{"a length in bytes", "shop", func(c *irv1.Catalog) {
		check(table(c, "posts"), "posts_title_check").GetPortable().GetLength().Unit = irv1.LengthUnit_LENGTH_UNIT_BYTES
	}, "no Postgres check", nil},
	{"digits", "shop", func(c *irv1.Catalog) {
		check(table(c, "posts"), "posts_title_check").Form = &irv1.Constraint_Portable{Portable: &irv1.PortableConstraint{
			Kind: &irv1.PortableConstraint_Digits{Digits: &irv1.DigitsConstraint{Precision: 3}}}}
	}, "no Postgres check", nil},
	{"domain default and nullability", "shop", func(c *irv1.Catalog) {
		d := c.GetNamespaces()[0].GetDomains()[0]
		d.Default = &irv1.Default{Class: irv1.DefaultClass_DEFAULT_CONSTANT, Native: "'x'::text"}
		d.Nullable = false
	}, "", []string{`DEFAULT 'x'::text NOT NULL CONSTRAINT`}},
	{"a sequence that is no identity's", "shop", func(c *irv1.Catalog) {
		ns := c.GetNamespaces()[0]
		ns.Sequences = append(ns.Sequences, &irv1.Sequence{Name: &irv1.Identifier{Stored: "tickets"}})
	}, "sequence tickets: no writer", nil},
	{"an identity with no sequence", "shop", func(c *irv1.Catalog) {
		column(table(c, "users"), "id").GetDefault().GetSequence().Object = "nope"
	}, "no sequence nope", nil},
	{"a cycling identity", "shop", func(c *irv1.Catalog) {
		ns := c.GetNamespaces()[0]
		for _, s := range ns.GetSequences() {
			s.Cycle = true
		}
	}, "", []string{"MAXVALUE 9223372036854775807 CYCLE)"}},
	{"an identity sequence's comment", "shop", func(c *irv1.Catalog) {
		s := c.GetNamespaces()[0].GetSequences()[0]
		s.Extensions = append(s.Extensions, ext(&pgextv1.Comment{Text: "x"}))
	}, "no writer for extension dql.ext.postgres.v1.Comment", nil},
	{"nulls not distinct", "shop", func(c *irv1.Catalog) {
		key(table(c, "users"), "users_tenant_email_key").NullsDistinct = false
	}, "", []string{`CONSTRAINT "users_tenant_email_key" UNIQUE NULLS NOT DISTINCT ("tenant_id", "email")`}},
	{"an index with no definition", "shop", func(c *irv1.Catalog) {
		key(table(c, "posts"), "posts_one_published_slug").Extensions = nil
	}, "posts_one_published_slug on \"app\".\"posts\": no definition", nil},
	{"match full", "shop", func(c *irv1.Catalog) {
		table(c, "posts").GetForeignKeys()[0].Match = irv1.ForeignKeyMatch_FOREIGN_KEY_MATCH_FULL
	}, "MATCH FOREIGN_KEY_MATCH_FULL", nil},
	{"materialized view", "shop", func(c *irv1.Catalog) {
		table(c, "published_posts").Kind = irv1.TableKind_RELATION_KIND_MATERIALIZED_VIEW
	}, "no writer for RELATION_KIND_MATERIALIZED_VIEW", nil},
	{"procedure", "shop", func(c *irv1.Catalog) { overload(c).Kind = irv1.RoutineKind_ROUTINE_KIND_PROCEDURE }, "no writer for a routine", nil},
	{"routine with no source", "shop", func(c *irv1.Catalog) { overload(c).Extensions = nil }, "no writer for a routine", nil},
	{"out argument", "shop", func(c *irv1.Catalog) { overload(c).GetArguments()[0].Mode = irv1.ArgMode_ARG_MODE_OUT }, "of mode ARG_MODE_OUT", nil},
	{"a body holding the quote", "shop", func(c *irv1.Catalog) {
		o := overload(c)
		o.Extensions = append(o.Extensions, ext(&pgextv1.RoutineSource{Language: "sql", Body: "$dql$"}))
	}, "a body holding $dql$", nil},
	{"a called-on-null function", "shop", func(c *irv1.Catalog) {
		overload(c).NullBehavior = irv1.NullBehavior_NULL_BEHAVIOR_CALLED_ON_NULL_INPUT
	}, "", []string{"LANGUAGE sql STABLE PARALLEL UNSAFE"}},
	{"alien routine extension", "shop", func(c *irv1.Catalog) {
		o := overload(c)
		o.Extensions = append(o.Extensions, alien)
	}, "no writer for extension", nil},
}...)

var mysqlCases = append(failures("mysqlshop", "users", "published_posts"), []writeCase{
	{"a length in bytes and a table comment", "mysqlshop", func(c *irv1.Catalog) {
		check(table(c, "posts"), "posts_title_check").GetPortable().GetLength().Unit = irv1.LengthUnit_LENGTH_UNIT_BYTES
		t := table(c, "users")
		t.Extensions = append(t.Extensions, ext(&mysqlextv1.Comment{Text: `a\b`}))
	}, "", []string{"CHECK (length(`title`) BETWEEN 1 AND 200)", `COMMENT='a\\b'`}},
	{"a length in UTF-16 code units", "mysqlshop", func(c *irv1.Catalog) {
		check(table(c, "posts"), "posts_title_check").GetPortable().GetLength().Unit = irv1.LengthUnit_LENGTH_UNIT_UTF16_CODE_UNITS
	}, "no MySQL check", nil},
	{"create options", "mysqlshop", func(c *irv1.Catalog) {
		t := table(c, "users")
		t.Extensions = append(t.Extensions, ext(&mysqlextv1.TableOptions{CreateOptions: "row_format=COMPACT"}))
	}, "no writer for options row_format=COMPACT", nil},
	{"a virtual column", "mysqlshop", func(c *irv1.Catalog) {
		column(table(c, "posts"), "slug").GetGeneration().Kind = irv1.GenerationKind_GENERATION_VIRTUAL
	}, "", []string{") VIRTUAL"}},
	{"an always identity", "mysqlshop", func(c *irv1.Catalog) {
		column(table(c, "users"), "id").GetDefault().Class = irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS
	}, "no writer for default DEFAULT_IDENTITY_ALWAYS", nil},
	{"an index part of an expression", "mysqlshop", func(c *irv1.Catalog) {
		t := table(c, "posts")
		t.Extensions = append(t.Extensions, ext(&mysqlextv1.Index{Name: "x", Type: "BTREE", Visible: true, Parts: []*mysqlextv1.IndexPart{{Expression: "1"}}}))
	}, "no writer for index part", nil},
	{"an invisible index", "mysqlshop", func(c *irv1.Catalog) {
		t := table(c, "posts")
		t.Extensions = append(t.Extensions, ext(&mysqlextv1.Index{Name: "x", Type: "BTREE"}))
	}, "no writer for index", nil},
	{"a view with a check option", "mysqlshop", func(c *irv1.Catalog) {
		t := table(c, "published_posts")
		t.Extensions = []*anypb.Any{ext(&mysqlextv1.ViewDefinition{Definition: "select 1", CheckOption: "CASCADED"})}
	}, "no writer for ALGORITHM or CHECK OPTION", nil},
	{"procedure", "mysqlshop", func(c *irv1.Catalog) { overload(c).Kind = irv1.RoutineKind_ROUTINE_KIND_PROCEDURE }, "no writer for a routine", nil},
	{"not deterministic", "mysqlshop", func(c *irv1.Catalog) {
		o := overload(c)
		o.Extensions = []*anypb.Any{ext(&mysqlextv1.RoutineProperties{DataAccess: "NO SQL", SecurityType: "INVOKER", Definition: "RETURN 1"})}
	}, "", []string{"RETURNS decimal(65,30) NOT DETERMINISTIC NO SQL SQL SECURITY INVOKER RETURN 1"}},
	{"alien routine extension", "mysqlshop", func(c *irv1.Catalog) {
		o := overload(c)
		o.Extensions = append(o.Extensions, alien)
	}, "no writer for extension", nil},
}...)

var sqliteCases = []writeCase{
	{"alien catalog extension", "sqliteshop", func(c *irv1.Catalog) { c.Extensions = append(c.Extensions, alien) }, "no writer for extension", nil},
	{"a created index, a table comment", "sqliteshop", func(c *irv1.Catalog) {
		t := table(c, "posts")
		t.Extensions = append(t.Extensions, ext(&sqliteextv1.Index{Name: "x", Origin: "c", Sql: "CREATE INDEX x ON posts (title)"}),
			ext(&sqliteextv1.Comment{Text: "@x"}))
	}, "", []string{"CREATE INDEX x ON posts (title);"}},
	{"alien table extension", "sqliteshop", func(c *irv1.Catalog) { t := table(c, "posts"); t.Extensions = append(t.Extensions, alien) }, "no writer for extension", nil},
	{"no definition", "sqliteshop", func(c *irv1.Catalog) { table(c, "posts").Extensions = nil }, "no definition", nil},
	{"alien key extension", "sqliteshop", func(c *irv1.Catalog) {
		u := key(table(c, "tags"), "tags_name_key")
		u.Extensions = append(u.Extensions, alien)
	}, "no writer for extension", nil},
	{"alien column extension", "sqliteshop", func(c *irv1.Catalog) {
		col := column(table(c, "posts"), "id")
		col.Extensions = append(col.Extensions, alien)
	}, "no writer for extension", nil},
}

var sqlserverCases = append(failures("sqlservershop", "users", "published_posts"), []writeCase{
	{"a length in bytes, a table comment and an index", "sqlservershop", func(c *irv1.Catalog) {
		check(table(c, "posts"), "posts_title_check").GetPortable().GetLength().Unit = irv1.LengthUnit_LENGTH_UNIT_BYTES
		t := table(c, "users")
		t.Extensions = append(t.Extensions, ext(&sqlserverextv1.Comment{Text: "it's"}),
			ext(&sqlserverextv1.Index{Name: "x", Type: "NONCLUSTERED", Parts: []*sqlserverextv1.IndexPart{{Column: "email"}}}))
	}, "", []string{"CHECK (DATALENGTH([title]) BETWEEN 1 AND 200)", "N'it''s', N'SCHEMA', N'app', N'TABLE', N'users'",
		"CREATE NONCLUSTERED INDEX [x] ON [app].[users] ([email])"}},
	{"a length in characters", "sqlservershop", func(c *irv1.Catalog) {
		check(table(c, "posts"), "posts_title_check").GetPortable().GetLength().Unit = irv1.LengthUnit_LENGTH_UNIT_CHARACTERS
	}, "no SQL Server check", nil},
	{"a pattern", "sqlservershop", func(c *irv1.Catalog) {
		check(table(c, "posts"), "posts_title_check").Form = &irv1.Constraint_Portable{Portable: &irv1.PortableConstraint{
			Kind: &irv1.PortableConstraint_Pattern{Pattern: &irv1.PatternConstraint{Regex: "x"}}}}
	}, "no SQL Server check", nil},
	{"alien namespace extension", "sqlservershop", func(c *irv1.Catalog) {
		ns := c.GetNamespaces()[0]
		ns.Extensions = append(ns.Extensions, alien)
	}, "no writer for extension", nil},
	{"a key with no index", "sqlservershop", func(c *irv1.Catalog) {
		key(table(c, "users"), "users_pkey").Extensions = nil
	}, "uniqueness users_pkey: no index", nil},
	{"an index with included columns", "sqlservershop", func(c *irv1.Catalog) {
		u := key(table(c, "posts"), "posts_one_published_slug")
		u.Extensions = []*anypb.Any{ext(&sqlserverextv1.Index{Name: "posts_one_published_slug", Included: []string{"title"}})}
	}, "no writer for INCLUDE", nil},
	{"a descending part", "sqlservershop", func(c *irv1.Catalog) {
		u := key(table(c, "posts"), "posts_pkey")
		u.Extensions = []*anypb.Any{ext(&sqlserverextv1.Index{Name: "posts_pkey", Parts: []*sqlserverextv1.IndexPart{{Column: "id", Descending: true}}})}
	}, "no writer for a descending part", nil},
	{"a view text with no final newline", "sqlservershop", func(c *irv1.Catalog) {
		table(c, "published_posts").Extensions = []*anypb.Any{ext(&sqlserverextv1.ViewDefinition{Definition: "CREATE VIEW app.v AS SELECT 1"})}
	}, "", []string{"\nCREATE VIEW app.v AS SELECT 1\nGO"}},
	{"a procedure's comment", "sqlservershop", func(c *irv1.Catalog) {
		overload(c).Kind = irv1.RoutineKind_ROUTINE_KIND_PROCEDURE
	}, "", []string{"N'PROCEDURE', N'post_score'"}},
	{"routine with no definition", "sqlservershop", func(c *irv1.Catalog) { overload(c).Extensions = nil }, "no definition", nil},
	{"alien routine extension", "sqlservershop", func(c *irv1.Catalog) {
		o := overload(c)
		o.Extensions = append(o.Extensions, alien)
	}, "no writer for extension", nil},
}...)
