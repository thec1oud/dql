package queries

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// describe writes a plan as nested calls: fragment(hole=value, ...), with
// a parameter as $name and a list as [a, b].
func describe(parts []*irv1.FragmentUse) string {
	var out []string
	for _, u := range parts {
		out = append(out, describeUse(u))
	}
	return strings.Join(out, " + ")
}

func describeUse(u *irv1.FragmentUse) string {
	if len(u.GetHoles()) == 0 {
		return u.GetFragment()
	}
	var holes []string
	for _, h := range u.GetHoles() {
		var v string
		switch {
		case h.GetFragment() != nil:
			v = describeUse(h.GetFragment())
		case h.GetList() != nil:
			var items []string
			for _, it := range h.GetList().GetItems() {
				if it.GetFragment() != nil {
					items = append(items, describeUse(it.GetFragment()))
				} else {
					items = append(items, "$"+it.GetParam())
				}
			}
			v = "[" + strings.Join(items, ", ") + "]"
		default:
			v = "$" + h.GetParam()
		}
		holes = append(holes, h.GetHole()+"="+v)
	}
	return u.GetFragment() + "(" + strings.Join(holes, ", ") + ")"
}

func planned(b *irv1.Bundle) map[string]*irv1.Query {
	out := map[string]*irv1.Query{}
	for _, q := range Plan(b) {
		out[q.GetName().GetStored()] = q
	}
	return out
}

func names(qs map[string]*irv1.Query) string {
	var out []string
	for n := range qs {
		out = append(out, n)
	}
	slices.Sort(out)
	return strings.Join(out, " ")
}

func addFragments(b *irv1.Bundle, ids ...string) {
	for _, id := range ids {
		b.Queries.Fragments.Fragments = append(b.Queries.Fragments.Fragments, &irv1.Fragment{Id: id})
	}
}

func dropFragments(b *irv1.Bundle, ids ...string) {
	b.Queries.Fragments.Fragments = slices.DeleteFunc(b.Queries.Fragments.Fragments, func(f *irv1.Fragment) bool { return slices.Contains(ids, f.GetId()) })
}

func table(b *irv1.Bundle, name string) *irv1.Table {
	for _, t := range b.GetCatalogs()[0].GetNamespaces()[0].GetTables() {
		if t.GetName().GetStored() == name {
			return t
		}
	}
	return nil
}

func TestPlanShop(t *testing.T) {
	qs := planned(shop(t))
	want := "post.count post.delete post.exists post.find.by_primary post.get.by_primary post.insert post.patch " +
		"post.upsert.by_posts_one_published_slug post.upsert.by_primary " +
		"post_tag.count post_tag.delete post_tag.exists post_tag.find.by_primary post_tag.get.by_primary post_tag.insert post_tag.upsert.by_primary " +
		"profile.count profile.delete profile.exists profile.find.by_primary profile.get.by_primary profile.insert profile.patch profile.upsert.by_primary " +
		"published_post.count published_post.exists published_post.find.by_logical_key published_post.get.by_logical_key " +
		"tag.count tag.delete tag.exists tag.find.by_name tag.find.by_primary tag.get.by_name tag.get.by_primary tag.insert tag.patch tag.upsert.by_name tag.upsert.by_primary " +
		"tenant.count tenant.delete tenant.exists tenant.find.by_primary tenant.get.by_primary tenant.insert tenant.patch tenant.upsert.by_primary " +
		"user.count user.delete user.exists user.find.by_primary user.find.by_tenant_id_and_email user.get.by_primary user.get.by_tenant_id_and_email " +
		"user.insert user.patch user.upsert.by_primary user.upsert.by_tenant_id_and_email"
	if got := names(qs); got != want {
		t.Errorf("queries\n got %s\nwant %s", got, want)
	}
	scope := "op.eq(0=col.users.tenant_id, 1=$scope.tenant_id)"
	full := "returning.user.full.flat"
	// The Draft's optional fields take the database's defaults when left out.
	draftCols := "name.users.email, name.users.display_name, name.users.password_hash, name.users.created_at, name.users.updated_at, name.users.version, name.users.tenant_id"
	draftVals := "$email, $display_name, $password_hash, $created_at, $updated_at, $version, $scope.tenant_id"
	for name, plan := range map[string]string{
		"user.get.by_primary": "select.user.full.flat + clause.where(0=bool.and(0=" + scope + ", 1=op.eq(0=col.users.id, 1=$key.id)))",
		// The scope supplies tenant_id, so the key filters on email alone.
		"user.find.by_tenant_id_and_email": "select.user.full.flat + clause.where(0=bool.and(0=" + scope + ", 1=op.eq(0=col.users.email, 1=$key.email)))",
		"user.count":                       "count.users(where=clause.where(0=" + scope + "))",
		"tenant.exists":                    "exists.tenants",
		"user.insert":                      "insert.users(cols=[" + draftCols + "], returning=" + full + ", rows=values.row(0=[" + draftVals + "]))",
		"user.patch": "update.users(returning=" + full + ", sets=[set.users.email(0=$email), set.users.display_name(0=$display_name), set.users.password_hash(0=$password_hash), " +
			"set.users.version(0=op.add(0=col.users.version, 1=value.one))], " +
			"where=clause.where(0=bool.and(0=bool.and(0=" + scope + ", 1=op.eq(0=col.users.id, 1=$key.id)), 1=op.eq(0=col.users.version, 1=$key.version))))",
		"user.delete": "delete.users(where=clause.where(0=bool.and(0=bool.and(0=" + scope + ", 1=op.eq(0=col.users.id, 1=$key.id)), 1=op.eq(0=col.users.version, 1=$key.version))))",
		"user.upsert.by_tenant_id_and_email": "upsert.users.users_tenant_email_key(cols=[" + draftCols + "], returning=" + full + ", rows=values.row(0=[" + draftVals + "]), " +
			"sets=[set.users.display_name(0=excluded.users.display_name), set.users.password_hash(0=excluded.users.password_hash), set.users.version(0=op.add(0=col.users.version, 1=value.one))])",
		// Every field is the key: the upsert sets a key column to itself.
		"post_tag.upsert.by_primary": "upsert.post_tags.post_tags_pkey(cols=[name.post_tags.post_id, name.post_tags.tag_id], returning=returning.post_tag.full.flat, " +
			"rows=values.row(0=[$post_id, $tag_id]), sets=[set.post_tags.post_id(0=excluded.post_tags.post_id)])",
	} {
		if got := describe(qs[name].GetPlan().GetParts()); got != plan {
			t.Errorf("%s\n got %s\nwant %s", name, got, plan)
		}
	}

	get := qs["user.get.by_primary"]
	if get.GetSource() != irv1.QuerySource_QUERY_SOURCE_BUILDER || get.GetCardinality() != irv1.Cardinality_CARDINALITY_ONE || !get.GetAtMostOne() ||
		get.GetWrites() || get.GetCatalog() != "shop" || !slices.Equal(get.GetScope().GetColumns(), []string{"tenant_id"}) ||
		get.GetResult().GetShape().GetPreset() != irv1.Preset_PRESET_FULL || len(get.GetTables()) != 1 || get.GetTables()[0].GetObject() != "users" {
		t.Errorf("user.get.by_primary %v", get)
	}
	if p := get.GetParams(); len(p) != 1 || p[0].GetName() != "key.id" || p[0].GetType().GetInt64() == nil {
		t.Errorf("get params %v", p)
	}
	if qs["user.find.by_primary"].GetCardinality() != irv1.Cardinality_CARDINALITY_OPTIONAL {
		t.Error("find is not optional")
	}
	var params []string
	for _, p := range qs["user.patch"].GetParams() {
		params = append(params, p.GetName())
		if p.GetOptional() != !strings.HasPrefix(p.GetName(), "key.") {
			t.Errorf("patch param %s optional %v", p.GetName(), p.GetOptional())
		}
	}
	if got := strings.Join(params, " "); got != "email display_name password_hash key.id key.version" {
		t.Errorf("patch params %s", got)
	}
	ins := qs["user.insert"].GetParams()
	if len(ins) != 6 || ins[0].GetOptional() || !ins[1].GetOptional() || !ins[1].GetNullable() || !ins[5].GetOptional() {
		t.Errorf("insert params %v", ins)
	}
	for name, card := range map[string]irv1.Cardinality{
		"user.count": irv1.Cardinality_CARDINALITY_COUNT, "user.exists": irv1.Cardinality_CARDINALITY_EXISTS,
		"user.delete": irv1.Cardinality_CARDINALITY_EXEC_ROWS, "user.insert": irv1.Cardinality_CARDINALITY_ONE,
	} {
		if qs[name].GetCardinality() != card {
			t.Errorf("%s is %v", name, qs[name].GetCardinality())
		}
	}
	if qs["tenant.count"].GetScope() != nil || qs["user.count"].GetResult() != nil || !qs["user.delete"].GetWrites() {
		t.Error("scope, result or writes")
	}

	// The builder's queries are not checked as query files are.
	b := shop(t)
	if got := check(b, qs["user.count"]); got != "" {
		t.Errorf("check: %s", got)
	}
}

// A soft-delete entity reads and deletes only live rows, and gets a hard
// delete that reaches deleted ones (§21.3).
func TestPlanSoftDelete(t *testing.T) {
	b := shop(t)
	posts := table(b, "posts")
	for _, c := range posts.GetColumns() {
		if c.GetName().GetStored() == "published_at" {
			c.Annotations = append(c.Annotations, &irv1.Annotation{Key: keySoftDelete})
		}
	}
	users := table(b, "users")
	users.Columns = append(users.Columns, &irv1.Column{Name: &irv1.Identifier{Stored: "hidden"}, Type: &irv1.Type{Kind: &irv1.Type_Bool{Bool: &irv1.Empty{}}},
		Annotations: []*irv1.Annotation{{Key: keySoftDelete}}})
	addFragments(b, "col.users.hidden", "set.users.hidden")
	qs := planned(b)
	live := "op.is_null(0=col.posts.published_at)"
	key := "op.eq(0=col.posts.id, 1=$key.id)"
	for name, plan := range map[string]string{
		"post.get.by_primary": "select.post.full.flat + clause.where(0=bool.and(0=" + live + ", 1=" + key + "))",
		"post.delete":         "update.posts(sets=[set.posts.published_at(0=value.now)], where=clause.where(0=bool.and(0=" + live + ", 1=" + key + ")))",
		"post.hard_delete":    "delete.posts(where=clause.where(0=" + key + "))",
		"user.count":          "count.users(where=clause.where(0=bool.and(0=op.eq(0=col.users.tenant_id, 1=$scope.tenant_id), 1=op.is_distinct_from(0=col.users.hidden, 1=value.true))))",
		"user.delete": "update.users(sets=[set.users.hidden(0=value.true), set.users.version(0=op.add(0=col.users.version, 1=value.one))], " +
			"where=clause.where(0=bool.and(0=bool.and(0=bool.and(0=op.eq(0=col.users.tenant_id, 1=$scope.tenant_id), 1=op.is_distinct_from(0=col.users.hidden, 1=value.true)), " +
			"1=op.eq(0=col.users.id, 1=$key.id)), 1=op.eq(0=col.users.version, 1=$key.version))))",
		"user.hard_delete": "delete.users(where=clause.where(0=bool.and(0=bool.and(0=op.eq(0=col.users.tenant_id, 1=$scope.tenant_id), " +
			"1=op.eq(0=col.users.id, 1=$key.id)), 1=op.eq(0=col.users.version, 1=$key.version))))",
	} {
		if got := describe(qs[name].GetPlan().GetParts()); got != plan {
			t.Errorf("%s\n got %s\nwant %s", name, got, plan)
		}
	}
}

// Custom write shapes get their own insert and patch; a bound column is a
// parameter (§14.8).
func TestPlanCustomShapes(t *testing.T) {
	b := shop(t)
	var draft, partial *irv1.Shape
	for _, s := range b.GetShapes().GetShapes() {
		if s.GetEntity().GetObject() == "users" && s.GetId().GetForm() == irv1.Form_FORM_FLAT && s.GetId().GetForRelation() == "" {
			switch s.GetId().GetPreset() {
			case irv1.Preset_PRESET_DRAFT:
				draft = s
			case irv1.Preset_PRESET_PARTIAL:
				partial = s
			}
		}
	}
	invite := proto.Clone(draft).(*irv1.Shape)
	invite.Id = &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "quick_invite"}}
	invite.Fields = invite.Fields[:1]
	invite.Bind = []string{"password_hash"}
	rename := proto.Clone(partial).(*irv1.Shape)
	rename.Id = &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "rename"}}
	rename.Fields = rename.Fields[1:2]
	nolineage := &irv1.Shape{Id: &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "bare"}}, Entity: users()}
	b.Shapes.Shapes = append(b.Shapes.Shapes, invite, rename, nolineage)
	qs := planned(b)
	if got := describe(qs["user.insert.quick_invite"].GetPlan().GetParts()); got != "insert.users(cols=[name.users.email, name.users.tenant_id, name.users.password_hash], "+
		"returning=returning.user.full.flat, rows=values.row(0=[$email, $scope.tenant_id, $password_hash]))" {
		t.Errorf("quick_invite: %s", got)
	}
	if p := qs["user.insert.quick_invite"].GetParams(); len(p) != 2 || p[1].GetName() != "password_hash" || p[1].GetOptional() {
		t.Errorf("quick_invite params %v", p)
	}
	if got := describe(qs["user.patch.rename"].GetPlan().GetParts()); !strings.HasPrefix(got, "update.users(returning=returning.user.full.flat, sets=[set.users.display_name(0=$display_name), set.users.version") {
		t.Errorf("rename: %s", got)
	}
	if qs["user.insert.bare"] != nil || qs["user.patch.bare"] != nil {
		t.Error("a shape without lineage got queries")
	}
}

// A query whose fragments the dialect lacks is left out, as are the writes
// of an entity without a Key and every query of one without a Full.
func TestPlanMissing(t *testing.T) {
	b := shop(t)
	dropFragments(b,
		"count.tags",              // a part
		"returning.tag.full.flat", // a hole's fragment
		"name.tenants.name",       // a list item
		"value.one",               // a fragment inside a list item's hole
	)
	b.Shapes.Shapes = slices.DeleteFunc(b.Shapes.Shapes, func(s *irv1.Shape) bool {
		e := s.GetEntity().GetObject()
		return e == "profiles" && s.GetId().GetPreset() == irv1.Preset_PRESET_FULL ||
			e == "post_tags" && s.GetId().GetPreset() == irv1.Preset_PRESET_KEY
	})
	for _, u := range b.GetShapes().GetUpserts() {
		if u.GetEntity().GetObject() == "posts" {
			u.InsertShape = &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: "nope"}}
		}
	}
	qs := planned(b)
	for _, name := range []string{
		"tag.count", "tag.insert", "tag.patch", "tag.upsert.by_primary", "tenant.insert", "user.patch", "user.upsert.by_primary",
		"profile.get.by_primary", "profile.count", "profile.upsert.by_primary", "post_tag.delete", "post.upsert.by_primary",
	} {
		if qs[name] != nil {
			t.Errorf("%s was planned", name)
		}
	}
	for _, name := range []string{"tag.exists", "tag.delete", "tenant.patch", "user.insert", "post_tag.insert"} {
		if qs[name] == nil {
			t.Errorf("%s was not planned", name)
		}
	}
}

// An upsert on a key of expressions alone, whose fields the default update
// all leaves alone, has nothing to set and is left out; an unversioned
// Partial with no fields gets no patch.
func TestPlanNothingToSet(t *testing.T) {
	b := shop(t)
	tags := table(b, "tags")
	tags.Uniqueness = append(tags.Uniqueness, &irv1.Uniqueness{Name: "tags_lower_name", Expressions: []*irv1.IndexExpression{{Native: "lower(name)"}}})
	tq := &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: "tags"}
	b.Shapes.Upserts = append(b.Shapes.Upserts, &irv1.Upsert{Entity: tq, ConflictTarget: "tags_lower_name",
		InsertShape:           &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: tq}, Preset: irv1.Preset_PRESET_DRAFT, Form: irv1.Form_FORM_FLAT},
		DefaultUpdateExcludes: []*irv1.UpdateExclude{{Column: "name"}}})
	addFragments(b, "upsert.tags.tags_lower_name")
	qs := planned(b)
	if qs["tag.upsert.by_tags_lower_name"] != nil || qs["post_tag.patch"] != nil {
		t.Error("planned a query with nothing to set")
	}
}

func TestTableID(t *testing.T) {
	cats := []*irv1.Catalog{
		{Name: &irv1.Identifier{Stored: "shop"}, DefaultNamespace: "app"},
		{Name: &irv1.Identifier{Stored: "events"}, DefaultNamespace: "public"},
	}
	for _, tt := range []struct {
		cats []*irv1.Catalog
		q    *irv1.QualifiedName
		want string
	}{
		{cats[:1], &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: "users"}, "users"},
		{cats[:1], &irv1.QualifiedName{Catalog: "shop", Namespace: "audit", Object: "log"}, "audit.log"},
		{cats, &irv1.QualifiedName{Catalog: "events", Namespace: "public", Object: "hits"}, "events.hits"},
		{cats, &irv1.QualifiedName{Catalog: "nope", Namespace: "x", Object: "y"}, "nope.x.y"},
	} {
		if got := TableID(tt.cats, tt.q); got != tt.want {
			t.Errorf("%v: %s, want %s", tt.q, got, tt.want)
		}
	}
}

// An entity without host-name words is spelled by its table's name.
func TestPlanTableWords(t *testing.T) {
	b := shop(t)
	table(b, "tenants").HostName = nil
	qs := planned(b)
	if qs["tenants.count"] == nil || qs["tenant.count"] != nil {
		t.Errorf("queries %s", names(qs))
	}
}
