package postgres_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thec1oud/dql/dialects/postgres"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func ident(stored string, words ...string) *irv1.Identifier {
	if len(words) == 0 {
		words = strings.Split(stored, "_")
	}
	return &irv1.Identifier{Stored: stored, Words: words}
}

func qn(cat, ns, obj string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: cat, Namespace: ns, Object: obj}
}

func columns(names ...string) []*irv1.Column {
	var out []*irv1.Column
	for _, n := range names {
		out = append(out, &irv1.Column{Name: ident(n)})
	}
	return out
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

// A bundle of two catalogs: shop, with tables in its default namespace and
// in audit, and a second catalog whose table shares shop's names.
func fixture() *irv1.Bundle {
	people := qn("shop", "app", "people")
	pets := qn("shop", "app", "pets")
	links := qn("shop", "app", "people_pets")
	logs := qn("shop", "audit", "all_sales")
	return &irv1.Bundle{
		Dialect: irv1.Dialect_DIALECT_POSTGRES,
		Catalogs: []*irv1.Catalog{
			{
				Name: ident("shop"), DefaultNamespace: "app",
				Namespaces: []*irv1.Namespace{
					{
						Name: ident("app"),
						Tables: []*irv1.Table{
							{
								Name: ident("people"), HostName: ident("", "person"), Kind: irv1.TableKind_RELATION_KIND_TABLE,
								Columns: columns("id", "manager_id", "email"),
								Uniqueness: []*irv1.Uniqueness{
									{Name: "people_lower_email", Expressions: []*irv1.IndexExpression{{Position: 1, Native: "lower(email)"}}, Columns: []string{"manager_id"},
										Predicate: &irv1.NativeText{Native: "(manager_id IS NOT NULL)"}},
									{Name: "people_pkey", Columns: []string{"id"}},
								},
								Relations: []*irv1.Relation{
									{Name: ident("manager"), To: &irv1.ColumnSet{Table: people, Columns: []string{"id"}}, From: &irv1.ColumnSet{Columns: []string{"manager_id"}}},
									{Name: ident("pets"), To: &irv1.ColumnSet{Table: pets, Columns: []string{"id"}}, From: &irv1.ColumnSet{Columns: []string{"id"}}, Through: links},
									{Name: ident("remote"), CrossCatalog: true, To: &irv1.ColumnSet{Table: qn("other", "public", "people")}},
								},
							},
							{
								Name: ident("people_pets"), Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: columns("person_id", "pet_id"),
								ForeignKeys: []*irv1.ForeignKey{
									{Name: ident("people_pets_person_id_fkey"), Columns: []string{"person_id"}, References: &irv1.ColumnSet{Table: people, Columns: []string{"id"}}},
									{Name: ident("people_pets_pet_id_fkey"), Columns: []string{"pet_id"}, References: &irv1.ColumnSet{Table: pets, Columns: []string{"id"}}},
								},
							},
							{Name: ident("pets"), Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: columns("id")},
							{Name: ident("pet_view"), Kind: irv1.TableKind_RELATION_KIND_VIEW, Columns: columns("id")},
							{Name: ident("1st"), Kind: irv1.TableKind_RELATION_KIND_VIEW, Columns: columns("id")},
							{Name: &irv1.Identifier{Stored: "x"}, Kind: irv1.TableKind_RELATION_KIND_VIEW, Columns: columns("id")},
						},
					},
					{Name: ident("audit"), Tables: []*irv1.Table{{Name: ident("all_sales"), Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: columns(`we"ird`)}}},
				},
			},
			{
				Name: ident("other"), DefaultNamespace: "public",
				Namespaces: []*irv1.Namespace{{Name: ident("public"), Tables: []*irv1.Table{{Name: ident("people"), Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: columns("id")}}}},
			},
		},
		Shapes: &irv1.ShapeLayer{
			Shapes: []*irv1.Shape{
				fullShape(people, "", irv1.Form_FORM_FLAT, "id", "email"),
				fullShape(people, "", irv1.Form_FORM_NESTED, "id", "email"),
				fullShape(people, "person_brief", irv1.Form_FORM_UNSPECIFIED, "email"),
				fullShape(logs, "", irv1.Form_FORM_FLAT, `we"ird`),
				{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: pets}, Preset: irv1.Preset_PRESET_DRAFT, Form: irv1.Form_FORM_FLAT}, Entity: pets,
					Lineage: []*irv1.LineageStep{{Op: irv1.ShapeOp_PRESET, Preset: irv1.Preset_PRESET_DRAFT}}},
				{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: pets}, Preset: irv1.Preset_PRESET_FULL, Form: irv1.Form_FORM_FLAT, ForRelation: "owner"}, Entity: pets,
					Lineage: []*irv1.LineageStep{{Op: irv1.ShapeOp_PRESET, Preset: irv1.Preset_PRESET_FULL}}},
				{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "lineless"}}, Entity: pets},
			},
			Upserts: []*irv1.Upsert{{Entity: people, ConflictTarget: "people_lower_email"}},
		},
	}
}

func TestFragments(t *testing.T) {
	b := fixture()
	res, err := postgres.New().Invoke(context.Background(), &irv1.Invoke{Bundle: b})
	if err != nil {
		t.Fatal(err)
	}
	ft := res.GetBundle().GetQueries().GetFragments()
	if ft.GetDialect() != irv1.Dialect_DIALECT_POSTGRES || ft.GetPlaceholder() != irv1.Placeholder_PLACEHOLDER_DOLLAR_NUMBERED {
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
	for id, want := range map[string]string{
		// Ids carry the catalog, as the bundle has two, and the namespace
		// outside the default one.
		"from.shop.people":                 `"app"."people" p2`,
		"from.shop.people_pets":            `"app"."people_pets" pp`,
		"from.shop.pets":                   `"app"."pets" p3`,
		"from.shop.pet_view":               `"app"."pet_view" pv`,
		"from.shop.1st":                    `"app"."1st" t`,
		"from.shop.x":                      `"app"."x" t2`,
		"from.shop.audit.all_sales":        `"audit"."all_sales" as2`,
		"from.other.people":                `"public"."people" p`,
		"col.shop.audit.all_sales.we\"ird": `as2."we""ird"`,
		// A self-relation's rows take the _r alias, with their own columns.
		"rel.shop.people.manager":               `EXISTS (SELECT 1 FROM "app"."people" p2_r WHERE p2_r."id" = p2."manager_id"{and})`,
		"col_r.shop.people.email":               `p2_r."email"`,
		"rel.shop.people.pets":                  `EXISTS (SELECT 1 FROM "app"."people_pets" pp JOIN "app"."pets" p3 ON p3."id" = pp."pet_id" WHERE pp."person_id" = p2."id"{and})`,
		"select.person.full.flat":               `SELECT p2."id", p2."email" FROM "app"."people" p2`,
		"returning.person_brief":                ` RETURNING p2."email"`,
		"select.all_sales.full.flat":            `SELECT as2."we""ird" FROM "audit"."all_sales" as2`,
		"upsert.shop.people.people_lower_email": `INSERT INTO "app"."people" AS p2 ({cols}) VALUES {rows} ON CONFLICT ("manager_id", (lower(email))) WHERE (manager_id IS NOT NULL) DO UPDATE SET {sets}{returning}`,
		"ignore.shop.people.people_lower_email": `INSERT INTO "app"."people" AS p2 ({cols}) VALUES {rows} ON CONFLICT ("manager_id", (lower(email))) WHERE (manager_id IS NOT NULL) DO NOTHING{returning}`,
		"insert.shop.pets":                      `INSERT INTO "app"."pets" AS p3 ({cols}) VALUES {rows}{returning}`,
		"update.shop.pets":                      `UPDATE "app"."pets" AS p3 SET {sets}{where}{returning}`,
		"delete.shop.pets":                      `DELETE FROM "app"."pets" AS p3{where}{returning}`,
		"set.shop.pets.id":                      `"id" = {0}`,
		"excluded.shop.pets.id":                 `EXCLUDED."id"`,
		"name.shop.pets.id":                     `"id"`,
	} {
		if got[id].GetTemplate() != want {
			t.Errorf("%s\n got %q\nwant %q", id, got[id].GetTemplate(), want)
		}
	}
	for _, id := range []string{
		"rel.shop.people.remote",    // crosses catalogs
		"col_r.shop.pets.id",        // pets has no self-relation
		"insert.shop.pet_view",      // a view takes no writes
		"select.person.full.nested", // the Flat select serves it
		"select.pet.draft.flat",     // a write shape
		"select.pet.full.flat.for.owner",
		"select.lineless",
	} {
		if got[id] != nil {
			t.Errorf("unexpected %s: %q", id, got[id].GetTemplate())
		}
	}
	if f := got["op.ilike"]; f.GetTemplate() != "{0} ILIKE {1}" || f.GetFidelity() != irv1.Fidelity_FIDELITY_EXACT {
		t.Errorf("op.ilike %v", f)
	}
	if got["clause.where"].GetFidelity() != irv1.Fidelity_FIDELITY_UNSPECIFIED {
		t.Error("a clause has a fidelity")
	}
	// Every operator the profile supports renders.
	for _, s := range postgres.Profile().GetOperators() {
		if got["op."+s.GetName()] == nil {
			t.Errorf("no fragment for %s", s.GetName())
		}
	}
}

// An operator the profile lists that the plugin cannot render gets no
// fragment.
func TestFragmentsSkipUnknownOperators(t *testing.T) {
	p := &irv1.CapabilityProfile{Operators: []*irv1.OperatorSupport{{Name: "postgres.soundex", Fidelity: irv1.Fidelity_FIDELITY_EXACT}}}
	for _, f := range postgres.Fragments(&irv1.Bundle{}, p).GetFragments() {
		if strings.HasPrefix(f.GetId(), "op.") {
			t.Errorf("fragment %s", f.GetId())
		}
	}
}
