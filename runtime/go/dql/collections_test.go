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
