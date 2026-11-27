package diagnostics

import (
	"slices"
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func qn(parts ...string) *irv1.QualifiedName {
	parts = append(parts, "", "", "", "")
	return &irv1.QualifiedName{Catalog: parts[0], Namespace: parts[1], Object: parts[2], Member: parts[3]}
}

func TestSort(t *testing.T) {
	ds := []*irv1.Diagnostic{
		Warningf("b", qn("shop", "app", "users"), "w"),
		Errorf("z", qn("shop", "app", "users", "id"), "e"),
		Errorf("a", qn("shop", "app", "users", "id"), "e"),
		Errorf("a", qn("shop", "app", "posts"), "e%d", 2),
		Errorf("a", qn("shop", "app", "posts"), "e1"),
		{Severity: irv1.Severity_SEVERITY_ERROR, Code: "a", Target: qn("shop", "app", "posts"), Shape: "s", Message: "e0"},
		{Severity: irv1.Severity_SEVERITY_ERROR, Code: "a", Target: qn("shop", "app", "posts"), Query: "q", Message: "e0"},
		Errorf("a", qn("billing"), "e"),
		Errorf("a", qn("shop"), "e"),
	}
	Sort(ds)
	var got []string
	for _, d := range ds {
		got = append(got, Name(d.GetTarget())+" "+d.GetCode()+" "+d.GetShape()+d.GetQuery()+d.GetMessage())
	}
	want := []string{
		"billing a e",
		"shop a e",
		"shop.app.posts a e1",
		"shop.app.posts a e2",
		"shop.app.posts a qe0",
		"shop.app.posts a se0",
		"shop.app.users.id a e",
		"shop.app.users.id z e",
		"shop.app.users b w",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("order:\n got %q\nwant %q", got, want)
	}
	if ds[0].GetSeverity() != irv1.Severity_SEVERITY_ERROR || ds[8].GetSeverity() != irv1.Severity_SEVERITY_WARNING {
		t.Error("errors must sort before warnings")
	}
}

func TestCompareNamesByParts(t *testing.T) {
	// "a.b" as one part must not compare equal to "a" then "b".
	if CompareNames(qn("a", "b"), qn("a.b")) >= 0 {
		t.Error("names must compare part by part")
	}
	if CompareNames(qn("x", "y", "z", "m"), qn("x", "y", "z", "m")) != 0 {
		t.Error("equal names must compare equal")
	}
}

func TestName(t *testing.T) {
	if got := Name(qn("billing", "", "invoices", "user")); got != "billing.invoices.user" {
		t.Errorf("Name = %q", got)
	}
	if got := Name(nil); got != "" {
		t.Errorf("Name(nil) = %q", got)
	}
}
