package sqlitesql

import (
	"strconv"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Affinity is a column's type affinity, by SQLite's rules on its declared
// type.
type Affinity int

// The affinities.
const (
	AffinityBlob Affinity = iota
	AffinityInteger
	AffinityText
	AffinityReal
	AffinityNumeric
)

// AffinityOf applies SQLite's rules, in their order, to a declared type.
func AffinityOf(declared string) Affinity {
	d := strings.ToUpper(declared)
	switch {
	case strings.Contains(d, "INT"):
		return AffinityInteger
	case strings.Contains(d, "CHAR"), strings.Contains(d, "CLOB"), strings.Contains(d, "TEXT"):
		return AffinityText
	case strings.Contains(d, "BLOB"), d == "":
		return AffinityBlob
	case strings.Contains(d, "REAL"), strings.Contains(d, "FLOA"), strings.Contains(d, "DOUB"):
		return AffinityReal
	}
	return AffinityNumeric
}

// storageClasses are SQLite's, the raw side of every weak type (§8.10).
var storageClasses = []irv1.StorageClass{
	irv1.StorageClass_STORAGE_INTEGER, irv1.StorageClass_STORAGE_REAL,
	irv1.StorageClass_STORAGE_TEXT, irv1.StorageClass_STORAGE_BLOB,
}

// ParseType returns a column's logical type, with its native type: the
// declared type's words, its numbers as modifiers (§8.1, §8.2).
//
// A STRICT table's column holds its type's storage class only: INT and
// INTEGER are int64, REAL float64, TEXT text and BLOB bytes. ANY holds any
// class as stored, so it is weak bytes. Any other column, and every column
// of a table that is not STRICT, is weak in its affinity's logical type
// (§8.10): integer int64, text text, real float64, numeric decimal and blob
// (or no type) bytes; except rowid, a rowid alias, which SQLite only ever
// stores as an integer.
func ParseType(declared string, strict, rowid bool) *irv1.Type {
	name, mods, _ := strings.Cut(declared, "(")
	native := &irv1.NativeType{Name: &irv1.QualifiedName{Object: strings.ToLower(strings.TrimSpace(name))}, Raw: declared}
	for _, m := range strings.Split(strings.TrimSuffix(strings.TrimSpace(mods), ")"), ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(m), 10, 32); err == nil {
			native.Modifiers = append(native.Modifiers, &irv1.TypeModifier{Kind: &irv1.TypeModifier_Int{Int: int32(n)}})
		}
	}
	empty := &irv1.Empty{}
	logical := map[Affinity]*irv1.Type{
		AffinityInteger: {Kind: &irv1.Type_Int64{Int64: empty}},
		AffinityText:    {Kind: &irv1.Type_Text{Text: &irv1.Text{}}},
		AffinityReal:    {Kind: &irv1.Type_Float64{Float64: empty}},
		AffinityNumeric: {Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}},
		AffinityBlob:    {Kind: &irv1.Type_Bytes{Bytes: &irv1.Bytes{}}},
	}
	if rowid {
		t := logical[AffinityInteger]
		t.Native = native
		return t
	}
	if strict {
		var a Affinity = -1
		switch strings.ToUpper(declared) {
		case "INT", "INTEGER":
			a = AffinityInteger
		case "REAL":
			a = AffinityReal
		case "TEXT":
			a = AffinityText
		case "BLOB":
			a = AffinityBlob
		}
		if t, ok := logical[a]; ok {
			t.Native = native
			return t
		}
	}
	affinity := AffinityOf(declared)
	if strict {
		// ANY, the one other type STRICT allows, converts nothing.
		affinity = AffinityBlob
	}
	return &irv1.Type{Kind: &irv1.Type_Weak{Weak: &irv1.Weak{Affinity: logical[affinity], StorageClasses: storageClasses}}, Native: native}
}
