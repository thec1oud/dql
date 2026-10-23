package annotate

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestParseList(t *testing.T) {
	for text, want := range map[string][]string{
		"id":                        {"id"},
		"tenant_id, slug":           {"tenant_id", "slug"},
		" tenant_id ,slug ":         {"tenant_id", "slug"},
		`"Order Lines", id`:         {"Order Lines", "id"},
		`"a,b", " c ", "say ""x"""`: {"a,b", " c ", `say "x"`},
		`"k:v"`:                     {"k:v"},
	} {
		got, err := ParseList(text)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("ParseList(%q) = %q, %v; want %q", text, got, err, want)
		}
	}
}

func TestParseListFails(t *testing.T) {
	for text, want := range map[string]string{
		"":        "item 1: it is empty",
		"a,":      "item 2: it is empty",
		"a,,b":    "item 2: it is empty",
		`"a`:      "never closed",
		`a"b"`:    "has a double quote inside",
		`"a"b`:    "continues after its closing quote",
		`a, ""`:   "item 2: it is empty; write a value inside the quotes",
		`"a" "b"`: "continues after its closing quote",
	} {
		if _, err := ParseList(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseList(%q) = %v; want %q", text, err, want)
		}
	}
}

func TestParseStruct(t *testing.T) {
	for text, want := range map[string]map[string]string{
		"to: shop.users.id": {"to": "shop.users.id"},
		"to: shop.users.id, name: user, inverse-name: invoices": {
			"to": "shop.users.id", "name": "user", "inverse-name": "invoices"},
		` to:"a, b" ,name :" x:y "`: {"to": "a, b", "name": " x:y "},
	} {
		got, err := ParseStruct(text)
		if err != nil || !maps.Equal(got, want) {
			t.Errorf("ParseStruct(%q) = %v, %v; want %v", text, got, err, want)
		}
	}
}

func TestParseStructFails(t *testing.T) {
	for text, want := range map[string]string{
		"":                 `field 1, "", has no colon`,
		"to shop.users.id": `field 1, "to shop.users.id", has no colon`,
		": shop.users.id":  "field 1 has no name",
		`"to": a`:          "field name \"to\" is quoted",
		"to:":              "field to: it is empty",
		"to: a, to: b":     "field to is given twice",
		`to: "a`:           "never closed",
		"to: a,":           "field 2",
	} {
		if _, err := ParseStruct(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseStruct(%q) = %v; want %q", text, err, want)
		}
	}
}
