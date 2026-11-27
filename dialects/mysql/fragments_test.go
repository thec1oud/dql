package mysql_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thec1oud/dql/dialects/mysql"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func ident(stored string, words ...string) *irv1.Identifier {
	if len(words) == 0 {
		words = strings.Split(stored, "_")
	}
	return &irv1.Identifier{Stored: stored, Words: words}
}

func qn(cat, obj string) *irv1.QualifiedName {
	return &irv1.QualifiedName{Catalog: cat, Object: obj}
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

// A bundle of two catalogs, each a database with its one namespace: shop,
// and a second catalog whose table shares a name with shop's.
func fixture() *irv1.Bundle {
	people := qn("shop", "people")
	pets := qn("shop", "pets")
	links := qn("shop", "people_pets")
	return &irv1.Bundle{
		Dialect: irv1.Dialect_DIALECT_MYSQL,
		Catalogs: []*irv1.Catalog{
			{
				Name: ident("shop"),
				Namespaces: []*irv1.Namespace{{
					Name: &irv1.Identifier{},
					Tables: []*irv1.Table{
						{
							Name: ident("people"), HostName: ident("", "person"), Kind: irv1.TableKind_RELATION_KIND_TABLE,
							Columns: columns("id", "manager_id", "email"),
							Uniqueness: []*irv1.Uniqueness{
								{Name: "PRIMARY", Columns: []string{"id"}},
								{Name: "people_lower_email", Expressions: []*irv1.IndexExpression{{Position: 0, Native: "lower(`email`)"}}},
							},
							Relations: []*irv1.Relation{
								{Name: ident("manager"), To: &irv1.ColumnSet{Table: people, Columns: []string{"id"}}, From: &irv1.ColumnSet{Columns: []string{"manager_id"}}},
								{Name: ident("pets"), To: &irv1.ColumnSet{Table: pets, Columns: []string{"id"}}, From: &irv1.ColumnSet{Columns: []string{"id"}}, Through: links},
								{Name: ident("remote"), CrossCatalog: true, To: &irv1.ColumnSet{Table: qn("other", "people")}},
							},
						},
						{
							Name: ident("people_pets"), Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: columns("person_id", "pet_id"),
							ForeignKeys: []*irv1.ForeignKey{
								{Name: ident("people_pets_person"), Columns: []string{"person_id"}, References: &irv1.ColumnSet{Table: people, Columns: []string{"id"}}},
								{Name: ident("people_pets_pet"), Columns: []string{"pet_id"}, References: &irv1.ColumnSet{Table: pets, Columns: []string{"id"}}},
							},
						},
						{
							Name: ident("pets"), Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: columns("id", "we`ird"),
							Uniqueness: []*irv1.Uniqueness{{Name: "PRIMARY", Columns: []string{"id"}}},
						},
						{Name: ident("pet_view"), Kind: irv1.TableKind_RELATION_KIND_VIEW, Columns: columns("id")},
					},
				}},
			},
			{
				Name:       ident("other"),
				Namespaces: []*irv1.Namespace{{Name: &irv1.Identifier{}, Tables: []*irv1.Table{{Name: ident("people"), Kind: irv1.TableKind_RELATION_KIND_TABLE, Columns: columns("id")}}}},
			},
		},
		Shapes: &irv1.ShapeLayer{
			Shapes: []*irv1.Shape{
				fullShape(people, "", irv1.Form_FORM_FLAT, "id", "email"),
				fullShape(people, "", irv1.Form_FORM_NESTED, "id", "email"),
				fullShape(people, "person_brief", irv1.Form_FORM_UNSPECIFIED, "email"),
				fullShape(pets, "", irv1.Form_FORM_FLAT, "we`ird"),
				{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: pets}, Preset: irv1.Preset_PRESET_DRAFT, Form: irv1.Form_FORM_FLAT}, Entity: pets,
					Lineage: []*irv1.LineageStep{{Op: irv1.ShapeOp_PRESET, Preset: irv1.Preset_PRESET_DRAFT}}},
				{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: pets}, Preset: irv1.Preset_PRESET_FULL, Form: irv1.Form_FORM_FLAT, ForRelation: "owner"}, Entity: pets,
					Lineage: []*irv1.LineageStep{{Op: irv1.ShapeOp_PRESET, Preset: irv1.Preset_PRESET_FULL}}},
				{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "lineless"}}, Entity: pets},
			},
			Upserts: []*irv1.Upsert{
				{Entity: people, ConflictTarget: "people_lower_email"},
				{Entity: pets, ConflictTarget: "PRIMARY"},
			},
		},
	}
}

func table(t *testing.T, b *irv1.Bundle) map[string]*irv1.Fragment {
	t.Helper()
	res, err := mysql.New().Invoke(context.Background(), &irv1.Invoke{Bundle: b})
	if err != nil {
		t.Fatal(err)
	}
	ft := res.GetBundle().GetQueries().GetFragments()
	if ft.GetDialect() != irv1.Dialect_DIALECT_MYSQL || ft.GetPlaceholder() != irv1.Placeholder_PLACEHOLDER_QUESTION {
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
	for id, want := range map[string]string{
		// Ids carry the catalog, as the bundle has two. Tables go
		// unqualified and are their own aliases.
		"from.shop.people":        "`people`",
		"from.other.people":       "`people`",
		"col.shop.people.email":   "`people`.`email`",
		"col.shop.pets.we`ird":    "`pets`.`we``ird`",
		"count.shop.pets":         "SELECT count(*) FROM `pets`{where}",
		"exists.shop.pets":        "SELECT EXISTS (SELECT 1 FROM `pets`{where})",
		"rel.shop.people.manager": "EXISTS (SELECT 1 FROM `people` AS `people_r` WHERE `people_r`.`id` = `people`.`manager_id`{and})",
		"col_r.shop.people.email": "`people_r`.`email`",
		"rel.shop.people.pets":    "EXISTS (SELECT 1 FROM `people_pets` JOIN `pets` ON `pets`.`id` = `people_pets`.`pet_id` WHERE `people_pets`.`person_id` = `people`.`id`{and})",
		"select.person.full.flat": "SELECT `people`.`id`, `people`.`email` FROM `people`",
		"select.person_brief":     "SELECT `people`.`email` FROM `people`",
		"select.pets.full.flat":   "SELECT `pets`.`we``ird` FROM `pets`",
		// No write returns its rows; the runtime re-reads them.
		"insert.shop.pets":         "INSERT INTO `pets` ({cols}) VALUES {rows}",
		"update.shop.pets":         "UPDATE `pets` SET {sets}{where}",
		"delete.shop.pets":         "DELETE FROM `pets`{where}",
		"set.shop.pets.id":         "`id` = {0}",
		"name.shop.pets.id":        "`id`",
		"excluded.shop.pets.id":    "`dql_new`.`id`",
		"upsert.shop.pets.PRIMARY": "INSERT INTO `pets` ({cols}) VALUES {rows} AS `dql_new` ON DUPLICATE KEY UPDATE {sets}",
		"ignore.shop.pets.PRIMARY": "INSERT IGNORE INTO `pets` ({cols}) VALUES {rows}",
		"lock.share":               " FOR SHARE",
		"value.no_limit":           "18446744073709551615",
		"value.now":                "CURRENT_TIMESTAMP(6)",
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
		"returning.person.full.flat", // rows are re-read
		"select.distinct_on",
	} {
		if got[id] != nil {
			t.Errorf("unexpected %s: %q", id, got[id].GetTemplate())
		}
	}
	// An upsert is exact only where its key is the table's one uniqueness
	// fact: ON DUPLICATE KEY fires on any key.
	if f := got["upsert.shop.pets.PRIMARY"]; f.GetFidelity() != irv1.Fidelity_FIDELITY_EXACT {
		t.Errorf("pets upsert %v", f.GetFidelity())
	}
	if f := got["upsert.shop.people.people_lower_email"]; f.GetFidelity() != irv1.Fidelity_FIDELITY_APPROXIMATE {
		t.Errorf("people upsert %v", f.GetFidelity())
	}
	// INSERT IGNORE turns other errors into warnings too.
	if f := got["ignore.shop.pets.PRIMARY"]; f.GetFidelity() != irv1.Fidelity_FIDELITY_APPROXIMATE {
		t.Errorf("pets ignore %v", f.GetFidelity())
	}
	if f := got["op.ilike"]; f.GetTemplate() != "LOWER({0}) LIKE LOWER({1})" || f.GetFidelity() != irv1.Fidelity_FIDELITY_EMULATED {
		t.Errorf("op.ilike %v", f)
	}
	if got["clause.where"].GetFidelity() != irv1.Fidelity_FIDELITY_UNSPECIFIED {
		t.Error("a clause has a fidelity")
	}
	for _, s := range mysql.Profile().GetOperators() {
		if got["op."+s.GetName()] == nil {
			t.Errorf("no fragment for %s", s.GetName())
		}
	}
}

// MariaDB has no row alias, so an upsert reads the proposed row through
// VALUES(), and it spells regular expressions and list membership its own
// way.
func TestMariaDBFragments(t *testing.T) {
	b := fixture()
	b.Catalogs = []*irv1.Catalog{b.Catalogs[0]}
	b.Catalogs[0].Extensions = flavored("mariadb").Extensions
	got := table(t, b)
	for id, want := range map[string]string{
		"excluded.pets.id":    "VALUES(`id`)",
		"upsert.pets.PRIMARY": "INSERT INTO `pets` ({cols}) VALUES {rows} ON DUPLICATE KEY UPDATE {sets}",
		"lock.share":          " LOCK IN SHARE MODE",
		"op.regex_match":      "{0} REGEXP CONCAT('(?-i)', {1})",
		"op.in":               "JSON_CONTAINS({1}, JSON_ARRAY({0}))",
		"op.eq":               "{0} = {1}",
	} {
		if got[id].GetTemplate() != want {
			t.Errorf("%s\n got %q\nwant %q", id, got[id].GetTemplate(), want)
		}
	}
}

// An operator the profile lists that the plugin cannot render gets no
// fragment.
func TestFragmentsSkipUnknownOperators(t *testing.T) {
	p := &irv1.CapabilityProfile{Operators: []*irv1.OperatorSupport{{Name: "mysql.soundex", Fidelity: irv1.Fidelity_FIDELITY_EXACT}}}
	for _, f := range mysql.Fragments(&irv1.Bundle{}, p, mysql.MySQL).GetFragments() {
		if strings.HasPrefix(f.GetId(), "op.") {
			t.Errorf("fragment %s", f.GetId())
		}
	}
}

// A list is one JSON array, which a column's in and any expand into items
// of the column's type, in its collation (§25.3); an opaque column, or one
// whose native type is unknown, has none, and falls back to MEMBER OF.
func TestListFragments(t *testing.T) {
	b := fixture()
	cols := b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0].GetColumns()
	native := func(raw string) *irv1.NativeType { return &irv1.NativeType{Raw: raw} }
	cols[0].Type = &irv1.Type{Kind: &irv1.Type_Int64{Int64: &irv1.Empty{}}, Native: native("bigint unsigned")}
	cols[1].Type = &irv1.Type{Kind: &irv1.Type_Opaque{Opaque: &irv1.Opaque{}}, Native: native("set('a')")}
	cols[2].Type = &irv1.Type{Kind: &irv1.Type_Varchar{Varchar: &irv1.Varchar{Length: 80}}, Native: native("varchar(80)")}
	cols[2].Collation = &irv1.Collation{Name: "utf8mb4_0900_ai_ci"}
	got := table(t, b)
	for id, want := range map[string]string{
		"op.in.shop.people.id":     "{0} IN (SELECT dql_list.v FROM JSON_TABLE({1}, '$[*]' COLUMNS (v bigint unsigned PATH '$')) AS dql_list)",
		"op.any.shop.people.email": "{0} IN (SELECT dql_list.v FROM JSON_TABLE({1}, '$[*]' COLUMNS (v varchar(80) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci PATH '$')) AS dql_list)",
		"op.in":                    "{0} MEMBER OF ({1})",
	} {
		if got[id].GetTemplate() != want {
			t.Errorf("%s\n got %q\nwant %q", id, got[id].GetTemplate(), want)
		}
	}
	for _, id := range []string{"op.in.shop.people.manager_id", "op.in.shop.pets.id"} {
		if got[id] != nil {
			t.Errorf("unexpected %s", id)
		}
	}
}

// MariaDB's JSON_TABLE has no enum or set columns, so a list compared with
// one holds its items as text.
func TestMariaDBListFragments(t *testing.T) {
	b := fixture()
	b.Catalogs = []*irv1.Catalog{b.Catalogs[0]}
	b.Catalogs[0].Extensions = flavored("mariadb").Extensions
	cols := b.GetCatalogs()[0].GetNamespaces()[0].GetTables()[0].GetColumns()
	for i, raw := range []string{"enum('a','b')", "set('a')", "bigint"} {
		cols[i].Type = &irv1.Type{Kind: &irv1.Type_Text{Text: &irv1.Text{}}, Native: &irv1.NativeType{Raw: raw}}
	}
	cols[0].Collation = &irv1.Collation{Name: "utf8mb4_uca1400_ai_ci"}
	got := table(t, b)
	for id, want := range map[string]string{
		"op.in.people.id":         "{0} IN (SELECT dql_list.v FROM JSON_TABLE({1}, '$[*]' COLUMNS (v text CHARACTER SET utf8mb4 COLLATE utf8mb4_uca1400_ai_ci PATH '$')) AS dql_list)",
		"op.in.people.manager_id": "{0} IN (SELECT dql_list.v FROM JSON_TABLE({1}, '$[*]' COLUMNS (v text PATH '$')) AS dql_list)",
		"op.in.people.email":      "{0} IN (SELECT dql_list.v FROM JSON_TABLE({1}, '$[*]' COLUMNS (v bigint PATH '$')) AS dql_list)",
	} {
		if got[id].GetTemplate() != want {
			t.Errorf("%s\n got %q\nwant %q", id, got[id].GetTemplate(), want)
		}
	}
}
