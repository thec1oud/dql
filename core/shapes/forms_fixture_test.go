package shapes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"

	// The catalog IR carries Postgres extensions.
	_ "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
)

// The forms are derived from flat presets. These fixtures write the flat
// presets of the running example (§1.3) as §14.2 and §14.3 give them, one
// entry per column: its Full, Draft and Partial presence, R for required, O
// for optional or a forbidden reason, then K when it is in the Key and V when
// it is the Key's expected version. A dash means the entity has no such
// preset, as for a view.
var shopFlats = map[string][]string{
	"tenants": {
		"id R O KEY K",
		"name R R O",
	},
	"users": {
		"id R IDENTITY_ALWAYS KEY K",
		"tenant_id R BOUND BOUND",
		"email R R O",
		"display_name R O O",
		"password_hash WRITE_ONLY R O",
		"created_at R O IMMUTABLE",
		"updated_at R O DB_UPDATED",
		"version R O VERSION V",
	},
	"profiles": {
		"user_id R R KEY K",
		"bio R O O",
		"birthday R O O",
	},
	"posts": {
		"id R O KEY K",
		"author_id R R O",
		"editor_id R O O",
		"title R R O",
		"slug R GENERATED GENERATED",
		"status R O O",
		"price R O O",
		"labels R O O",
		"published_at R O O",
	},
	"tags": {
		"id R IDENTITY_ALWAYS KEY K",
		"name R R O",
	},
	"post_tags": {
		"post_id R R KEY K",
		"tag_id R R KEY K",
	},
	"published_posts": {
		"id R - - K",
		"title R - -",
		"author_id R - -",
		"published_at R - -",
	},
}

// shopCatalogs reads the running example's catalog IR, with its facts
// derived.
func shopCatalogs(t *testing.T) []*irv1.Catalog {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "shop", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var bun irv1.Bundle
	if err := bundle.UnmarshalJSON(b, &bun); err != nil {
		t.Fatal(err)
	}
	return bun.GetCatalogs()
}

// flatShapes builds the flat presets and Keys that spec describes for the
// tables of catalogs.
func flatShapes(t *testing.T, catalogs []*irv1.Catalog, spec map[string][]string) []*irv1.Shape {
	t.Helper()
	var out []*irv1.Shape
	for _, c := range catalogs {
		for _, ns := range c.GetNamespaces() {
			for _, tab := range ns.GetTables() {
				lines, ok := spec[tab.GetName().GetStored()]
				if !ok {
					continue
				}
				qn := &irv1.QualifiedName{Catalog: c.GetName().GetStored(), Namespace: ns.GetName().GetStored(), Object: tab.GetName().GetStored()}
				out = append(out, entityFlats(t, qn, tab, lines)...)
			}
		}
	}
	return out
}

func entityFlats(t *testing.T, qn *irv1.QualifiedName, tab *irv1.Table, lines []string) []*irv1.Shape {
	t.Helper()
	words := tab.GetHostName().GetWords()
	if len(words) == 0 {
		words = tab.GetName().GetWords()
	}
	presets := []irv1.Preset{irv1.Preset_PRESET_FULL, irv1.Preset_PRESET_DRAFT, irv1.Preset_PRESET_PARTIAL}
	var out []*irv1.Shape
	key := fixtureShape(qn, words, irv1.Preset_PRESET_KEY, irv1.Form_FORM_UNSPECIFIED)
	for i, p := range presets {
		s := fixtureShape(qn, words, p, irv1.Form_FORM_FLAT)
		for _, l := range lines {
			f := strings.Fields(l)
			col := fixtureColumn(t, tab, f[0])
			switch f[1+i] {
			case "-":
				s = nil
			case "R", "O":
				fld := &irv1.Field{
					Column: f[0], Presence: irv1.Presence_PRESENCE_REQUIRED, Nullable: col.GetNullable(),
					NullabilitySource: col.GetNullabilitySource(), Type: col.GetType(),
				}
				if f[1+i] == "O" {
					fld.Presence = irv1.Presence_PRESENCE_OPTIONAL
				}
				if p != irv1.Preset_PRESET_FULL {
					fld.ValueKind = irv1.ValueKind_VALUE_KIND_VALUE_OR_EXPRESSION
				}
				s.Fields = append(s.Fields, fld)
			default:
				r, ok := irv1.ForbiddenReason_value["FORBIDDEN_"+f[1+i]]
				if !ok {
					t.Fatalf("fixture %s: unknown reason %s", l, f[1+i])
				}
				s.Forbidden = append(s.Forbidden, &irv1.ForbiddenField{Column: f[0], Reason: irv1.ForbiddenReason(r)})
				if r == int32(irv1.ForbiddenReason_FORBIDDEN_BOUND) {
					s.Scope = append(s.Scope, f[0])
				}
			}
			if s == nil {
				break
			}
		}
		if s != nil {
			out = append(out, s)
		}
	}
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) < 5 {
			continue
		}
		col := fixtureColumn(t, tab, f[0])
		fld := &irv1.Field{Column: f[0], Presence: irv1.Presence_PRESENCE_REQUIRED, NullabilitySource: col.GetNullabilitySource(), Type: col.GetType()}
		if f[4] == "V" {
			fld.Role = irv1.FieldRole_FIELD_ROLE_EXPECTED_VERSION
		}
		key.Fields = append(key.Fields, fld)
	}
	if len(key.Fields) == 0 {
		return out // a keyless entity
	}
	return append(out, key)
}

func fixtureShape(qn *irv1.QualifiedName, words []string, p irv1.Preset, form irv1.Form) *irv1.Shape {
	return &irv1.Shape{
		Id:       &irv1.ShapeId{Base: &irv1.ShapeId_Entity{Entity: qn}, Preset: p, Form: form},
		HostName: &irv1.Identifier{Words: append(append([]string(nil), words...), presetName(p))},
		Entity:   qn,
		Lineage:  []*irv1.LineageStep{{Op: irv1.ShapeOp_PRESET, Entity: qn, Preset: p, Form: form}},
	}
}

func fixtureColumn(t *testing.T, tab *irv1.Table, name string) *irv1.Column {
	t.Helper()
	for _, c := range tab.GetColumns() {
		if c.GetName().GetStored() == name {
			return c
		}
	}
	t.Fatalf("fixture: %s has no column %s", tab.GetName().GetStored(), name)
	return nil
}
