package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// fill fills a template's holes, by name.
func fill(template string, holes map[string]string) string {
	for h, v := range holes {
		template = strings.ReplaceAll(template, "{"+h+"}", v)
	}
	return template
}

// Every statement the running example's fragment table can build parses
// and type-checks on Postgres: each is prepared, which plans it without
// running it (§16.2, §18.4).
func TestFragmentsPrepare(t *testing.T) {
	code, stderr, b := generateShapes(t, recordShapes)
	if code != 0 {
		t.Fatal(stderr)
	}
	ft := b.GetQueries().GetFragments()
	if ft.GetDialect() != irv1.Dialect_DIALECT_POSTGRES || ft.GetPlaceholder() != irv1.Placeholder_PLACEHOLDER_DOLLAR_NUMBERED {
		t.Errorf("table %v %v", ft.GetDialect(), ft.GetPlaceholder())
	}
	fr := map[string]string{}
	for _, f := range ft.GetFragments() {
		fr[f.GetId()] = f.GetTemplate()
	}
	// The record's excerpt (§18.4).
	for id, want := range map[string]string{
		"select.user_summary": `SELECT u."id", u."email", u."display_name" FROM "app"."users" u`,
		"col.users.email":     `u."email"`,
		"op.ilike":            "{0} ILIKE {1}",
		"clause.where":        " WHERE {0}",
		"clause.order":        " ORDER BY {0}",
		"clause.limit":        " LIMIT {0}",
	} {
		if fr[id] != want {
			t.Errorf("%s is %q, want %q", id, fr[id], want)
		}
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, shopURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	prepare := func(what, sql string) {
		t.Helper()
		if _, err := conn.Prepare(ctx, "", sql); err != nil {
			t.Errorf("%s: %s: %v", what, sql, err)
		}
	}
	where := func(pred string) string { return fill(fr["clause.where"], map[string]string{"0": pred}) }

	// The record's first builder call (§18.2), with the scope filter.
	sql := fr["select.user_summary"] +
		where(fill(fr["bool.and"], map[string]string{
			"0": fill(fr["op.eq"], map[string]string{"0": fr["col.users.tenant_id"], "1": "$1"}),
			"1": fill(fr["op.ilike"], map[string]string{"0": fr["col.users.email"], "1": "$2"}),
		})) +
		fill(fr["clause.order"], map[string]string{"0": fill(fr["order.desc"], map[string]string{"0": fr["col.users.created_at"]})}) +
		fill(fr["clause.limit"], map[string]string{"0": "$3"})
	if want := `SELECT u."id", u."email", u."display_name" FROM "app"."users" u WHERE u."tenant_id" = $1 AND u."email" ILIKE $2 ORDER BY u."created_at" DESC LIMIT $3`; sql != want {
		t.Errorf("record example\n got %s\nwant %s", sql, want)
	}
	prepare("record example", sql)

	shapes := map[string]*irv1.Shape{}
	for _, s := range b.GetShapes().GetShapes() {
		shapes[spell(b, s.GetId())] = s
	}
	for _, tb := range b.GetCatalogs()[0].GetNamespaces()[0].GetTables() {
		name := tb.GetName().GetStored()
		col := func(c string) string { return fr["col."+name+"."+c] }
		from := fr["from."+name]
		key := tb.GetColumns()[0].GetName().GetStored()
		if k := tb.GetUniqueness(); len(k) > 0 {
			key = k[0].GetColumns()[0]
		}

		// Each column's operators, as filters.
		for _, c := range tb.GetColumns() {
			for _, op := range c.GetOperators() {
				holes := map[string]string{"0": col(c.GetName().GetStored()), "1": "$1", "2": "$2"}
				pred := fill(fr["op."+op.GetName()], holes)
				switch op.GetName() {
				case "array_contains":
					pred = fill(fr["op."+op.GetName()], map[string]string{"0": holes["0"], "1": "$1::text"})
				case "add", "sub", "mul", "div", "mod", "neg":
					pred = fill(fr["op.is_not_null"], map[string]string{"0": pred})
				}
				prepare(op.GetName(), "SELECT 1 FROM "+from+where(pred))
			}
		}
		filter := where(fill(fr["op.eq"], map[string]string{"0": col(key), "1": "$1"}))
		prepare("count", fill(fr["count."+name], map[string]string{"where": filter}))
		prepare("exists", fill(fr["exists."+name], map[string]string{"where": ""}))
		prepare("exists", fill(fr["exists."+name], map[string]string{"where": filter}))
		for _, r := range tb.GetRelations() {
			target := r.GetTo().GetTable().GetObject()
			more := fill(fr["clause.and"], map[string]string{"0": fill(fr["op.is_not_null"], map[string]string{"0": fr["col."+target+"."+r.GetTo().GetColumns()[0]]})})
			for _, and := range []string{"", more} {
				prepare("relation", "SELECT 1 FROM "+from+where(fill(fr["rel."+name+"."+r.GetName().GetStored()], map[string]string{"and": and})))
			}
		}
		prepare("distinct", fill(fr["select.distinct"], map[string]string{"cols": col(key), "from": from}))
		prepare("distinct on", fill(fr["select.distinct_on"], map[string]string{"keys": col(key), "cols": col(key), "from": from})+
			fill(fr["clause.order"], map[string]string{"0": col(key)}))

		entity := strings.Join(tb.GetHostName().GetWords(), "_")
		full := shapes[entity+".full.flat"]
		read := fr["select."+entity+".full.flat"]
		keyset := fill(fr["keyset.after"], map[string]string{"cols": col(key), "vals": "$1"})
		prepare("select", read+where(keyset)+fill(fr["clause.limit"], map[string]string{"0": "$2"})+fill(fr["clause.offset"], map[string]string{"0": "$3"}))
		if tb.GetKind() != irv1.TableKind_RELATION_KIND_TABLE {
			continue
		}
		for _, lock := range []string{"lock.update", "lock.share"} {
			prepare("lock", read+filter+fr[lock]+fr["lock.skip_locked"])
			prepare("lock", read+filter+fr[lock]+fr["lock.nowait"])
		}
		returning := fr["returning."+entity+".full.flat"]
		if full == nil || returning == "" {
			t.Fatalf("%s has no Full shape", name)
		}

		// Writes from the Draft and Partial.
		var cols, vals, sets, excluded []string
		for i, f := range shapes[entity+".draft.flat"].GetFields() {
			cols = append(cols, fr["name."+name+"."+f.GetColumn()])
			vals = append(vals, fmt.Sprintf("$%d", i+1))
		}
		for i, f := range shapes[entity+".partial.flat"].GetFields() {
			sets = append(sets, fill(fr["set."+name+"."+f.GetColumn()], map[string]string{"0": fmt.Sprintf("$%d", i+2)}))
			excluded = append(excluded, fill(fr["set."+name+"."+f.GetColumn()], map[string]string{"0": fr["excluded."+name+"."+f.GetColumn()]}))
		}
		row := fill(fr["values.row"], map[string]string{"0": strings.Join(vals, ", ")})
		insert := map[string]string{"cols": strings.Join(cols, ", "), "rows": row + ", " + row, "returning": returning}
		prepare("insert", fill(fr["insert."+name], insert))
		for _, u := range b.GetShapes().GetUpserts() {
			if u.GetEntity().GetObject() != name {
				continue
			}
			suffix := name + "." + u.GetConflictTarget()
			prepare("ignore", fill(fr["ignore."+suffix], insert))
			if len(excluded) > 0 {
				prepare("upsert", fill(fr["upsert."+suffix], map[string]string{"cols": insert["cols"], "rows": row, "sets": strings.Join(excluded, ", "), "returning": returning}))
			}
		}
		if len(sets) > 0 {
			prepare("update", fill(fr["update."+name], map[string]string{"sets": strings.Join(sets, ", "), "where": filter, "returning": returning}))
		}
		prepare("delete", fill(fr["delete."+name], map[string]string{"where": filter, "returning": ""}))
		prepare("delete", fill(fr["delete."+name], map[string]string{"where": filter, "returning": returning}))
	}

	// Values the runtime writes itself: a soft delete's now(), a default
	// in a multi-row insert, and a version bump (§20.4, §21.3).
	prepare("values", fill(fr["update.users"], map[string]string{
		"sets": strings.Join([]string{
			fill(fr["set.users.updated_at"], map[string]string{"0": fr["value.now"]}),
			fill(fr["set.users.version"], map[string]string{"0": fill(fr["op.add"], map[string]string{"0": fr["col.users.version"], "1": "$1"})}),
		}, ", "),
		"where": "", "returning": "",
	}))
	prepare("default", fill(fr["insert.tenants"], map[string]string{"cols": fr["name.tenants.id"] + ", " + fr["name.tenants.name"], "rows": "(" + fr["value.default"] + ", $1)", "returning": ""}))
	prepare("boolean", "SELECT 1 FROM "+fr["from.users"]+where(fill(fr["bool.not"], map[string]string{"0": fill(fr["bool.or"], map[string]string{"0": fr["value.true"], "1": "$1"})})))
	prepare("having", "SELECT count(*) FROM "+fr["from.users"]+" GROUP BY "+fr["col.users.tenant_id"]+fill(fr["clause.having"], map[string]string{"0": "count(*) > $1"}))
	prepare("keyset", fr["select.user.full.flat"]+where(fill(fr["keyset.after_desc"], map[string]string{"cols": fr["col.users.created_at"] + ", " + fr["col.users.id"], "vals": "$1, $2"})))
}
