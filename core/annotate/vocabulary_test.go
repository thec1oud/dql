package annotate

import (
	"slices"
	"strings"
	"testing"
)

func TestVocabularyIsSortedAndComplete(t *testing.T) {
	v := Vocabulary()
	var names []string
	for _, k := range v {
		names = append(names, k.Name)
		if len(k.Applies) == 0 || k.Target == "" || k.Form == 0 || k.See == "" {
			t.Errorf("%s is incomplete: %+v", k.Name, k)
		}
		if (k.Form == FormChoice) != (len(k.Choices) > 0) || (k.Form == FormStruct) != (len(k.Fields) > 0) {
			t.Errorf("%s: choices or fields do not match its form", k.Name)
		}
	}
	// Every key §7.2 defines.
	want := []string{"array-elements-nonnull", "as", "bool", "bound", "collection-policy", "db-filled",
		"db-updated", "enum", "immutable", "inverse-name", "logical-key", "name", "nonnull", "public",
		"read-only", "relation", "relation-name", "removal", "sensitive", "soft-delete", "version", "write-only"}
	if !slices.Equal(names, want) {
		t.Errorf("vocabulary = %q", names)
	}
	v[0].Name = "changed"
	if Vocabulary()[0].Name == "changed" {
		t.Error("Vocabulary returns the package's own slice")
	}
}

func TestLookup(t *testing.T) {
	k, ok := Lookup("core.write-only")
	if !ok || k.Name != "write-only" || k.IR() != "core.write-only" || !k.Allows(KindColumn) || k.Allows(KindViewColumn) {
		t.Errorf("Lookup(core.write-only) = %+v, %v", k, ok)
	}
	for _, key := range []string{"write-only", "core.write-once", "x.go.type", "core."} {
		if _, ok := Lookup(key); ok {
			t.Errorf("Lookup(%q) found a key", key)
		}
	}
}

func TestIRKey(t *testing.T) {
	for written, want := range map[string]string{
		"write-only":     "core.write-only",
		"inverse-name":   "core.inverse-name",
		"x.ts.time-type": "x.ts.time-type",
	} {
		if got := IRKey(written); got != want {
			t.Errorf("IRKey(%q) = %q, want %q", written, got, want)
		}
	}
}

func TestIsPlugin(t *testing.T) {
	for key, want := range map[string]bool{
		"x.go.type":       true,
		"x.ts.time-type":  true,
		"x.go.tag.json":   true,
		"x.go":            false,
		"x..type":         false,
		"x.go.":           false,
		"core.write-only": false,
		"xgo.type":        false,
		"go.type":         false,
	} {
		if got := IsPlugin(key); got != want {
			t.Errorf("IsPlugin(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestKindString(t *testing.T) {
	if KindViewColumn.String() != "view column" || Kind(0).String() != "unknown object" {
		t.Error("kind names")
	}
	for k := KindTable; k <= KindNamespace; k++ {
		if k.String() == "unknown object" {
			t.Errorf("kind %d has no name", k)
		}
	}
}

// as takes the scalar logical types of §8.1 and §8.8.
func TestLogicalTypes(t *testing.T) {
	k, _ := Lookup("core.as")
	for _, name := range []string{"bool", "int64", "uint8", "decimal", "text", "local_time", "instant_with_offset", "date", "uuid", "jsonb"} {
		if !slices.Contains(k.Choices, name) {
			t.Errorf("as does not take %s", name)
		}
	}
	for _, name := range structured {
		if slices.Contains(k.Choices, name) {
			t.Errorf("as takes %s", name)
		}
	}
	if !slices.IsSorted(k.Choices) || strings.Join(k.Choices, "") == "" {
		t.Error("choices are not sorted")
	}
	if typeName(nil) != "" || typeName(int32Type) != "int32" {
		t.Error("typeName")
	}
}
