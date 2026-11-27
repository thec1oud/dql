package dql_test

import (
	"cmp"
	"testing"

	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/dql"
)

func TestRangeContains(t *testing.T) {
	in, ex, un := dql.InclusiveBound[int], dql.ExclusiveBound[int], dql.UnboundedBound[int]
	tests := []struct {
		r    dql.Range[int]
		text string
		in   []int
		out  []int
	}{
		{dql.NewRange(in(1), ex(10)), "[1,10)", []int{1, 5, 9}, []int{0, 10}},
		{dql.NewRange(ex(1), in(10)), "(1,10]", []int{2, 10}, []int{1, 11}},
		{dql.NewRange(un(), in(5)), "(,5]", []int{-100, 5}, []int{6}},
		{dql.NewRange(in(5), un()), "[5,)", []int{5, 100}, []int{4}},
		{dql.Range[int]{}, "(,)", []int{-100, 0, 100}, nil},
		{dql.EmptyRange[int](), "empty", nil, []int{0}},
	}
	for _, tt := range tests {
		if got := tt.r.String(); got != tt.text {
			t.Errorf("String() = %q, want %q", got, tt.text)
		}
		for _, v := range tt.in {
			if !tt.r.Contains(v, cmp.Compare[int]) {
				t.Errorf("%s does not contain %d", tt.text, v)
			}
		}
		for _, v := range tt.out {
			if tt.r.Contains(v, cmp.Compare[int]) {
				t.Errorf("%s contains %d", tt.text, v)
			}
		}
	}
}

func TestRangeOfDecimals(t *testing.T) {
	r := dql.NewRange(dql.InclusiveBound(dec.MustParse("0.5")), dql.ExclusiveBound(dec.MustParse("1.50")))
	if !r.Contains(dec.MustParse("1.4999"), dec.Decimal.Cmp) || r.Contains(dec.MustParse("1.5"), dec.Decimal.Cmp) {
		t.Error("Contains")
	}
	if got := r.String(); got != "[0.5,1.50)" {
		t.Errorf("String() = %q", got)
	}
}
