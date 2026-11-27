package dql_test

import (
	"cmp"
	"errors"
	"regexp"
	"testing"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
	"github.com/thec1oud/dql/runtime/go/dql"
)

// §10.3: lengths count in the database's unit, never Go's bytes.
func TestLength(t *testing.T) {
	s := "aé😀" // 3 code points, 7 bytes, 4 UTF-16 code units
	for unit, want := range map[dql.Unit]int{dql.UnitCharacters: 3, dql.UnitBytes: 7, dql.UnitUTF16CodeUnits: 4, "": 3} {
		if got := dql.Length(s, unit); got != want {
			t.Errorf("Length(%q, %q) = %d, want %d", s, unit, got, want)
		}
	}
}

func TestLengthCheck(t *testing.T) {
	// §10.5: PostDraft{AuthorID: 7, Title: ""}.Validate()
	c := dql.LengthCheck{Constraint: "shop.app.posts.posts_title_check", Min: dql.Ptr(1), Max: dql.Ptr(200), Unit: dql.UnitCharacters}
	var v dql.ValidationErrors
	c.Check(&v, "post.draft.flat", "title", "")
	c.Check(&v, "post.draft.flat", "title", "Hello")
	want := dql.ValidationIssue{Shape: "post.draft.flat", Field: "title", Kind: dql.KindLength, Constraint: "shop.app.posts.posts_title_check",
		Min: 1, Max: 200, MinInclusive: true, MaxInclusive: true, Unit: dql.UnitCharacters, Got: 0}
	if len(v) != 1 || v[0].String() != want.String() || v[0].Min != 1 || v[0].Max != 200 || v[0].Shape != want.Shape {
		t.Fatalf("issues %+v", v)
	}
	// An unbounded side stays nil; 320 code points of é pass a max of 320
	// characters though they are 640 bytes.
	open := dql.LengthCheck{Max: dql.Ptr(320), Unit: dql.UnitCharacters}
	v = nil
	long := ""
	for range 320 {
		long += "é"
	}
	open.Check(&v, "", "email", long)
	open.Check(&v, "", "email", long+"x")
	if len(v) != 1 || v[0].Min != nil || v[0].Got != 321 {
		t.Fatalf("issues %+v", v)
	}
}

func TestNotEmptyCheck(t *testing.T) {
	var v dql.ValidationErrors
	c := dql.NotEmptyCheck{Constraint: "c"}
	c.Check(&v, "s", "name", " ")
	c.Check(&v, "s", "name", "")
	if len(v) != 1 || v[0].Kind != dql.KindNotEmpty || v[0].Field != "name" || v[0].Constraint != "c" {
		t.Fatalf("issues %+v", v)
	}
}

// §10.2: an unanchored search over code points, with explicit anchors and
// ASCII-only classes.
func TestPatternCheck(t *testing.T) {
	slug := dql.PatternCheck{Constraint: "shop.app.slug_text.slug_text_check", Regex: regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)}
	any := dql.PatternCheck{Regex: regexp.MustCompile(`b.d`)}
	digits := dql.PatternCheck{Regex: regexp.MustCompile(`^\d+$`)}
	var v dql.ValidationErrors
	slug.Check(&v, "", "name", "go-lang")
	any.Check(&v, "", "a", "abéd") // . is one code point, not one byte
	any.Check(&v, "", "a", "abd")
	digits.Check(&v, "", "n", "١٢") // Arabic-Indic digits are not \d
	slug.Check(&v, "", "name", "Go\n")
	if len(v) != 3 || v[0].Field != "a" || v[1].Field != "n" || v[2].Pattern != `^[a-z0-9]+(-[a-z0-9]+)*$` || v[2].Constraint != slug.Constraint {
		t.Fatalf("issues %+v", v)
	}
}

func TestRangeCheck(t *testing.T) {
	closed := dql.RangeCheck[int64]{Constraint: "c", Min: dql.Ptr[int64](0), Max: dql.Ptr[int64](10), MinInclusive: true, MaxInclusive: true, Compare: cmp.Compare[int64]}
	open := dql.RangeCheck[int64]{Min: dql.Ptr[int64](0), Max: dql.Ptr[int64](10), Compare: cmp.Compare[int64]}
	var v dql.ValidationErrors
	for _, x := range []int64{0, 5, 10} {
		closed.Check(&v, "", "n", x)
	}
	open.Check(&v, "", "n", 5)
	if len(v) != 0 {
		t.Fatalf("issues %+v", v)
	}
	open.Check(&v, "", "n", 0)
	open.Check(&v, "", "n", 10)
	closed.Check(&v, "", "n", 11)
	if len(v) != 3 || v[2].Min != int64(0) || v[2].Max != int64(10) || !v[2].MaxInclusive || v[2].Constraint != "c" {
		t.Fatalf("issues %+v", v)
	}
	// Unbounded sides, in a decimal and a date.
	price := dql.RangeCheck[dec.Decimal]{Min: dql.Ptr(dec.MustParse("0")), Compare: dec.Decimal.Cmp}
	day := dql.RangeCheck[civil.Date]{Max: dql.Ptr(dql.Must(civil.ParseDate("2000-01-01"))), Compare: civil.Date.Compare}
	v = nil
	price.Check(&v, "", "price", dec.MustParse("0.01"))
	price.Check(&v, "", "price", dec.MustParse("-0.01"))
	day.Check(&v, "", "day", dql.Must(civil.ParseDate("1999-12-31")))
	day.Check(&v, "", "day", dql.Must(civil.ParseDate("2000-01-02")))
	if len(v) != 2 || v[0].Field != "price" || v[0].Max != nil || v[1].Field != "day" || v[1].Min != nil {
		t.Fatalf("issues %+v", v)
	}
}

func TestDigitsCheck(t *testing.T) {
	c := dql.DigitsCheck{Constraint: "c", Precision: 5, Scale: 2}
	var v dql.ValidationErrors
	c.Check(&v, "", "total", dec.MustParse("999.994")) // rounds to 999.99
	c.Check(&v, "", "total", dec.MustParse("999.995")) // rounds to 1000.00
	if len(v) != 1 || v[0].Precision != 5 || v[0].Scale != 2 || v[0].Kind != dql.KindDigits {
		t.Fatalf("issues %+v", v)
	}
}

func TestMembershipCheck(t *testing.T) {
	c := dql.MembershipCheck[string]{Constraint: "c", Values: []string{"a", "b"}}
	var v dql.ValidationErrors
	c.Check(&v, "", "s", "b")
	c.Check(&v, "", "s", "c")
	if len(v) != 1 || len(v[0].Values) != 2 || v[0].Values[0] != "a" {
		t.Fatalf("issues %+v", v)
	}
}

func TestMust(t *testing.T) {
	if dql.Must(3, nil) != 3 {
		t.Error("Must changed its value")
	}
	defer func() {
		if recover() == nil {
			t.Error("Must did not panic")
		}
	}()
	dql.Must(0, errors.New("bad"))
}
