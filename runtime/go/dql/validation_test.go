package dql_test

import (
	"errors"
	"testing"

	"github.com/thec1oud/dql/runtime/go/dql"
)

func TestValidationErrors(t *testing.T) {
	// §10.5: PostDraft{AuthorID: 7, Title: ""}.Validate()
	errs := dql.ValidationErrors{
		{Field: "title", Kind: dql.KindLength,
			Constraint: "shop.app.posts.posts_title_check",
			Min:        1, Max: 200, Unit: dql.UnitCharacters, Got: 0},
	}
	err := errs.Err()
	if err == nil {
		t.Fatal("Err() of one issue is nil")
	}
	var got dql.ValidationErrors
	if !errors.As(err, &got) || len(got) != 1 || got[0].Max != 200 {
		t.Fatalf("errors.As = %v", got)
	}
	if dql.ClassOf(err) != dql.ClassValidationErrors {
		t.Errorf("class = %q", dql.ClassOf(err))
	}
	want := "dql: ValidationErrors: title: length 0 characters, want [1, 200] (shop.app.posts.posts_title_check)"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	if dql.ValidationErrors(nil).Err() != nil || (dql.ValidationErrors{}).Err() != nil {
		t.Error("Err() of no issues is not nil")
	}
}

func TestIssueString(t *testing.T) {
	tests := []struct {
		issue dql.ValidationIssue
		want  string
	}{
		{dql.ValidationIssue{Field: "title", Kind: dql.KindLength, Max: 200, Unit: dql.UnitBytes, Got: 201},
			"title: length 201 bytes, want (-∞, 200]"},
		{dql.ValidationIssue{Field: "n", Kind: dql.KindLength, Min: 2, Unit: dql.UnitUTF16CodeUnits, Got: 1},
			"n: length 1 utf16_code_units, want [2, ∞)"},
		{dql.ValidationIssue{Field: "price", Kind: dql.KindRange, Min: 0, Max: 100, MaxInclusive: true},
			"price: out of range (0, 100]"},
		{dql.ValidationIssue{Field: "price", Kind: dql.KindRange, Min: 0, MinInclusive: true, Constraint: "c"},
			"price: out of range [0, ∞) (c)"},
		{dql.ValidationIssue{Field: "total", Kind: dql.KindDigits, Precision: 12, Scale: 2},
			"total: does not fit decimal(12,2)"},
		{dql.ValidationIssue{Field: "status", Kind: dql.KindMembership, Values: []any{"a", "b"}},
			"status: not one of 2 listed values"},
		{dql.ValidationIssue{Field: "name", Kind: dql.KindNotEmpty},
			"name: empty"},
		{dql.ValidationIssue{Field: "slug", Kind: dql.KindPattern, Pattern: "^[a-z]+$"},
			`slug: does not match "^[a-z]+$"`},
		{dql.ValidationIssue{Field: "author_id", Kind: dql.KindRelationConflict, Other: "author"},
			"author_id: disagrees with author"},
		{dql.ValidationIssue{Field: "author_id", Kind: dql.KindRelationMissing, Other: "author"},
			"author_id: required, through it or author"},
		{dql.ValidationIssue{Field: "published_at", Kind: dql.KindFieldSetTwice},
			"published_at: set by both the payload and an expression"},
		{dql.ValidationIssue{Field: "x", Kind: "future_kind"},
			"x: future_kind"},
		{dql.ValidationIssue{Shape: "shop.app.slug_text", Kind: dql.KindPattern, Pattern: "^[a-z]+$", Constraint: "c"},
			`shop.app.slug_text: does not match "^[a-z]+$" (c)`},
		{dql.ValidationIssue{Kind: dql.KindNotEmpty},
			"empty"},
	}
	for _, tt := range tests {
		if got := tt.issue.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestAt(t *testing.T) {
	errs := dql.ValidationErrors{
		{Field: "title", Kind: dql.KindNotEmpty},
		{Field: "author_id", Other: "author", Kind: dql.KindRelationConflict},
		{Field: "[0]", Kind: dql.KindNotEmpty},
		{Field: "", Kind: dql.KindNotEmpty},
	}
	got := errs.At("posts_to_author[1]")
	want := []struct{ field, other string }{
		{"posts_to_author[1].title", ""},
		{"posts_to_author[1].author_id", "posts_to_author[1].author"},
		{"posts_to_author[1][0]", ""},
		{"posts_to_author[1]", ""},
	}
	for i, w := range want {
		if got[i].Field != w.field || got[i].Other != w.other {
			t.Errorf("At: issue %d = %q, %q; want %q, %q", i, got[i].Field, got[i].Other, w.field, w.other)
		}
	}
	if errs[0].Field != "title" {
		t.Error("At changed its receiver")
	}
	if got := errs.At(""); got[0].Field != "title" {
		t.Errorf("At(\"\") = %q", got[0].Field)
	}
}
