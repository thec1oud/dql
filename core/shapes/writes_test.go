package shapes

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// customDraft is a custom shape of entity's Draft that keeps only keep, as
// the custom-shape operators would derive it (§14.8).
func customDraft(t *testing.T, n *Nester, name string, entity string, keep ...string) *irv1.Shape {
	t.Helper()
	base := n.preset(edgeQN(entity), irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT)
	s := proto.Clone(base).(*irv1.Shape)
	s.Id = &irv1.ShapeId{Base: &irv1.ShapeId_Custom{Custom: name}}
	s.HostName = &irv1.Identifier{Words: strings.Split(name, "_")}
	s.Lineage = append(s.Lineage, &irv1.LineageStep{Op: irv1.ShapeOp_PICK, Columns: keep})
	s.Fields = nil
	for _, f := range base.GetFields() {
		if strings.Contains(" "+strings.Join(keep, " ")+" ", " "+f.GetColumn()+" ") {
			s.Fields = append(s.Fields, f)
		} else {
			s.Forbidden = append(s.Forbidden, &irv1.ForbiddenField{Column: f.GetColumn(), Reason: irv1.ForbiddenReason_FORBIDDEN_OMITTED})
		}
	}
	return s
}

func relationOf(t *testing.T, cats []*irv1.Catalog, entity, name string) *irv1.Relation {
	t.Helper()
	for _, tab := range cats[0].GetNamespaces()[0].GetTables() {
		if tab.GetName().GetStored() != entity {
			continue
		}
		for _, r := range tab.GetRelations() {
			if r.GetName().GetStored() == name {
				return r
			}
		}
	}
	t.Fatalf("no relation %s.%s", entity, name)
	return nil
}

// A custom Draft that drops a NOT NULL foreign key and nests its relation
// must be given the relation, and its Ref holds the custom target.
func TestNestWriteIntoCustomShape(t *testing.T) {
	cats := edgeCatalog()
	n := NewNester(cats, flatShapes(t, cats, edgeFlats))
	shape := customDraft(t, n, "cc_pick", "cc", "id")
	// A nest and a forbidden entry for the relation already there are replaced.
	shape.Nests = []*irv1.Nest{{Relation: "a"}}
	shape.Forbidden = append(shape.Forbidden, &irv1.ForbiddenField{Relation: "a", Reason: irv1.ForbiddenReason_FORBIDDEN_OMITTED})
	target := customDraft(t, n, "a_pick", "a", "id")
	added, unions, diag := n.NestWrite(shape, relationOf(t, cats, "cc", "a"), target)
	if diag != nil || len(added) != 0 {
		t.Fatalf("added %v, diag %v", added, diag)
	}
	if len(shape.GetNests()) != 1 {
		t.Fatalf("nests = %v", shape.GetNests())
	}
	nest := shape.GetNests()[0]
	if nest.GetPresence() != irv1.Presence_PRESENCE_REQUIRED || len(nest.GetSupplies()) != 0 || nest.GetNeedsOne() {
		t.Errorf("nest = %v, want a required relation with no columns to supply it", nest)
	}
	if got, want := forbiddenNames(shape), "a_id=OMITTED"; got != want {
		t.Errorf("forbidden = %q, want %q", got, want)
	}
	if len(unions) != 1 {
		t.Fatalf("unions = %v", unions)
	}
	u := unions[0]
	if got := strings.Join(u.GetHostName().GetWords(), " "); got != "cc pick a ref" || u.GetId().GetCustom() != "cc_pick" {
		t.Errorf("union %q, custom %q", got, u.GetId().GetCustom())
	}
	if got, want := variantWords(u), "cc pick a ref link, cc pick a ref create"; got != want {
		t.Errorf("variants = %q, want %q", got, want)
	}
	if got := u.GetVariants()[1].GetPayload().GetCustom(); got != "a_pick" {
		t.Errorf("create payload = %q, want a_pick", got)
	}
}

// A preset nesting a custom target names its union after the target; a
// dependent child's custom shape loses the forward relation's nest.
func TestNestWriteCustomTargetsOfAPreset(t *testing.T) {
	cats := edgeCatalog()
	n := NewNester(cats, flatShapes(t, cats, edgeFlats))
	shape := formsDerive(n.preset(edgeQN("a"), irv1.Preset_PRESET_DRAFT, irv1.Form_FORM_FLAT), irv1.Form_FORM_NESTED)
	_, unions, _ := n.NestWrite(shape, relationOf(t, cats, "a", "b"), customDraft(t, n, "b_pick", "b", "id"))
	if got := strings.Join(unions[0].GetHostName().GetWords(), " "); got != "b pick b ref" || unions[0].GetId().GetCustom() != "b_pick" {
		t.Errorf("union %q, custom %q", got, unions[0].GetId().GetCustom())
	}

	child := customDraft(t, n, "cc_slim", "cc", "id", "a_id")
	child.Nests = []*irv1.Nest{{Relation: "a"}}
	added, _, _ := n.NestWrite(shape, relationOf(t, cats, "a", "cs"), child)
	if len(added) != 1 {
		t.Fatalf("added = %v", added)
	}
	s := added[0]
	if n.spell(s.GetId()) != "cc_slim.for.a" || len(s.GetNests()) != 0 || forbiddenNames(s) != "a_id=PARENT" {
		t.Errorf("child = %s, nests %v, forbidden %q", n.spell(s.GetId()), s.GetNests(), forbiddenNames(s))
	}
	if got := strings.Join(s.GetHostName().GetWords(), " "); got != "cc slim for a" {
		t.Errorf("child words = %q", got)
	}
}
