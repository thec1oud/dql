package annotate

import (
	"fmt"
	"strings"
)

// A source writes a value as the text it read (§7.1): the inside of the
// parentheses of @key(value) in a comment. The core reads that text by the
// key's form. A list is written as in @logical-key(id) (§1.3): items
// separated by commas, @logical-key(tenant_id, slug). A struct is written as
// config writes one inside its braces (§5.3): fields separated by commas,
// each a field name, a colon and a value, @relation(to: shop.users.id, name:
// user). Spaces around an item, a field name or a value are not part of it.
// An item or a value may be written in double quotes, with "" for a quote
// inside, to hold a comma, a colon, a quote or spaces at its ends, since
// stored names may hold any character (§4.1).

// ParseList reads a list value written as text.
func ParseList(text string) ([]string, error) {
	items, err := split(text)
	if err != nil {
		return nil, err
	}
	var out []string
	for i, it := range items {
		v, err := item(it)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i+1, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// ParseStruct reads a struct value written as text, as field names and
// their values.
func ParseStruct(text string) (map[string]string, error) {
	items, err := split(text)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i, it := range items {
		name, value, ok := cutColon(it)
		name = strings.TrimSpace(name)
		switch {
		case !ok:
			return nil, fmt.Errorf("field %d, %q, has no colon; write it as name: value", i+1, strings.TrimSpace(it))
		case name == "":
			return nil, fmt.Errorf("field %d has no name before its colon; write it as name: value", i+1)
		case strings.ContainsRune(name, '"'):
			return nil, fmt.Errorf("field name %s is quoted; write field names without quotes", name)
		}
		v, err := item(value)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", name, err)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("field %s is given twice", name)
		}
		out[name] = v
	}
	return out, nil
}

// split cuts text at the commas outside double quotes. An empty text is one
// empty item.
func split(text string) ([]string, error) {
	var items []string
	quoted, start := false, 0
	for i := range len(text) {
		switch {
		case text[i] == '"':
			quoted = !quoted
		case text[i] == ',' && !quoted:
			items = append(items, text[start:i])
			start = i + 1
		}
	}
	if quoted {
		return nil, fmt.Errorf("a double quote is never closed")
	}
	return append(items, text[start:]), nil
}

// cutColon cuts an item at its first colon outside double quotes.
func cutColon(it string) (before, after string, found bool) {
	quoted := false
	for i := range len(it) {
		switch {
		case it[i] == '"':
			quoted = !quoted
		case it[i] == ':' && !quoted:
			return it[:i], it[i+1:], true
		}
	}
	return it, "", false
}

// item reads one item or value: trimmed, and unquoted when it is written in
// double quotes. split has checked that every quote is closed.
func item(it string) (string, error) {
	it = strings.TrimSpace(it)
	if it == "" {
		return "", fmt.Errorf("it is empty; remove the extra comma or write a value")
	}
	if !strings.HasPrefix(it, `"`) {
		if strings.ContainsRune(it, '"') {
			return "", fmt.Errorf("%s has a double quote inside; quote the whole value, writing \"\" for a quote", it)
		}
		return it, nil
	}
	// The quote closing the value is the first one not doubled.
	var b strings.Builder
	for i := 1; i < len(it); i++ {
		if it[i] != '"' {
			b.WriteByte(it[i])
			continue
		}
		if i+1 < len(it) && it[i+1] == '"' {
			b.WriteByte('"')
			i++
			continue
		}
		if rest := it[i+1:]; rest != "" {
			return "", fmt.Errorf("%s continues after its closing quote; quote the whole value", it)
		}
		break
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("it is empty; write a value inside the quotes")
	}
	return b.String(), nil
}
