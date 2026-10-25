package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// assembler joins a builder query's fragments as the runtime does (guide
// §5.5): holes in template order, placeholders numbered left to right. A
// set whose optional parameter is absent is dropped, and the parameter is
// DEFAULT in a VALUES row.
type assembler struct {
	fragments map[string]string
	optional  map[string]bool
	values    map[string]any
	args      []any
}

func (a *assembler) param(name string) (string, bool) {
	v, ok := a.values[name]
	if !ok {
		if a.optional[name] {
			return "", false
		}
		panic("no value for " + name)
	}
	a.args = append(a.args, v)
	return fmt.Sprintf("$%d", len(a.args)), true
}

func (a *assembler) use(u *irv1.FragmentUse) (string, bool) {
	t := a.fragments[u.GetFragment()]
	fills := map[string]*irv1.HoleFill{}
	for _, h := range u.GetHoles() {
		fills[h.GetHole()] = h
	}
	var out strings.Builder
	for {
		i := strings.IndexByte(t, '{')
		if i < 0 {
			out.WriteString(t)
			return out.String(), true
		}
		j := strings.IndexByte(t[i:], '}') + i
		out.WriteString(t[:i])
		h := fills[t[i+1:j]]
		switch {
		case h == nil:
		case h.GetFragment() != nil:
			s, _ := a.use(h.GetFragment())
			out.WriteString(s)
		case h.GetList() != nil:
			var items []string
			for _, it := range h.GetList().GetItems() {
				if it.GetFragment() != nil {
					if s, ok := a.use(it.GetFragment()); ok {
						items = append(items, s)
					}
				} else if s, ok := a.param(it.GetParam()); ok {
					items = append(items, s)
				} else {
					items = append(items, a.fragments["value.default"])
				}
			}
			out.WriteString(strings.Join(items, h.GetList().GetSeparator()))
		default:
			s, ok := a.param(h.GetParam())
			if !ok {
				// Only a set holds an optional parameter by itself.
				return "", false
			}
			out.WriteString(s)
		}
		t = t[j+1:]
	}
}

func assemble(fr map[string]string, q *irv1.Query, values map[string]any) (string, []any) {
	a := &assembler{fragments: fr, optional: map[string]bool{}, values: values}
	for _, p := range q.GetParams() {
		a.optional[p.GetName()] = p.GetOptional()
	}
	var sql strings.Builder
	for _, u := range q.GetPlan().GetParts() {
		s, _ := a.use(u)
		sql.WriteString(s)
	}
	return sql.String(), a.args
}

// Every builder query of the running example assembles into SQL that
// prepares on Postgres, and the user operations run end to end (§18 to
// §21).
func TestBuilderQueries(t *testing.T) {
	code, stderr, b := generateShapes(t, recordShapes)
	if code != 0 {
		t.Fatal(stderr)
	}
	fr := map[string]string{}
	for _, f := range b.GetQueries().GetFragments().GetFragments() {
		fr[f.GetId()] = f.GetTemplate()
	}
	byName := map[string]*irv1.Query{}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, shopURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	for _, q := range b.GetQueries().GetQueries() {
		byName[q.GetName().GetStored()] = q
		values := map[string]any{}
		for _, p := range q.GetParams() {
			values[p.GetName()] = nil
		}
		for _, c := range q.GetScope().GetColumns() {
			values["scope."+c] = nil
		}
		sql, _ := assemble(fr, q, values)
		if _, err := conn.Prepare(ctx, "", sql); err != nil {
			t.Errorf("%s: %s: %v", q.GetName().GetStored(), sql, err)
		}
	}

	want := map[string]string{
		"user.get.by_primary": `SELECT u."id", u."tenant_id", u."email", u."display_name", u."created_at", u."updated_at", u."version" FROM "app"."users" u WHERE u."tenant_id" = $1 AND u."id" = $2`,
		"user.patch": `UPDATE "app"."users" AS u SET "email" = $1, "version" = (u."version" + 1) WHERE u."tenant_id" = $2 AND u."id" = $3 AND u."version" = $4` +
			` RETURNING u."id", u."tenant_id", u."email", u."display_name", u."created_at", u."updated_at", u."version"`,
		"user.count": `SELECT count(*) FROM "app"."users" u WHERE u."tenant_id" = $1`,
	}
	for name, sql := range want {
		values := map[string]any{"scope.tenant_id": 1, "key.id": 2, "key.version": 3, "email": "e"}
		if got, _ := assemble(fr, byName[name], values); got != sql {
			t.Errorf("%s\n got %s\nwant %s", name, got, sql)
		}
	}

	// A tenant's user, inserted, read, patched and deleted, in a
	// transaction that rolls back.
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	run := func(name string, values map[string]any) [][]any {
		t.Helper()
		sql, args := assemble(fr, byName[name], values)
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var out [][]any
		for rows.Next() {
			v, _ := rows.Values()
			out = append(out, v)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("%s: %s: %v", name, sql, err)
		}
		return out
	}
	tenant := run("tenant.insert", map[string]any{"name": "acme"})[0][0]
	scope := map[string]any{"scope.tenant_id": tenant}
	with := func(m map[string]any) map[string]any {
		for k, v := range scope {
			m[k] = v
		}
		return m
	}
	// display_name is optional and left out, so it takes its default.
	u := run("user.insert", with(map[string]any{"email": "a@x.com", "password_hash": "h"}))[0]
	id, version := u[0], u[6]
	if u[3] != nil || version != int32(1) {
		t.Errorf("inserted %v", u)
	}
	if got := run("user.get.by_tenant_id_and_email", with(map[string]any{"key.email": "a@x.com"})); len(got) != 1 || got[0][0] != id {
		t.Errorf("get by email %v", got)
	}
	p := run("user.patch", with(map[string]any{"key.id": id, "key.version": version, "display_name": "A"}))
	if len(p) != 1 || p[0][3] != "A" || p[0][2] != "a@x.com" || p[0][6] != int32(2) {
		t.Errorf("patched %v", p)
	}
	if stale := run("user.patch", with(map[string]any{"key.id": id, "key.version": version, "display_name": "B"})); len(stale) != 0 {
		t.Errorf("a stale version patched %v", stale)
	}
	up := run("user.upsert.by_tenant_id_and_email", with(map[string]any{"email": "a@x.com", "password_hash": "h2", "display_name": "C"}))
	if len(up) != 1 || up[0][0] != id || up[0][3] != "C" || up[0][6] != int32(3) {
		t.Errorf("upserted %v", up)
	}
	if n := run("user.count", with(map[string]any{})); n[0][0] != int64(1) {
		t.Errorf("count %v", n)
	}
	if other := run("user.count", map[string]any{"scope.tenant_id": "00000000-0000-0000-0000-000000000000"}); other[0][0] != int64(0) {
		t.Errorf("another tenant counts %v", other)
	}
	run("user.delete", with(map[string]any{"key.id": id, "key.version": int32(3)}))
	if e := run("user.exists", with(map[string]any{})); e[0][0] != false {
		t.Errorf("exists after delete %v", e)
	}
}
