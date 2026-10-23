package annotate

import (
	"strings"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func column(typ *irv1.Type, notNull bool) Target {
	return Target{Kind: KindColumn, Name: q("users", "c"), Type: typ, NotNull: notNull}
}

var (
	viewTarget = Target{Kind: KindView, Name: q("published_posts", ""),
		Columns: []Column{{Name: "id"}, {Name: "author_id", Nullable: true}}}
	tableTarget = Target{Kind: KindTable, Name: q("users", ""), Words: []string{"users"}, PrimaryKey: true}
	fkTarget    = Target{Kind: KindForeignKey, Name: q("posts", "posts_author_id_fkey"), NotNull: true}
	checkTarget = Target{Kind: KindCheck, Name: q("orders", "orders_status_check"), Membership: true}
	viewColumn  = Target{Kind: KindViewColumn, Name: q("published_posts", "title"), Type: textType}
	attribute   = Target{Kind: KindAttribute, Name: q("money_amount", "amount"), Type: textType}
	result      = Target{Kind: KindResult, Name: q("post_score", "")}
	function    = Target{Kind: KindFunction, Name: q("post_score", "")}
	procedure   = Target{Kind: KindProcedure, Name: q("archive_posts", "")}
)

type checkCase struct {
	target Target
	ann    *irv1.Annotation
	want   []string
}

// M1 gate (implementation guide §4): every key of the core vocabulary has a
// passing case and a failing case.
func TestVocabularyGate(t *testing.T) {
	relation := strct(t, map[string]any{"to": "shop.users.id", "name": "user", "inverse-name": "invoices"})
	gate := map[string][2]checkCase{
		"array-elements-nonnull": {
			{column(arrayType(true), false), comment("core.array-elements-nonnull", nil), nil},
			{column(textType, false), comment("core.array-elements-nonnull", nil), []string{CodeWrongTarget}},
		},
		"as": {
			{column(intervalT, true), config("core.as", str("local_time")), nil},
			{viewColumn, config("core.as", str("local_time")), []string{CodeWrongTarget}},
		},
		"bool": {
			{Target{Kind: KindColumn, Name: q("flags", "on"), Type: int8Type, TinyIntOne: true}, config("core.bool", nil), nil},
			{column(int8Type, true), config("core.bool", nil), []string{CodeWrongTarget}},
		},
		"bound": {
			{column(int32Type, true), comment("core.bound", nil), nil},
			{tableTarget, comment("core.bound", nil), []string{CodeWrongTarget}},
		},
		"collection-policy": {
			{fkTarget, config("core.collection-policy", str("replace")), nil},
			{column(int32Type, true), config("core.collection-policy", str("replace")), []string{CodeWrongTarget}},
		},
		"db-filled": {
			{column(textType, true), comment("core.db-filled", nil), nil},
			{viewColumn, comment("core.db-filled", nil), []string{CodeWrongTarget}},
		},
		"db-updated": {
			{column(instant, true), comment("core.db-updated", nil), nil},
			{attribute, comment("core.db-updated", nil), []string{CodeWrongTarget}},
		},
		"enum": {
			{checkTarget, config("core.enum", str("order_status")), nil},
			{Target{Kind: KindCheck, Name: q("posts", "posts_published_check")}, config("core.enum", str("x")),
				[]string{CodeEnumNotMembership}},
		},
		"immutable": {
			{column(instant, true), comment("core.immutable", nil), nil},
			{fkTarget, comment("core.immutable", nil), []string{CodeWrongTarget}},
		},
		"inverse-name": {
			{fkTarget, config("core.inverse-name", words("profile")), nil},
			{column(int32Type, true), config("core.inverse-name", words("profile")), []string{CodeWrongTarget}},
		},
		"logical-key": {
			{viewTarget, comment("core.logical-key", list("id")), nil},
			{tableTarget, comment("core.logical-key", list("id")), []string{CodeWrongTarget}},
		},
		"name": {
			{tableTarget, config("core.name", words("user")), nil},
			{result, config("core.name", words("score")), []string{CodeWrongTarget}},
		},
		"nonnull": {
			{viewColumn, comment("core.nonnull", nil), nil},
			{fkTarget, comment("core.nonnull", nil), []string{CodeWrongTarget}},
		},
		"public": {
			{function, comment("core.public", nil), nil},
			{tableTarget, comment("core.public", nil), []string{CodeWrongTarget}},
		},
		"read-only": {
			{column(textType, true), comment("core.read-only", nil), nil},
			{procedure, comment("core.read-only", nil), []string{CodeWrongTarget}},
		},
		"relation": {
			{column(int32Type, true), config("core.relation", relation), nil},
			{fkTarget, config("core.relation", relation), []string{CodeWrongTarget}},
		},
		"relation-name": {
			{fkTarget, config("core.relation-name", words("writer")), nil},
			{checkTarget, config("core.relation-name", words("writer")), []string{CodeWrongTarget}},
		},
		"removal": {
			{fkTarget, config("core.removal", str("delete")), nil},
			{viewTarget, config("core.removal", str("delete")), []string{CodeWrongTarget}},
		},
		"sensitive": {
			{column(textType, true), comment("core.sensitive", nil), nil},
			{viewColumn, comment("core.sensitive", nil), []string{CodeWrongTarget}},
		},
		"soft-delete": {
			{column(instant, false), config("core.soft-delete", nil), nil},
			{column(textType, false), config("core.soft-delete", nil), []string{CodeWrongTarget}},
		},
		"version": {
			{column(int32Type, true), comment("core.version", nil), nil},
			// §7.3: version on a text column.
			{column(textType, true), comment("core.version", nil), []string{CodeWrongTarget}},
		},
		"write-only": {
			{column(textType, true), comment("core.write-only", nil), nil},
			{result, comment("core.write-only", nil), []string{CodeWrongTarget}},
		},
	}
	for _, k := range Vocabulary() {
		cases, ok := gate[k.Name]
		if !ok {
			t.Errorf("%s has no gate cases", k.Name)
			continue
		}
		for i, c := range cases {
			t.Run(k.Name+[]string{"/pass", "/fail"}[i], func(t *testing.T) {
				wantCodes(t, Check(c.target, []*irv1.Annotation{c.ann}), c.want...)
			})
		}
	}
	if len(gate) != len(Vocabulary()) {
		t.Errorf("gate has %d keys, vocabulary %d", len(gate), len(Vocabulary()))
	}
}

func run(t *testing.T, cases map[string]checkCase) {
	t.Helper()
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			wantCodes(t, Check(c.target, []*irv1.Annotation{c.ann}), c.want...)
		})
	}
}

// §7.3: unknown core keys fail; plugin keys pass through untouched.
func TestKeys(t *testing.T) {
	col := column(textType, true)
	run(t, map[string]checkCase{
		"unknown core key":     {col, comment("core.write-once", nil), []string{CodeUnknown}},
		"plugin key":           {col, config("x.ts.time-type", str("date")), nil},
		"plugin key, any form": {col, config("x.go.type", words("big", "int")), nil},
		"plugin key, no key":   {col, config("x.go", nil), []string{CodeKeyInvalid}},
		"plugin key, no name":  {col, config("x..type", nil), []string{CodeKeyInvalid}},
		"no prefix":            {col, config("write-only", nil), []string{CodeKeyInvalid}},
	})
}

func TestValueForms(t *testing.T) {
	col := column(int32Type, true)
	run(t, map[string]checkCase{
		"none with a value":         {col, comment("core.immutable", str("yes")), []string{CodeValueInvalid}},
		"none as an empty value":    {col, comment("core.immutable", &irv1.AnnotationValue{}), nil},
		"string empty":              {checkTarget, config("core.enum", str("")), []string{CodeValueInvalid}},
		"string as words":           {checkTarget, config("core.enum", words("order", "status")), []string{CodeValueInvalid}},
		"choice outside the set":    {fkTarget, config("core.collection-policy", str("sideways")), []string{CodeValueInvalid}},
		"removal outside the set":   {fkTarget, config("core.removal", str("cascade")), []string{CodeValueInvalid}},
		"as an unknown type":        {col, config("core.as", str("integer")), []string{CodeValueInvalid}},
		"as a structured type":      {col, config("core.as", str("array")), []string{CodeValueInvalid}},
		"words empty":               {tableTarget, config("core.name", words()), []string{CodeValueInvalid}},
		"words with an empty word":  {tableTarget, config("core.name", words("user", "")), []string{CodeValueInvalid}},
		"words as a string":         {tableTarget, config("core.name", str("user")), []string{CodeValueInvalid}},
		"list empty":                {viewTarget, comment("core.logical-key", list()), []string{CodeValueInvalid}},
		"list with duplicates":      {viewTarget, comment("core.logical-key", list("id", "id")), []string{CodeValueInvalid}},
		"logical-key unknown":       {viewTarget, comment("core.logical-key", list("uuid")), []string{CodeValueInvalid}},
		"struct missing":            {col, config("core.relation", str("shop.users.id")), []string{CodeValueInvalid}},
		"struct without to":         {col, config("core.relation", strct(t, map[string]any{"name": "user"})), []string{CodeValueInvalid}},
		"struct with an empty to":   {col, config("core.relation", strct(t, map[string]any{"to": ""})), []string{CodeValueInvalid}},
		"struct with a list to":     {col, config("core.relation", strct(t, map[string]any{"to": []any{"a"}})), []string{CodeValueInvalid}},
		"struct with to only":       {col, config("core.relation", strct(t, map[string]any{"to": "shop.users.id"})), nil},
		"struct with a word list":   {col, config("core.relation", strct(t, map[string]any{"to": "shop.users.id", "name": []any{"author"}})), nil},
		"struct with a number name": {col, config("core.relation", strct(t, map[string]any{"to": "shop.users.id", "name": 1})), []string{CodeValueInvalid}},
		"struct with an extra field": {col, config("core.relation", strct(t, map[string]any{"to": "shop.users.id", "kind": "one"})),
			[]string{CodeValueInvalid}},
	})
}

// §7.3 and the target rules beyond the kind that §7.2, §8.4, §11.1, §14.6
// and §21.3 give.
func TestTargetRules(t *testing.T) {
	tinyint := Target{Kind: KindColumn, Name: q("flags", "deleted"), Type: int8Type, TinyIntOne: true}
	run(t, map[string]checkCase{
		"version on an unsigned integer": {column(&irv1.Type{Kind: &irv1.Type_Uint64{Uint64: &irv1.Empty{}}}, true),
			comment("core.version", nil), nil},
		"version on an untyped column": {column(nil, true), comment("core.version", nil), []string{CodeWrongTarget}},
		"enum on an exclusion": {Target{Kind: KindExclusion, Name: q("rooms", "no_overlap")},
			config("core.enum", str("x")), []string{CodeWrongTarget}},
		"logical-key on a table without a key": {Target{Kind: KindTable, Name: q("events", ""), Columns: []Column{{Name: "id"}}},
			comment("core.logical-key", list("id")), nil},
		// §7.3: logical-key over nullable columns.
		"logical-key over a nullable column": {viewTarget, comment("core.logical-key", list("id", "author_id")),
			[]string{CodeLogicalKeyNullable}},
		"soft-delete on a NOT NULL timestamp": {column(instant, true), config("core.soft-delete", nil), []string{CodeWrongTarget}},
		"soft-delete on a boolean":            {column(boolType, true), config("core.soft-delete", nil), nil},
		"soft-delete on a TINYINT(1) alone":   {tinyint, config("core.soft-delete", nil), []string{CodeWrongTarget}},
		"unlink on a NOT NULL foreign key":    {fkTarget, config("core.removal", str("unlink")), []string{CodeWrongTarget}},
		"unlink on a nullable foreign key": {Target{Kind: KindForeignKey, Name: q("posts", "posts_editor_id_fkey")},
			config("core.removal", str("unlink")), nil},
		"name on an overload": {function, config("core.name", words("score")), nil},
	})

	// soft-delete on a TINYINT(1) annotated bool is a boolean (§8.7, §21.3).
	wantCodes(t, Check(tinyint, []*irv1.Annotation{config("core.bool", nil), config("core.soft-delete", nil)}))
}

// §7.3: bound together with version.
func TestBoundWithVersion(t *testing.T) {
	col := column(int32Type, true)
	wantCodes(t, Check(col, []*irv1.Annotation{comment("core.bound", nil), comment("core.version", nil)}),
		CodeBoundWithVersion)
	wantCodes(t, Check(col, []*irv1.Annotation{comment("core.bound", nil), comment("core.sensitive", nil)}))
}

// §7.3: a redundant annotation is a warning.
func TestRedundant(t *testing.T) {
	generated := column(textType, false)
	generated.Generated = true
	onUpdate := column(instant, true)
	onUpdate.OnUpdate = true
	run(t, map[string]checkCase{
		"nonnull on a NOT NULL column":       {column(textType, true), comment("core.nonnull", nil), []string{CodeRedundant}},
		"nonnull on a proven result":         {Target{Kind: KindResult, Name: q("f", ""), NotNull: true}, comment("core.nonnull", nil), []string{CodeRedundant}},
		"array-elements-nonnull, non-null":   {column(arrayType(false), false), comment("core.array-elements-nonnull", nil), []string{CodeRedundant}},
		"name equal to the words":            {tableTarget, config("core.name", words("users")), []string{CodeRedundant}},
		"name without words":                 {Target{Kind: KindTable, Name: q("t", "")}, config("core.name", words("t")), nil},
		"collection-policy merge":            {fkTarget, config("core.collection-policy", str("merge")), []string{CodeRedundant}},
		"as its own type":                    {column(timeType, true), config("core.as", str("local_time")), []string{CodeRedundant}},
		"db-updated detected":                {onUpdate, comment("core.db-updated", nil), []string{CodeRedundant}},
		"read-only on a generated column":    {generated, comment("core.read-only", nil), []string{CodeRedundant}},
		"immutable on a generated column":    {generated, comment("core.immutable", nil), []string{CodeRedundant}},
		"sensitive on a generated column":    {generated, comment("core.sensitive", nil), nil},
		"write-only on a NOT NULL column":    {column(textType, true), comment("core.write-only", nil), nil},
		"collection-policy append is chosen": {fkTarget, config("core.collection-policy", str("append")), nil},
	})

	// §14.2: a forbidden result from any rule wins, so these add nothing.
	col := column(int32Type, true)
	pairs := []struct{ redundant, by string }{
		{"immutable", "read-only"},
		{"immutable", "version"},
		{"db-updated", "bound"},
		{"db-filled", "read-only"},
		{"db-filled", "version"},
		{"read-only", "bound"},
		{"read-only", "soft-delete"},
	}
	for _, p := range pairs {
		target := col
		if p.by == "soft-delete" {
			target = column(instant, false)
		}
		ds := Check(target, []*irv1.Annotation{comment(CorePrefix+p.by, nil), comment(CorePrefix+p.redundant, nil)})
		wantCodes(t, ds, CodeRedundant)
		if !strings.Contains(ds[0].GetMessage(), CorePrefix+p.redundant+" on") {
			t.Errorf("%s next to %s: message %q", p.redundant, p.by, ds[0].GetMessage())
		}
	}
	// Neither of immutable and db-updated subsumes the other: they mean
	// different things.
	wantCodes(t, Check(col, []*irv1.Annotation{comment("core.immutable", nil), comment("core.db-updated", nil)}))
}

func TestMessagesNameTheTargetAndTheFix(t *testing.T) {
	ds := Check(column(textType, true), []*irv1.Annotation{comment("core.version", nil)})
	want := "core.version applies to an integer column, and shop.app.users.c is a text column; " +
		"remove the annotation or move it to an integer column (§14.2)"
	if got := ds[0].GetMessage(); got != want {
		t.Errorf("message = %q\nwant      %q", got, want)
	}
	if got := display(ds[0].GetTarget()); got != "shop.app.users.c" {
		t.Errorf("target = %s", got)
	}
	ds = Check(column(nil, true), []*irv1.Annotation{comment("core.version", nil)})
	if !strings.Contains(ds[0].GetMessage(), "is an untyped column") {
		t.Errorf("message = %q", ds[0].GetMessage())
	}
}

func TestCheckSortsDiagnostics(t *testing.T) {
	col := column(textType, true)
	ds := Check(col, []*irv1.Annotation{
		comment("core.nonnull", nil),
		comment("core.zzz", nil),
		comment("core.bound", nil),
		comment("core.version", nil),
		comment("core.aaa", nil),
	})
	wantCodes(t, ds, CodeUnknown, CodeUnknown, CodeWrongTarget, CodeBoundWithVersion, CodeRedundant)
	if ds[0].GetMessage() > ds[1].GetMessage() {
		t.Error("equal codes are not ordered by message")
	}
}

func TestSortDiagnostics(t *testing.T) {
	d := func(sev irv1.Severity, object, code string) *irv1.Diagnostic {
		return &irv1.Diagnostic{Severity: sev, Target: q(object, ""), Code: code}
	}
	warn, fail := irv1.Severity_SEVERITY_WARNING, irv1.Severity_SEVERITY_ERROR
	ds := []*irv1.Diagnostic{
		d(warn, "a", "x"),
		d(fail, "b", "y"),
		d(fail, "b", "x"),
		d(fail, "a", "z"),
		{Severity: fail, Target: &irv1.QualifiedName{Catalog: "billing"}, Code: "z"},
		{Severity: fail, Target: &irv1.QualifiedName{Catalog: "shop", Namespace: "a"}, Code: "z"},
		{Severity: fail, Target: q("a", "m"), Code: "a"},
	}
	SortDiagnostics(ds)
	var got []string
	for _, x := range ds {
		got = append(got, display(x.GetTarget())+"/"+x.GetCode())
	}
	want := "billing/z shop.a/z shop.app.a/z shop.app.a.m/a shop.app.b/x shop.app.b/y shop.app.a/x"
	if strings.Join(got, " ") != want {
		t.Errorf("order = %s\nwant    %s", strings.Join(got, " "), want)
	}
}

// The annotations of the running example (§1.3) check clean.
func TestRunningExample(t *testing.T) {
	users := func(member string, typ *irv1.Type) Target {
		return Target{Kind: KindColumn, Name: q("users", member), Type: typ, NotNull: true}
	}
	uuid := &irv1.Type{Kind: &irv1.Type_Uuid{Uuid: &irv1.Empty{}}}
	targets := []struct {
		target Target
		anns   []*irv1.Annotation
	}{
		{users("tenant_id", uuid), []*irv1.Annotation{comment("core.bound", nil)}},
		{users("password_hash", textType), []*irv1.Annotation{comment("core.sensitive", nil), comment("core.write-only", nil)}},
		{users("created_at", instant), []*irv1.Annotation{comment("core.immutable", nil)}},
		{users("updated_at", instant), []*irv1.Annotation{comment("core.db-updated", nil)}},
		{users("version", int32Type), []*irv1.Annotation{comment("core.version", nil)}},
		{viewTarget, []*irv1.Annotation{comment("core.logical-key", list("id")), config("core.name", words("published", "post"))}},
		{Target{Kind: KindViewColumn, Name: q("published_posts", "id")}, []*irv1.Annotation{comment("core.nonnull", nil)}},
		{viewColumn, []*irv1.Annotation{comment("core.nonnull", nil)}},
		{function, []*irv1.Annotation{comment("core.public", nil)}},
		{tableTarget, []*irv1.Annotation{config("core.name", words("user"))}},
		{Target{Kind: KindForeignKey, Name: q("profiles", "profiles_user_id_fkey"), NotNull: true},
			[]*irv1.Annotation{config("core.inverse-name", words("profile"))}},
		{fkTarget, []*irv1.Annotation{config("core.collection-policy", str("replace")), config("core.removal", str("delete"))}},
		{Target{Kind: KindAttribute, Name: q("money_amount", "amount")}, []*irv1.Annotation{config("core.nonnull", nil)}},
	}
	for _, x := range targets {
		wantCodes(t, Check(x.target, x.anns))
	}
}
