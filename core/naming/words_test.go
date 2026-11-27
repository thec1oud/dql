package naming

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"unicode"

	"github.com/thec1oud/dql/testkit/determinism"
)

type wordsCase struct {
	stored string
	want   []string
}

var wordsCases = []wordsCase{
	// The table of §4.2, exactly.
	{"display_name", []string{"display", "name"}},
	{"userID", []string{"user", "id"}},
	{"HTTPServer_log", []string{"http", "server", "log"}},
	{"v2_items", []string{"v2", "items"}},
	{"Order Lines", []string{"order", "lines"}},

	// Rule 1: every non-alphanumeric rune separates; runs and edges add nothing.
	{"posts_author_id_fkey", []string{"posts", "author", "id", "fkey"}}, // §4.1
	{"user.id", []string{"user", "id"}},
	{"a-b$c/d\te", []string{"a", "b", "c", "d", "e"}},
	{"__a--b__", []string{"a", "b"}},
	{"", nil},
	{"_-. ", nil},

	// Rule 2.
	{"displayName", []string{"display", "name"}},
	{"HTTPServer", []string{"http", "server"}},
	{"aB", []string{"a", "b"}},
	{"ABc", []string{"a", "bc"}},
	{"ID", []string{"id"}},
	{"Users", []string{"users"}},
	{"getHTTPResponseCode", []string{"get", "http", "response", "code"}},

	// Rule 3: digits stay with the letters before them and continue their case.
	{"address2", []string{"address2"}},
	{"v2items", []string{"v2items"}},
	{"V2Items", []string{"v2", "items"}},
	{"item2Name", []string{"item2", "name"}},
	{"HTTP2Server", []string{"http2", "server"}},
	{"user2ID", []string{"user2", "id"}},
	{"ID2", []string{"id2"}},
	// Leading digits start a word; letters after them stay in it.
	{"2fa_code", []string{"2fa", "code"}},
	{"2FA", []string{"2fa"}},
	{"user_2fa", []string{"user", "2fa"}},
	{"2024_sales", []string{"2024", "sales"}},
	{"x²", []string{"x²"}},
	{"chapterⅧ", []string{"chapterⅷ"}}, // a letter number attaches like a digit

	// Rule 4: simple case folding, never locale-dependent.
	{"straße", []string{"straße"}},     // simple folding keeps ß
	{"STRASSE", []string{"strasse"}},   // and so does not equate it with ss
	{"STRAẞE", []string{"straße"}},     // capital sharp s folds to ß
	{"İstanbul", []string{"İstanbul"}}, // İ has no simple folding
	{"ISTANBUL", []string{"istanbul"}}, // I folds to i, also in Turkish
	{"ılık", []string{"ılık"}},         // dotless ı has no simple folding
	{"IŞIK", []string{"işik"}},
	{"Kelvin", []string{"kelvin"}}, // Kelvin sign
	{"ſtate", []string{"state"}},   // long s
	{"ΣΟΦΟΣ", []string{"σοφοσ"}},
	{"σοφος", []string{"σοφοσ"}}, // final sigma folds to σ
	{"ꭰꭱ", []string{"ᎠᎡ"}},       // Cherokee folds to the capitals
	{"ǅemal", []string{"ǆemal"}}, // a title-case letter counts as upper case
	{"aǅemal", []string{"a", "ǆemal"}},

	// Letters without case and combining marks.
	{"顧客_ID", []string{"顧客", "id"}},
	{"ユーザー名", []string{"ユーザー名"}},
	{"日本Name", []string{"日本name"}},
	{"नाम_सूची", []string{"नाम", "सूची"}},
	{"café_menu", []string{"café", "menu"}},
	{"́x", []string{"́x"}},
	{"AB́c", []string{"a", "b́c"}},
}

func TestWords(t *testing.T) {
	for _, c := range wordsCases {
		if got := Words(c.stored); !slices.Equal(got, c.want) {
			t.Errorf("Words(%q) = %q, want %q", c.stored, got, c.want)
		}
	}
}

// fold must agree with Unicode simple case folding on every rune: it picks one
// member of each SimpleFold orbit, and the same member for the whole orbit.
// The member it picks is pinned by the cases above.
func TestFoldPicksOneMemberPerOrbit(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		f := foldRune(r)
		same := f == r
		for s := unicode.SimpleFold(r); s != r; s = unicode.SimpleFold(s) {
			same = same || s == f
			if g := foldRune(s); g != f {
				t.Fatalf("%U folds to %U but %U, in its orbit, folds to %U", r, f, s, g)
			}
		}
		if !same {
			t.Fatalf("%U folds to %U, outside its orbit", r, f)
		}
	}
}

// Word splitting is locale-independent (§3.2 rule 4): the test binary prints
// every case's words under each environment of determinism.Environments,
// Turkish among them, and the outputs must be identical.
func TestWordsIgnoresLocale(t *testing.T) {
	const helper = "DQL_NAMING_PRINT_WORDS=1"
	if os.Getenv("DQL_NAMING_PRINT_WORDS") == "1" {
		for _, c := range wordsCases {
			fmt.Printf("%q\n", Words(c.stored))
		}
		return
	}
	var envs [][]string
	for _, env := range determinism.Environments {
		envs = append(envs, append(slices.Clone(env), helper))
	}
	if err := determinism.Command(envs, os.Args[0], "-test.run=^TestWordsIgnoresLocale$"); err != nil {
		t.Fatal(err)
	}
}
