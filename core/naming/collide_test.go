package naming

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/thec1oud/dql/testkit/determinism"
)

func items(stored ...string) []Item {
	var out []Item
	for _, s := range stored {
		out = append(out, Item{Stored: s, Words: Words(s)})
	}
	return out
}

func TestCollisions(t *testing.T) {
	cases := []struct {
		name  string
		items []Item
		want  []Collision
	}{
		{"none", nil, nil},
		{"distinct", items("id", "user_id", "display_name"), nil},
		// The example of §4.3.
		{"user_id and userId", items("user_id", "userId"),
			[]Collision{{Words: []string{"user", "id"}, Stored: []string{"userId", "user_id"}}}},
		{"groups sorted by words", items("Zeta", "user_id", "zeta", "USER_ID", "id", "userId", "z_eta"),
			[]Collision{
				{Words: []string{"user", "id"}, Stored: []string{"USER_ID", "userId", "user_id"}},
				{Words: []string{"zeta"}, Stored: []string{"Zeta", "zeta"}},
			}},
		{"prefix of another list", items("user", "user_id"), nil},
		{"words without characters", items("_", "-"),
			[]Collision{{Words: nil, Stored: []string{"-", "_"}}}},
		{"after prefixes", []Item{
			{Stored: "audit.users", Words: []string{"audit", "users"}},
			{Stored: "app.audit_users", Words: []string{"audit", "users"}},
		}, []Collision{{Words: []string{"audit", "users"}, Stored: []string{"app.audit_users", "audit.users"}}}},
	}
	for _, c := range cases {
		if got := Collisions(c.items); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCollisionsDoNotAliasInput(t *testing.T) {
	in := items("user_id", "userId")
	got := Collisions(in)
	got[0].Words[0] = "changed"
	if in[0].Words[0] != "user" || in[1].Words[0] != "user" {
		t.Fatal("result shares word slices with the input")
	}
	if in[0].Stored != "user_id" {
		t.Fatal("input was reordered")
	}
}

// The result does not depend on the order of the items (§3.2).
func TestCollisionsIgnoreInputOrder(t *testing.T) {
	in := items("user_id", "userId", "USER_ID", "zeta", "Zeta", "order_lines", "OrderLines", "id")
	seed := uint64(0)
	err := determinism.Repeat(50, func() ([]byte, error) {
		seed++
		return json.Marshal(Collisions(determinism.Shuffled(seed, in)))
	})
	if err != nil {
		t.Fatal(err)
	}
}
