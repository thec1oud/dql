package harness

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/thec1oud/dql/conformance/harness/go/testdata/mysqlshopdb"
	"github.com/thec1oud/dql/runtime/go/dql"
	"github.com/thec1oud/dql/runtime/go/opt"
)

// The query files' queries run on MySQL in both statement modes: their ?
// placeholders take the parameters in the order the IR gives, a parameter
// used twice fills two placeholders, and a list is one JSON array that the
// statement expands with JSON_TABLE (conformance/schemas/mysql/queries.sql).
func TestMySQLQueries(t *testing.T) {
	ctx := context.Background()
	annName := "Ann Lee"
	for _, mode := range MySQLSchemas["shop"].Modes {
		t.Run(mode.String(), func(t *testing.T) {
			db := open(t, MySQLSchemas["shop"], false, 0, mode).DB.(mysqlshopdb.DB)
			check := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			acme, err := db.Tenants.Insert(ctx, mysqlshopdb.TenantDraft{ID: uuid.New(), Name: "acme"})
			check(err)
			h := db.WithScope(mysqlshopdb.UserScope{TenantID: acme.ID})
			ann, err := h.Users.Insert(ctx, mysqlshopdb.UserDraft{Email: "ann@acme.com", DisplayName: opt.Some(&annName), PasswordHash: "x"})
			check(err)
			bob, err := h.Users.Insert(ctx, mysqlshopdb.UserDraft{Email: "bob@acme.com", PasswordHash: "x"})
			check(err)
			_, err = h.Posts.Insert(ctx, mysqlshopdb.PostDraft{AuthorID: ann.ID, Title: "One"})
			check(err)

			got, err := h.Queries.GetUserByEmail(ctx, "ANN@acme.com")
			check(err)
			if got.ID != ann.ID {
				t.Errorf("by email %+v", got)
			}
			counts, err := h.Queries.AuthorPostCounts(ctx)
			check(err)
			slices.SortFunc(counts, func(a, b mysqlshopdb.AuthorPostCountsRow) int { return int(a.AuthorID - b.AuthorID) })
			if len(counts) != 2 || counts[0].Posts != 1 || counts[1].Posts != 0 || counts[1].DisplayName != nil {
				t.Errorf("counts %+v", counts)
			}
			// The list compares with the column's collation, which ignores
			// case.
			byEmail, err := h.Queries.UsersByEmail(ctx, []string{"BOB@acme.com", "ann@acme.com", "nobody@acme.com"})
			check(err)
			if len(byEmail) != 2 || byEmail[0].ID != ann.ID || byEmail[1].ID != bob.ID {
				t.Errorf("by emails %+v", byEmail)
			}
			if none, err := h.Queries.UsersByEmail(ctx, nil); err != nil || len(none) != 0 {
				t.Errorf("no emails: %+v %v", none, err)
			}
			found, err := h.Queries.SearchUsers(ctx, "Lee", 10)
			check(err)
			if len(found) != 1 || found[0].ID != ann.ID {
				t.Errorf("search %+v", found)
			}
			all, err := h.Queries.SearchUsers(ctx, "", 1)
			check(err)
			if len(all) != 1 || all[0].ID != ann.ID {
				t.Errorf("search all %+v", all)
			}
			n, err := h.Queries.RenameUser(ctx, nil, ann.ID, ann.Version)
			check(err)
			if n != 1 {
				t.Errorf("rename %d", n)
			}
			if n, err = h.Queries.RenameUser(ctx, nil, ann.ID, ann.Version); err != nil || n != 0 {
				t.Errorf("stale rename %d %v", n, err)
			}
			other := db.WithScope(mysqlshopdb.UserScope{TenantID: uuid.New()})
			if _, err := other.Queries.GetUserByEmail(ctx, "ann@acme.com"); dql.ClassOf(err) != dql.ClassNotFound {
				t.Errorf("another scope: %v", err)
			}
		})
	}
}
