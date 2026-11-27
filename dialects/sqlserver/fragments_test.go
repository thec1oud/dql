package sqlserver_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thec1oud/dql/dialects/sqlserver"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func ident(stored string, words ...string) *irv1.Identifier {
	if len(words) == 0 {
		words = strings.Split(stored, "_")
	}
	return &irv1.Identifier{Stored: stored, Words: words}
}

func qn(ns, obj string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: "shop", Namespace: ns, Object: obj}
}

func native(raw string) *irv1.Type {
	return &irv1.Type{Kind: &irv1.Type_Int64{Int64: &irv1.Empty{}}, Native: &irv1.NativeType{Raw: raw}}
}

// col is a bigint column.
func col(name string) *irv1.Column {
	return &irv1.Column{Name: ident(name), Type: native("bigint")}
}

func fullShape(entity *irv1.QualifiedName, custom string, form irv1.Form, cols ...string) *irv1.Shape {
	id := &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: entity}, Preset: irv1.Preset_PRESET_FULL, Form: form}
	if custom != "" {
		id = &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: custom}}
	}
	s := &irv1.Shape{Id: id, Entity: entity, Lineage: []*irv1.LineageStep{{Op: irv1.ShapeOp_PRESET, Preset: irv1.Preset_PRESET_FULL}}}
	for _, c := range cols {
		s.Fields = append(s.Fields, &irv1.Field{Column: c})
	}
	return s
}

// A database shop with two schemas, app, its default, and billing, each
// holding a table people.
func fixture() *irv1.Bundle {
	people := qn("app", "people")
	pets := qn("app", "pets")
	links := qn("app", "people_pets")
	id := col("id")
	id.Default = &irv1.Default{Class: irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS}
	email := &irv1.Column{Name: ident("email"), Type: &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}, Native: &irv1.NativeType{Raw: "nvarchar(320)"}},
		Collation: &irv1.Collation{Name: "Latin1_General_100_BIN2"}}
	photo := &irv1.Column{Name: ident("photo"), Type: &irv1.Type{Kind: &irv1.Type_Bytes{Bytes: &irv1.Bytes{}}, Native: &irv1.NativeType{Raw: "varbinary(max)"}}, Nullable: true}
	rv := &irv1.Column{Name: ident("rv"), Type: &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}, Native: &irv1.NativeType{Raw: "timestamp"}}}
	slug := &irv1.Column{Name: ident("slug"), Type: native("bigint"), Generation: &irv1.Generation{Kind: irv1.GenerationKind_GENERATION_STORED, Native: "(abs([id]))"}}
	return &irv1.Bundle{
		Dialect: irv1.Dialect_DIALECT_SQLSERVER,
		Catalogs: []*irv1.Catalog{{
			Name: ident("shop"), DefaultNamespace: "app",
			Namespaces: []*irv1.Namespace{
				{
					Name: ident("app"),
					Tables: []*irv1.Table{
						{
							Name: ident("people"), HostName: ident("", "person"), Kind: irv1.TableKind_RELATION_KIND_TABLE,
							Columns: []*irv1.Column{id, col("manager_id"), email, photo, rv},
							Uniqueness: []*irv1.Uniqueness{
								{Name: "people_pkey", Columns: []string{"id"}},
								{Name: "people_email", Columns: []string{"email"}},
							},
							Relations: []*irv1.Relation{
								{Name: ident("manager"), To: &irv1.ColumnSet{Table: people, Columns: []string{"id"}}, From: &irv1.ColumnSet{Columns: []string{"manager_id"}}},
								{Name: ident("pets"), To: &irv1.ColumnSet{Table: pets, Columns: []string{"id"}}, From: &irv1.ColumnSet{Columns: []string{"id"}}, Through: links},
								{Name: ident("remote"), CrossCatalog: true, To: &irv1.ColumnSet{Table: &irv1.QualifiedName{Catalog: "other", Namespace: "dbo", Object: "people"}}},
							},
						},
						{
							Name: ident("people_pets"), Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: []*irv1.Column{col("person_id"), col("pet_id")},
							ForeignKeys: []*irv1.ForeignKey{
								{Name: ident("people_pets_person"), Columns: []string{"person_id"}, References: &irv1.ColumnSet{Table: people, Columns: []string{"id"}}},
								{Name: ident("people_pets_pet"), Columns: []string{"pet_id"}, References: &irv1.ColumnSet{Table: pets, Columns: []string{"id"}}},
							},
						},
						{
							Name: ident("pets"), Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: []*irv1.Column{col("id"), col("we]ird"), slug},
							Uniqueness: []*irv1.Uniqueness{
								{Name: "pets_pkey", Columns: []string{"id"}},
								{Name: "pets_live", Columns: []string{"we]ird", "id"}, Predicate: &irv1.NativeText{Native: "([id]>(0))"}},
								{Name: "pets_slug", Columns: []string{"slug"}},
							},
						},
						{Name: ident("pet_view"), Kind: irv1.TableKind_RELATION_KIND_VIEW, Columns: []*irv1.Column{col("id")}},
					},
				},
				{
					Name:   ident("billing"),
					Tables: []*irv1.Table{{Name: ident("people"), Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: []*irv1.Column{col("id")}}},
				},
			},
		}},
		Shapes: &irv1.ShapeLayer{
			Shapes: []*irv1.Shape{
				fullShape(people, "", irv1.Form_FORM_FLAT, "id", "email"),
				fullShape(people, "", irv1.Form_FORM_NESTED, "id", "email"),
				fullShape(people, "person_brief", irv1.Form_FORM_UNSPECIFIED, "email"),
				fullShape(pets, "", irv1.Form_FORM_FLAT, "we]ird"),
				{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: pets}, Preset: irv1.Preset_PRESET_DRAFT, Form: irv1.Form_FORM_FLAT}, Entity: pets,
					Lineage: []*irv1.LineageStep{{Op: irv1.ShapeOp_PRESET, Preset: irv1.Preset_PRESET_DRAFT}}},
				{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: pets}, Preset: irv1.Preset_PRESET_FULL, Form: irv1.Form_FORM_FLAT, ForRelation: "owner"}, Entity: pets,
					Lineage: []*irv1.LineageStep{{Op: irv1.ShapeOp_PRESET, Preset: irv1.Preset_PRESET_FULL}}},
				{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "lineless"}}, Entity: pets},
			},
			Upserts: []*irv1.Upsert{
				{Entity: people, ConflictTarget: "people_pkey"},
				{Entity: people, ConflictTarget: "people_email"},
				{Entity: pets, ConflictTarget: "pets_pkey"},
				{Entity: pets, ConflictTarget: "pets_live"},
				{Entity: pets, ConflictTarget: "pets_slug"},
			},
		},
	}
}

func table(t *testing.T, b *irv1.Bundle) map[string]*irv1.Fragment {
	t.Helper()
	res, err := sqlserver.New().Invoke(context.Background(), &irv1.Invoke{Bundle: b})
	if err != nil {
		t.Fatal(err)
	}
	ft := res.GetBundle().GetQueries().GetFragments()
	if ft.GetDialect() != irv1.Dialect_DIALECT_SQLSERVER || ft.GetPlaceholder() != irv1.Placeholder_PLACEHOLDER_AT_NUMBERED {
		t.Errorf("table %v", ft)
	}
	got := map[string]*irv1.Fragment{}
	var ids []string
	for _, f := range ft.GetFragments() {
		got[f.GetId()] = f
		ids = append(ids, f.GetId())
	}
	if !slices.IsSorted(ids) {
		t.Error("fragments are not sorted by id")
	}
	return got
}

func TestFragments(t *testing.T) {
	got := table(t, fixture())
	const peopleOut = "DECLARE @dql_out TABLE ([id] bigint NULL, [manager_id] bigint NULL, [email] nvarchar(320) NULL, [photo] varbinary(max) NULL, [rv] binary(8) NULL); "
	const petsOut = "DECLARE @dql_out TABLE ([id] bigint NULL, [we]]ird] bigint NULL, [slug] bigint NULL); "
	const petsMerge = "MERGE INTO [app].[pets] WITH (HOLDLOCK) AS [pets] USING (VALUES {rows}) AS [dql_new] ({cols}) ON [pets].[id] = [dql_new].[id]"
	const insertBranch = " WHEN NOT MATCHED THEN INSERT ({cols}) VALUES ({news}) OUTPUT INSERTED.* INTO @dql_out;{returning}"
	for id, want := range map[string]string{
		// Ids leave out the default schema. Tables are schema.name, each
		// aliased by its name, or schema_name where two share a name.
		"from.people":                "[app].[people] AS [app_people]",
		"from.billing.people":        "[billing].[people] AS [billing_people]",
		"from.pets":                  "[app].[pets] AS [pets]",
		"col.people.email":           "[app_people].[email]",
		"col.pets.we]ird":            "[pets].[we]]ird]",
		"count.pets":                 "SELECT COUNT_BIG(*) FROM [app].[pets] AS [pets]{where}",
		"exists.pets":                "SELECT CAST(CASE WHEN EXISTS (SELECT 1 FROM [app].[pets] AS [pets]{where}) THEN 1 ELSE 0 END AS bit)",
		"rel.people.manager":         "EXISTS (SELECT 1 FROM [app].[people] AS [people_r] WHERE [people_r].[id] = [app_people].[manager_id]{and})",
		"col_r.people.email":         "[people_r].[email]",
		"rel.people.pets":            "EXISTS (SELECT 1 FROM [app].[people_pets] AS [people_pets] JOIN [app].[pets] AS [pets] ON [pets].[id] = [people_pets].[pet_id] WHERE [people_pets].[person_id] = [app_people].[id]{and})",
		"select.person.full.flat":    "SELECT [app_people].[id], [app_people].[email] FROM [app].[people] AS [app_people]{locks}",
		"returning.person.full.flat": " SELECT [id], [email] FROM @dql_out;",
		"select.person_brief":        "SELECT [app_people].[email] FROM [app].[people] AS [app_people]{locks}",
		"insert.people":              peopleOut + "INSERT INTO [app].[people] ({cols}) OUTPUT INSERTED.* INTO @dql_out VALUES {rows};{returning}",
		"update.pets":                petsOut + "UPDATE [pets] SET {sets} OUTPUT INSERTED.* INTO @dql_out FROM [app].[pets] AS [pets]{where};{returning}",
		"delete.pets":                petsOut + "DELETE [pets] OUTPUT DELETED.* INTO @dql_out FROM [app].[pets] AS [pets]{where};{returning}",
		"set.pets.id":                "[id] = {0}",
		"name.pets.id":               "[id]",
		"excluded.pets.id":           "[dql_new].[id]",
		"upsert.pets.pets_pkey":      petsOut + petsMerge + " WHEN MATCHED THEN UPDATE SET {sets}" + insertBranch,
		"ignore.pets.pets_pkey":      petsOut + petsMerge + insertBranch,
		"upsert.pets.pets_live":      petsOut + "WITH [dql_target] AS (SELECT * FROM [app].[pets] WHERE ([id]>(0))) MERGE INTO [dql_target] WITH (HOLDLOCK) AS [pets] USING (VALUES {rows}) AS [dql_new] ({cols}) ON [pets].[we]]ird] = [dql_new].[we]]ird] AND [pets].[id] = [dql_new].[id] WHEN MATCHED THEN UPDATE SET {sets}" + insertBranch,
		"upsert.people.people_email": peopleOut + "MERGE INTO [app].[people] WITH (HOLDLOCK) AS [app_people] USING (VALUES {rows}) AS [dql_new] ({cols}) ON [app_people].[email] = [dql_new].[email] WHEN MATCHED THEN UPDATE SET {sets}" + insertBranch,
		// A list compared with a column reads as the column's type, in its
		// collation.
		"op.in.people.email": "{0} IN (SELECT [v] FROM OPENJSON({1}) WITH ([v] nvarchar(320) COLLATE Latin1_General_100_BIN2 '$'))",
		"op.any.pets.id":     "{0} IN (SELECT [v] FROM OPENJSON({1}) WITH ([v] bigint '$'))",
		"op.in":              "{0} IN (SELECT [value] FROM OPENJSON({1}))",
		"op.like":            `{0} LIKE {1} ESCAPE '\'`,
		"clause.page":        " OFFSET {offset} ROWS FETCH NEXT {limit} ROWS ONLY",
		"clause.offset":      " OFFSET {0} ROWS",
		"order.none":         " ORDER BY (SELECT NULL)",
		"lock.hints":         " WITH ({0})",
		"lock.update":        "UPDLOCK, ROWLOCK",
		"lock.share":         "HOLDLOCK, ROWLOCK",
		"select.distinct":    "SELECT DISTINCT {cols} FROM {from}{locks}",
		"value.true":         "1 = 1",
		"value.now":          "TODATETIMEOFFSET(SYSUTCDATETIME(), 0)",
	} {
		if got[id].GetTemplate() != want {
			t.Errorf("%s\n got %q\nwant %q", id, got[id].GetTemplate(), want)
		}
	}
	for _, id := range []string{
		"rel.people.remote",         // crosses catalogs
		"col_r.pets.id",             // pets has no self-relation
		"insert.pet_view",           // a view takes no writes
		"select.person.full.nested", // the Flat select serves it
		"select.pet.draft.flat",     // a write shape
		"select.pet.full.flat.for.owner",
		"select.lineless",
		"upsert.people.people_pkey", // an identity key
		"ignore.people.people_pkey",
		"upsert.pets.pets_slug", // a computed key
		"op.in.people.photo",    // OPENJSON reads no binary
		"op.in.people.rv",
		"clause.limit", // a page is one clause
		"keyset.after", // no row values
		"select.distinct_on",
	} {
		if got[id] != nil {
			t.Errorf("unexpected %s: %q", id, got[id].GetTemplate())
		}
	}
	if got["clause.where"].GetFidelity() != irv1.Fidelity_FIDELITY_UNSPECIFIED {
		t.Error("a clause has a fidelity")
	}
	for _, s := range sqlserver.Profile().GetOperators() {
		if f := got["op."+s.GetName()]; f == nil || f.GetFidelity() != s.GetFidelity() {
			t.Errorf("fragment for %s: %v", s.GetName(), f)
		}
	}
}

// An operator the profile lists that the plugin cannot render gets no
// fragment.
func TestFragmentsSkipUnknownOperators(t *testing.T) {
	p := &irv1.CapabilityProfile{Operators: []*irv1.OperatorSupport{{Name: "sqlserver.contains_table", Fidelity: irv1.Fidelity_FIDELITY_EXACT}}}
	for _, f := range sqlserver.Fragments(&irv1.Bundle{}, p).GetFragments() {
		if strings.HasPrefix(f.GetId(), "op.") {
			t.Errorf("fragment %s", f.GetId())
		}
	}
}
