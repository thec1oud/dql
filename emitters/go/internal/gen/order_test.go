package gen

import (
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// A nullable column orders by the dialect's nullable fragment when it has
// one, which places NULLs as Postgres does.
func TestOrderFragment(t *testing.T) {
	nullable, required := &irv1.Column{Nullable: true}, &irv1.Column{}
	g := &generator{fragmentIDs: map[string]bool{"order.asc": true, "order.desc": true, "order.asc_nullable": true}}
	for _, tc := range []struct {
		c         *irv1.Column
		dir, want string
	}{
		{nullable, "asc", "order.asc_nullable"},
		{nullable, "desc", "order.desc"},
		{required, "asc", "order.asc"},
	} {
		if got := g.orderFragment(tc.c, tc.dir); got != tc.want {
			t.Errorf("orderFragment(nullable %v, %s) = %s, want %s", tc.c.GetNullable(), tc.dir, got, tc.want)
		}
	}
}
