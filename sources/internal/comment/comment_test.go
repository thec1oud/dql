package comment

import (
	"reflect"
	"testing"
)

func TestParseComment(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       []Token
	}{
		{"empty", "", nil},
		{"prose only", "Users of the shop.", nil},
		// §1.3 and §7.5.
		{"two flags", "@write-only @sensitive", []Token{
			{Key: "write-only", Start: 0, End: 11},
			{Key: "sensitive", Start: 12, End: 22},
		}},
		{"value", "@logical-key(id)", []Token{
			{Key: "logical-key", HasValue: true, Value: "id", Start: 0, End: 16},
		}},
		// §7.6.
		{"words value", "@inverse-name(user_profile)", []Token{
			{Key: "inverse-name", HasValue: true, Value: "user_profile", Start: 0, End: 27},
		}},
		{"text around tokens", "Hashed with argon2. @write-only, never logged @sensitive.", []Token{
			{Key: "write-only", Start: 20, End: 31},
			{Key: "sensitive", Start: 46, End: 56},
		}},
		{"newline and tab separate", "first\n@a\t@b", []Token{
			{Key: "a", Start: 6, End: 8},
			{Key: "b", Start: 9, End: 11},
		}},
		{"unicode space separates", "x @a", []Token{{Key: "a", Start: 3, End: 5}}},
		{"plugin key", "@x.go.type(string)", []Token{
			{Key: "x.go.type", HasValue: true, Value: "string", Start: 0, End: 18},
		}},
		{"dot without letter ends the key", "@public. @a.1 @b..c", []Token{
			{Key: "public", Start: 0, End: 7},
			{Key: "a", Start: 9, End: 11},
			{Key: "b", Start: 14, End: 16},
		}},
		{"digits and underscores", "@v2_key-x", []Token{{Key: "v2_key-x", Start: 0, End: 9}}},
		{"case is kept", "@Public", []Token{{Key: "Public", Start: 0, End: 7}}},
		{"commas inside a value", "@logical-key(tenant_id, id)", []Token{
			{Key: "logical-key", HasValue: true, Value: "tenant_id, id", Start: 0, End: 27},
		}},
		{"nested parentheses", "@as(decimal(12, 2)) @next", []Token{
			{Key: "as", HasValue: true, Value: "decimal(12, 2)", Start: 0, End: 19},
			{Key: "next", Start: 20, End: 25},
		}},
		{"value is kept exactly", "@k( a b )", []Token{{Key: "k", HasValue: true, Value: " a b ", Start: 0, End: 9}}},
		{"empty value", "@k()", []Token{{Key: "k", HasValue: true, Value: "", Start: 0, End: 4}}},
		{"no value after a space", "@k (v)", []Token{{Key: "k", Start: 0, End: 2}}},
		{"unclosed value", "@logical-key(id @public", []Token{
			{Key: "logical-key", HasValue: true, Value: "id @public", Unclosed: true, Start: 0, End: 23},
		}},
		{"stray at signs", "@ @@ @1 @-x trailing @", nil},
		{"email in prose", "Contact a@b.com or ops@example.org.", nil},
		{"token after an email", "mail a@b.com @sensitive", []Token{{Key: "sensitive", Start: 13, End: 23}}},
		{"at sign after a value is prose", "@a(b)@c", []Token{{Key: "a", HasValue: true, Value: "b", Start: 0, End: 5}}},
		{"repeated keys", "@a @a(1)", []Token{
			{Key: "a", Start: 0, End: 2},
			{Key: "a", HasValue: true, Value: "1", Start: 3, End: 8},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseComment(tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseComment(%q)\n got  %+v\n want %+v", tc.text, got, tc.want)
			}
			for _, tok := range got {
				if tok.Start < 0 || tok.End > len(tc.text) || tc.text[tok.Start] != '@' {
					t.Errorf("token %+v does not locate an @ in %q", tok, tc.text)
				}
			}
		})
	}
}
