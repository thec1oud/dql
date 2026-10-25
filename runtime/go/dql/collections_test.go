package dql_test

import (
	"slices"
	"strconv"
	"testing"

	"github.com/thec1oud/dql/runtime/go/dql"
)

type userFull struct{ ID int64 }

func (u userFull) Key() int64 { return u.ID }

type users []userFull

func TestMap(t *testing.T) {
	rows := users{{1}, {2}}
	if got := dql.Map(rows, userFull.Key); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("Map = %v", got)
	}
	if got := dql.Map([]int{}, strconv.Itoa); got == nil || len(got) != 0 {
		t.Errorf("Map(empty) = %#v", got)
	}
	if got := dql.Map([]int(nil), strconv.Itoa); got != nil {
		t.Errorf("Map(nil) = %#v", got)
	}
}

func TestKeyed(t *testing.T) {
	k := dql.Keyed[int64, string]{Key: 7, Value: "Ann"}
	if k.Key != 7 || k.Value != "Ann" {
		t.Errorf("Keyed = %+v", k)
	}
}

func TestPtr(t *testing.T) {
	v := 3
	p := dql.Ptr(v)
	v = 4
	if *p != 3 {
		t.Errorf("Ptr shares its argument")
	}
}

type slug string

func TestFoldASCII(t *testing.T) {
	if got := dql.FoldASCII(slug("Go-ÄBC_z")); got != "go-Äbc_z" {
		t.Errorf("FoldASCII = %q", got)
	}
	// Under a Turkish locale Unicode case mapping would give a dotless i;
	// the fold is ASCII only.
	if got := dql.FoldASCII("I"); got != "i" {
		t.Errorf("FoldASCII(I) = %q", got)
	}
}

func TestUnmatchedKeyError(t *testing.T) {
	err := error(&dql.UnmatchedKeyError{Entity: "shop.app.countries", Index: 2})
	if want := "dql: change 2 for shop.app.countries matches no row exactly, and its key's collation may match one the client cannot compare"; err.Error() != want {
		t.Errorf("Error = %q", err.Error())
	}
	if dql.ClassOf(err) != "" {
		t.Errorf("class %q", dql.ClassOf(err))
	}
}
