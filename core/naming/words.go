// Package naming computes the word splits that identify every name in the IR
// and that emitters turn into host names (design record §4). Every rule works
// on words, before any emitter casing, so all emitters see the same result.
// Nothing here inflects a word: the core never singularizes or pluralizes
// (§4.4).
package naming

import "unicode"

// class is a rune's role in word splitting.
type class uint8

const (
	separator class = iota // not alphanumeric: ends the current word
	caseless               // a letter without case, or a digit or mark that follows none
	lower
	upper
)

// classify returns the class of r on its own. Digits and marks are reported
// as caseless; Words gives them the class of the letters they follow.
func classify(r rune) class {
	switch {
	case unicode.IsUpper(r) || unicode.IsTitle(r):
		return upper
	case unicode.IsLower(r):
		return lower
	case unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r):
		return caseless
	}
	return separator
}

// Words splits an identifier into lower-case words (§4.2):
//
//  1. It splits on every rune that is not alphanumeric: not a letter, number
//     or combining mark. Marks count as part of the word they follow, so a
//     vowel sign or a decomposed accent never splits or drops a letter.
//  2. It splits where a lower-case letter is followed by an upper-case one
//     (displayName), and before the last capital of an upper-case run that is
//     followed by a lower-case letter (HTTPServer).
//  3. Digits stay attached to the letters before them (v2, address2). A digit
//     continues the case of those letters, so item2Name splits as item2, name
//     and HTTP2Server as http2, server, while v2items stays one word. Leading
//     digits start a word (2fa).
//  4. Every word is folded with Unicode simple case folding (see fold).
//
// The result depends only on the identifier, never on the process locale
// (§3.2 rule 4). An identifier with no alphanumeric runes has no words.
func Words(identifier string) []string {
	rs := []rune(identifier)
	var words []string
	var word []rune
	var prev class // the effective class of the last rune in word
	flush := func() {
		if len(word) > 0 {
			words = append(words, fold(string(word)))
			word = word[:0]
		}
	}
	for i, r := range rs {
		c := classify(r)
		switch {
		case c == separator:
			flush()
			continue
		case c == upper:
			// Only a capital starts a new word inside a run of alphanumerics.
			if prev == lower || (prev == upper && nextIsLower(rs[i+1:])) {
				flush()
			}
		case c == caseless && !unicode.IsLetter(r) && len(word) > 0:
			// Digits and marks take the case of what they follow (rule 3).
			c = prev
		}
		word = append(word, r)
		prev = c
	}
	flush()
	return words
}

// nextIsLower reports whether the first rune of rs that is not a combining
// mark is a lower-case letter, so that a decomposed accent on the last
// capital of a run does not hide the letter after it.
func nextIsLower(rs []rune) bool {
	for _, r := range rs {
		if !unicode.IsMark(r) {
			return classify(r) == lower
		}
	}
	return false
}

// fold applies Unicode simple case folding (the C and S mappings of
// CaseFolding.txt) to every rune of s.
//
// Simple folding differs from strings.ToLower, which applies the lower-case
// mapping of UnicodeData.txt, in ways that matter for identity: ToLower
// leaves ſ, ς and ϐ unchanged although they fold to s, σ and β; it maps
// İ (U+0130) to i, a Turkic-specific mapping that simple folding omits, so
// İ stays İ and dotless ı stays ı; and it maps Cherokee capitals to the small
// letters, while folding maps the small letters to the capitals. Folding is
// what makes two spellings the same word, and it is locale-independent:
// I always folds to i (§3.2 rule 4).
func fold(s string) string {
	rs := []rune(s)
	for i, r := range rs {
		rs[i] = foldRune(r)
	}
	return string(rs)
}

// foldRune returns the simple case folding of r. unicode.SimpleFold walks the
// orbit of runes that are equal under simple case folding; a rune alone in
// its orbit folds to itself. Every larger orbit folds to the lower-case form
// of its upper-case member, except Cherokee, whose orbits fold to the
// upper-case member because the small letters were added to Unicode after
// the capitals and folding is kept stable. This matches every C and S entry
// of CaseFolding.txt for the Unicode version of the Go standard library.
func foldRune(r rune) rune {
	switch {
	case unicode.SimpleFold(r) == r:
		return r
	case unicode.Is(unicode.Cherokee, r):
		return unicode.ToUpper(r)
	}
	return unicode.ToLower(unicode.ToUpper(r))
}
