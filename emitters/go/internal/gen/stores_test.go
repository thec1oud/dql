package gen

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// query returns the bundle's query of the given name.
func query(t *testing.T, b *irv1.Bundle, name string) *irv1.Query {
	t.Helper()
	for _, q := range b.GetQueries().GetQueries() {
		if q.GetName().GetStored() == name {
			return q
		}
	}
	t.Fatalf("no query %s", name)
	return nil
}

// contains checks that file holds every part, comparing runs of spaces and
// tabs as one space, so gofmt's alignment does not matter.
func contains(t *testing.T, files map[string]string, file string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(squash(files[file]), squash(p)) {
			t.Errorf("%s lacks %q", file, p)
		}
	}
}

var blanks = regexp.MustCompile(`[ \t]+`)

func squash(s string) string { return blanks.ReplaceAllString(s, " ") }

// §16.2, §22.5, §23: the dialect file follows the capability profile.
func TestDialectFile(t *testing.T) {
	b := shopShapes(t)
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "dql-dialect.dql.go",
		`Name: "postgres",`,
		`"23505": dql.ClassUniqueViolation,`,
		`{Catalog: "shop", Schema: "app", Object: "users", Class: dql.ClassUniqueViolation, Name: "users_pkey"}: {Name: "users_pkey", Kind: dql.ConstraintPrimaryKey},`,
		`{Catalog: "shop", Schema: "app", Object: "users", Class: dql.ClassNotNullViolation, Name: "email"}: {Name: "email", Kind: dql.ConstraintNotNull, Sensitive: true},`,
		"Replication: &dql.Replication{\n\t\tPosition: \"SELECT pg_current_wal_lsn()::text\",",
		"MaxParams: 65535,",
		`var Catalogs = []string{"shop"}`,
	)
	contains(t, files, "dql-fragments.dql.go", "Placeholder: dql.DollarNumbered,", "\"col.users.email\": `u.\"email\"`,")

	// A dialect with no replicas, limits, setup or rules, other
	// placeholders, and templates a raw string cannot hold.
	p := b.GetCapabilities()
	p.ConsistencyLevels = []irv1.ConsistencyLevel{irv1.ConsistencyLevel_CONSISTENCY_PRIMARY}
	p.MaxParams, p.SessionSetup, p.Violations = 0, nil, nil
	p.ErrorCodes = append(p.ErrorCodes, &irv1.ErrorCode{Code: "XX000"})
	t2 := b.GetQueries().GetFragments()
	t2.Placeholder = irv1.Placeholder_PLACEHOLDER_QUESTION
	t2.Fragments = append(t2.Fragments, &irv1.Fragment{Id: "zz.backquote", Template: "a`b"}, &irv1.Fragment{Id: "zz.return", Template: "a\rb"})
	files, codes = generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	d := files["dql-dialect.dql.go"]
	for _, absent := range []string{"Replication", "MaxParams", "Setup", "Violations", "XX000"} {
		if strings.Contains(d, absent) {
			t.Errorf("dialect has %s:\n%s", absent, d)
		}
	}
	contains(t, files, "dql-fragments.dql.go", "Placeholder: dql.Question,", `"zz.backquote": "a`+"`"+`b",`, `"zz.return": "a\rb",`)

	// Rules whose kind names no constraint are left out; so is a placeholder
	// style the runtime lacks.
	p.Violations = []*irv1.ViolationReport{{Kind: irv1.ErrorKind_ERROR_KIND_DEADLOCK, Object: qnShop("users"), Reported: "x", Rule: "x"}}
	t2.Placeholder = irv1.Placeholder_PLACEHOLDER_UNSPECIFIED
	files, _ = generate(t, b, Options{})
	if strings.Contains(files["dql-dialect.dql.go"], `Name: "x"`) || strings.Contains(files["dql-fragments.dql.go"], "Placeholder:") {
		t.Error("unknown rule or placeholder kept")
	}

	// Without a profile or a fragment table there is no query surface.
	b.Capabilities, b.Queries = nil, nil
	files, _ = generate(t, b, Options{})
	for _, name := range []string{"dql-dialect.dql.go", "dql-fragments.dql.go", "dql-db.dql.go"} {
		if _, ok := files[name]; ok {
			t.Errorf("%s generated", name)
		}
	}
}

func qnShop(object string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: object}
}

// §22.5: a query's default consistency, the primary at a level the dialect
// lacks.
func TestQueryConsistency(t *testing.T) {
	b := shopShapes(t)
	set := func(name string, c *irv1.Consistency) { query(t, b, name).Consistency = c }
	set("tag.count", &irv1.Consistency{Level: irv1.ConsistencyLevel_CONSISTENCY_ANY})
	set("tag.exists", &irv1.Consistency{Level: irv1.ConsistencyLevel_CONSISTENCY_BOUNDED_STALENESS, MaxStaleness: durationpb.New(2 * time.Second)})
	set("tag.get.by_name", &irv1.Consistency{Level: irv1.ConsistencyLevel_CONSISTENCY_READ_YOUR_WRITES})
	set("tag.find.by_name", &irv1.Consistency{Level: irv1.ConsistencyLevel_CONSISTENCY_PRIMARY})
	b.GetCapabilities().ConsistencyLevels = slices.DeleteFunc(b.GetCapabilities().GetConsistencyLevels(), func(l irv1.ConsistencyLevel) bool {
		return l == irv1.ConsistencyLevel_CONSISTENCY_READ_YOUR_WRITES
	})
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "tag.dql.go",
		"Consistency: dql.Any,",
		"Consistency: dql.Bounded(2000 * time.Millisecond),",
		"Name:        \"tag.get.by_name\",\n\t\tCatalog:     \"shop\",\n\t\tKind:        \"get\",\n\t\tAtMostOne:   true,\n\t\tConsistency: dql.Primary,",
		"Name:        \"tag.find.by_name\",\n\t\tCatalog:     \"shop\",\n\t\tKind:        \"find\",\n\t\tAtMostOne:   true,\n\t\tConsistency: dql.Primary,",
	)
	// A level this emitter does not know reads from the primary.
	set("tag.count", &irv1.Consistency{Level: 99})
	b.GetCapabilities().ConsistencyLevels = append(b.GetCapabilities().ConsistencyLevels, irv1.ConsistencyLevel_CONSISTENCY_READ_YOUR_WRITES, 99)
	files, _ = generate(t, b, Options{})
	contains(t, files, "tag.dql.go", "Name:        \"tag.count\",\n\t\tCatalog:     \"shop\",\n\t\tKind:        \"count\",\n\t\tConsistency: dql.Primary,", "Consistency: dql.ReadYourWrites,")
}

// §16.3: builder queries the emitter does not know are left out with a
// warning; plans and parameters it cannot fill are errors.
func TestQueryFailures(t *testing.T) {
	b := shopShapes(t)
	qs := b.GetQueries()
	add := func(name string, tables ...*irv1.QualifiedName) *irv1.Query {
		q := &irv1.Query{Name: &irv1.Identifier{Stored: name}, Source: irv1.QuerySource_QUERY_SOURCE_BUILDER, Tables: tables}
		qs.Queries = append(qs.Queries, q)
		return q
	}
	add("tag.merge", qnShop("tags"))
	add("tag.get.by_name.extra", qnShop("tags"))
	add("tag.get.name", qnShop("tags"))
	add("post.count", qnShop("tags"))
	add("nothing.count", qnShop("nothing"))
	add("tag.untabled")
	qs.Queries = append(qs.Queries, &irv1.Query{Name: &irv1.Identifier{Stored: "GetTag"}, Source: irv1.QuerySource_QUERY_SOURCE_FILE})
	_, codes := generate(t, b, Options{})
	want := []string{
		"SEVERITY_WARNING go-query-unsupported shop.app.nothing",
		"SEVERITY_WARNING go-query-unsupported shop.app.tags",
		"SEVERITY_WARNING go-query-unsupported shop.app.tags",
		"SEVERITY_WARNING go-query-unsupported shop.app.tags",
		"SEVERITY_WARNING go-query-unsupported shop.app.tags",
	}
	if !slices.Equal(codes, want) {
		t.Errorf("codes %q", codes)
	}

	b = shopShapes(t)
	q := query(t, b, "tag.count")
	q.GetPlan().GetParts()[0].Fragment = "nope"
	q = query(t, b, "tag.get.by_name")
	q.Params = append(q.Params, &irv1.Param{Name: "stray"})
	q = query(t, b, "tag.exists")
	q.Scope = &irv1.ScopeUse{Columns: []string{"tenant_id"}}
	_, codes = generate(t, b, Options{})
	want = []string{
		"SEVERITY_ERROR go-fragment-unknown shop.app.tags",
		"SEVERITY_ERROR go-reference-unknown shop.app.tags",
		"SEVERITY_ERROR go-reference-unknown shop.app.tags",
	}
	if !slices.Equal(codes, want) {
		t.Errorf("codes %q", codes)
	}
}

// §20.4: without a key lookup, a versioned write cannot tell NotFound from
// ConcurrentModification, so it runs as an unversioned one; without Get, an
// empty Partial is not short-cut.
func TestWritesWithoutLookups(t *testing.T) {
	b := shopShapes(t)
	b.GetQueries().Queries = slices.DeleteFunc(b.GetQueries().GetQueries(), func(q *irv1.Query) bool {
		n := q.GetName().GetStored()
		return n == "user.find.by_primary" || n == "profile.get.by_primary"
	})
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "user.dql.go", "return dql.OneOf(ctx, s.w, st, DecodeUserFull)\n}\n\n// Delete runs user.delete")
	contains(t, files, "profile.dql.go", "\targs := dql.Args{\"key.user_id\": k.UserID}\n\tif v, ok := p.Bio.Get(); ok {\n\t\targs[\"bio\"] = v\n\t}\n")
	if strings.Contains(files["profile.dql.go"], "set := false") {
		t.Error("empty Partial short-cut without Get")
	}
}

// §21.1: entities whose bound columns differ get scope types and handles of
// their own, entities with the same bound columns share one, and a handle
// covers the entities whose bound columns it holds.
func TestScopeGroups(t *testing.T) {
	b := shopShapes(t)
	scope := func(table string, words ...string) *irv1.Shape {
		return &irv1.Shape{
			Id:       &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: qnShop(table)}, Preset: irv1.Preset_PRESET_SCOPE},
			HostName: &irv1.Identifier{Words: words}, Entity: qnShop(table),
			Fields: []*irv1.Field{{Column: "id", Type: &irv1.Type{Kind: &irv1.Type_Int64{Int64: e()}}}},
		}
	}
	// Posts and tags are bound by id, an int64 unlike users' tenant_id.
	b.GetShapes().Shapes = append(b.GetShapes().GetShapes(), scope("posts", "post", "scope"), scope("tags", "tag", "scope"))
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "dql-db.dql.go",
		"func (db DB) WithPostScope(s PostScope) PostScoped",
		"func (db DB) WithUserScope(s UserScope) UserScoped",
		"Tags:           tagsWrites{tagsReads{exec, TagScope{ID: s.ID}}, exec},",
		"Posts:          postsUnscoped{postsUnscopedReads{exec}, exec},",
	)
	if strings.Contains(files["dql-db.dql.go"], "TagScoped") {
		t.Error("tags got a handle of their own")
	}
	db := files["dql-db.dql.go"]
	user := db[strings.Index(db, "type UserScoped struct"):]
	if user = user[:strings.Index(user, "}")]; strings.Contains(user, "\tPosts ") || !strings.Contains(user, "Users") {
		t.Errorf("UserScoped covers %s", user)
	}
}

// shopTable returns the shop bundle's table of the given name.
func shopTable(b *irv1.Bundle, name string) *irv1.Table {
	for _, ns := range b.GetCatalogs()[0].GetNamespaces() {
		for _, t := range ns.GetTables() {
			if t.GetName().GetStored() == name {
				return t
			}
		}
	}
	return nil
}

// §9.3, §9.4, §18.2: column handles get the operators their columns support
// and the fragment table holds; relations to tables without a store, to
// the table itself and without a fragment get no handle.
func TestTableHandles(t *testing.T) {
	b := shopShapes(t)
	frag := func(ids ...string) {
		for _, id := range ids {
			b.GetQueries().GetFragments().Fragments = append(b.GetQueries().GetFragments().GetFragments(), &irv1.Fragment{Id: id, Template: "x"})
		}
	}
	text := &irv1.OperandType{Kind: &irv1.OperandType_Type{Type: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}}}}
	tv := &irv1.OperandType{Kind: &irv1.OperandType_Var{Var: "T"}}
	rangeOf := &irv1.OperandType{Kind: &irv1.OperandType_Range{Range: tv}}
	arrayOf := &irv1.OperandType{Kind: &irv1.OperandType_Array{Array: tv}}
	nested := &irv1.OperandType{Kind: &irv1.OperandType_Array{Array: arrayOf}}
	boolean := &irv1.OperandType{Kind: &irv1.OperandType_Type{Type: &irv1.Type{Kind: &irv1.Type_Bool{Bool: e()}}}}
	op := func(name string, args ...*irv1.OperandType) {
		b.Operators = append(b.Operators, &irv1.Operator{Name: name, Signature: &irv1.Signature{Args: args, Returns: boolean}})
		frag("op." + name)
	}
	op("within", rangeOf, tv)
	op("meets", rangeOf, rangeOf)
	op("deep", tv, nested)
	op("approx", text, text)
	op("first_array", arrayOf, tv)
	frag("op.postgres.native")
	users := shopTable(b, "users")
	period := &irv1.Column{
		Name: &irv1.Identifier{Stored: "period"}, HostName: &irv1.Identifier{Words: []string{"period"}},
		Type: &irv1.Type{Kind: &irv1.Type_Range{Range: &irv1.RangeType{Element: &irv1.Type{Kind: &irv1.Type_Int64{Int64: e()}}}}},
		Operators: []*irv1.ColumnOperator{
			{Name: "within"}, {Name: "meets"}, {Name: "deep"}, {Name: "first_array"}, {Name: "postgres.native"}, {Name: "unknown"},
			{Name: "approx", Fidelity: irv1.Fidelity_FIDELITY_APPROXIMATE},
		},
	}
	// A column without a fragment, and one whose Go name another has.
	hidden := &irv1.Column{Name: &irv1.Identifier{Stored: "hidden"}, HostName: &irv1.Identifier{Words: []string{"hidden"}}, Type: period.GetType()}
	users.Columns = append(users.Columns, period, hidden)
	frag("col.users.period")
	rel := func(name, to string) *irv1.Relation {
		frag("rel.users." + name)
		return &irv1.Relation{Name: &irv1.Identifier{Stored: name, Words: []string{name}}, To: &irv1.ColumnSet{Table: qnShop(to)}}
	}
	users.Relations = append(users.Relations, rel("manager", "users"), rel("audit", "audits"),
		&irv1.Relation{Name: &irv1.Identifier{Stored: "unfragmented", Words: []string{"unfragmented"}}, To: &irv1.ColumnSet{Table: qnShop("posts")}})
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	contains(t, files, "user.dql.go",
		"func (usersPeriodColumn) Within(v int64) dql.Pred[UsersTable]",
		"func (usersPeriodColumn) Meets(v dql.Range[int64]) dql.Pred[UsersTable]",
		"// Approx is operator approx on period (§9.4).\n//\n// Approximate: its results can differ from the operator's (§9.2).\nfunc (usersPeriodColumn) Approx(v string)",
	)
	for _, absent := range []string{"Deep(", "FirstArray(", "Native(", "Unknown(", "usersHiddenColumn", "Manager", "Audit", "Unfragmented"} {
		if strings.Contains(files["user.dql.go"], absent) {
			t.Errorf("user.dql.go has %s", absent)
		}
	}

	// Field names that meet fail.
	users.Columns = append(users.Columns, &irv1.Column{Name: &irv1.Identifier{Stored: "Period"}, HostName: &irv1.Identifier{Words: []string{"period"}}, Type: period.GetType()})
	frag("col.users.Period")
	users.Relations = append(users.Relations, rel("email", "posts"))
	if _, codes := generate(t, b, Options{}); !slices.Equal(codes, []string{
		"SEVERITY_ERROR go-name-collision shop.app.users",
		"SEVERITY_ERROR go-name-collision shop.app.users",
	}) {
		t.Error(codes)
	}
}

// §18.2, §21.1: without select fragments there are no Shapes; a sensitive
// scope column is a sensitive argument.
func TestSelectShapes(t *testing.T) {
	b := shopShapes(t)
	b.GetQueries().GetFragments().Fragments = slices.DeleteFunc(b.GetQueries().GetFragments().GetFragments(), func(f *irv1.Fragment) bool {
		return strings.HasPrefix(f.GetId(), "select.") || strings.HasPrefix(f.GetId(), "lock.")
	})
	b.GetQueries().Queries = slices.DeleteFunc(b.GetQueries().GetQueries(), func(q *irv1.Query) bool {
		return !strings.Contains(q.GetName().GetStored(), ".count") && !strings.Contains(q.GetName().GetStored(), ".insert")
	})
	for _, s := range b.GetShapes().GetShapes() {
		if s.GetId().GetPreset() == irv1.Preset_PRESET_FULL && s.GetEntity().GetObject() == "users" {
			for _, f := range s.GetFields() {
				f.Redact = f.Redact || f.GetColumn() == "tenant_id"
			}
		}
	}
	files, codes := generate(t, b, Options{})
	if len(codes) != 0 {
		t.Fatal(codes)
	}
	if s := files["dql-select.dql.go"]; strings.Contains(s, "Shapes") || strings.Contains(s, "ForUpdate") || strings.Contains(s, "Distinct") {
		t.Error(s)
	}
	contains(t, files, "user.dql.go", `dql.Arg{Value: s.scope.TenantID, Sensitive: true}`)
}
