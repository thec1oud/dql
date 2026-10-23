package pgsql

import (
	"testing"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func typ(js string) *irv1.Type {
	t := &irv1.Type{}
	mustUnmarshal(js, t)
	return t
}

// columns of app.posts (§1.3) and a few more for the cases it lacks.
var postColumns = map[string]*irv1.Type{
	"title":        typ(`{"text": {}}`),
	"display_name": typ(`{"varchar": {"length": 80, "unit": "LENGTH_UNIT_CHARACTERS"}}`),
	"code":         typ(`{"char": {"length": 3, "unit": "LENGTH_UNIT_CHARACTERS"}}`),
	"status":       typ(`{"named": {"enum": {"catalog": "shop", "namespace": "app", "object": "post_status"}}}`),
	"email":        typ(`{"named": {"domain": {"catalog": "shop", "namespace": "app", "object": "email_address"}}}`),
	"published_at": typ(`{"instant": {"precision": 6}}`),
	"qty":          typ(`{"int32": {}}`),
	"big":          typ(`{"int64": {}}`),
	"small":        typ(`{"int16": {}}`),
	"price":        typ(`{"decimal": {"precision": 12, "scale": 2}}`),
	"ratio":        typ(`{"float64": {}}`),
	"born":         typ(`{"date": {}}`),
	"at":           typ(`{"localDatetime": {"precision": 6}}`),
	"t":            typ(`{"localTime": {"precision": 6}}`),
	"a":            typ(`{"int32": {}}`),
	"b":            typ(`{"int32": {}}`),
	"c":            typ(`{"int32": {}}`),
}

func portable(columns, kind string) string {
	return `{"kind": "CONSTRAINT_KIND_CHECK", "columns": [` + columns + `], "portable": ` + kind + `, "clientValidated": true}`
}

func length(minMax string) string {
	return `{"length": {` + minMax + `"unit": "LENGTH_UNIT_CHARACTERS"}}`
}

const (
	notEmpty = `{"notEmpty": {}}`
	title    = `"title"`
)

// TestRecognizeCheck covers every recognized form in pg-live's stored
// spelling (casts added, as pg_get_constraintdef prints, pretty or not) and in
// dpg's spelling (as written), and the near misses that stay opaque.
func TestRecognizeCheck(t *testing.T) {
	cases := []struct{ text, want string }{
		// §10.4: posts_title_check.
		{"CHECK (char_length(title) >= 1 AND char_length(title) <= 200)", portable(title, length(`"min": 1, "max": 200, `))},
		{"CHECK (((char_length(title) >= 1) AND (char_length(title) <= 200)))", portable(title, length(`"min": 1, "max": 200, `))},
		{"CHECK (char_length(title) BETWEEN 1 AND 200)", portable(title, length(`"min": 1, "max": 200, `))},
		{"char_length(title) BETWEEN 1 AND 200", portable(title, length(`"min": 1, "max": 200, `))},
		{"CHECK (char_length(title) >= 1) NOT VALID", portable(title, length(`"min": 1, `))},

		// Length.
		{"length(title) < 10", portable(title, length(`"max": 9, `))},
		{"character_length(title) > 0", portable(title, length(`"min": 1, `))},
		{"char_length(title) >= 0", portable(title, length(`"min": 0, `))},
		{"pg_catalog.char_length(title) = 3", portable(title, length(`"min": 3, "max": 3, `))},
		{"200 >= char_length(title)", portable(title, length(`"max": 200, `))},
		{"char_length(posts.title) <= 5", portable(title, length(`"max": 5, `))},
		{"CHECK ((char_length((display_name)::text) <= 80))", portable(`"display_name"`, length(`"max": 80, `))},
		{"char_length(display_name) <= 80", portable(`"display_name"`, length(`"max": 80, `))},
		{"char_length(title) <= '-5'::integer", opaqueCheck(title, "char_length(title) <= '-5'::integer")},
		{"char_length(title) < 0", opaqueCheck(title, "char_length(title) < 0")},
		{"char_length(title) >= 4294967296", opaqueCheck(title, "char_length(title) >= 4294967296")},
		{"char_length(title) <= 1.5", opaqueCheck(title, "char_length(title) <= 1.5")},
		{"char_length(title) <> 3", opaqueCheck(title, "char_length(title) <> 3")},
		{"char_length(code) <= 3", opaqueCheck(`"code"`, "char_length(code) <= 3")},
		{"char_length(qty) <= 3", opaqueCheck(`"qty"`, "char_length(qty) <= 3")},
		{"char_length(email) <= 3", opaqueCheck(`"email"`, "char_length(email) <= 3")},
		{"char_length((status)::text) < 3", opaqueCheck(`"status"`, "char_length((status)::text) < 3")},
		{"char_length((title)::varchar(3)) < 3", opaqueCheck(title, "char_length((title)::varchar(3)) < 3")},
		{"char_length(nosuch) < 3", opaqueCheck(`"nosuch"`, "char_length(nosuch) < 3")},
		{"upper(title) < 'x'", opaqueCheck(title, "upper(title) < 'x'")},
		{"app.char_length(title) < 3", opaqueCheck(title, "app.char_length(title) < 3")},
		{"char_length(DISTINCT title) < 3", opaqueCheck(title, "char_length(DISTINCT title) < 3")},
		{"char_length(title) >= 1 AND char_length(title) >= 2", opaqueCheck(title, "char_length(title) >= 1 AND char_length(title) >= 2")},
		{"char_length(title) = 1 AND char_length(title) <= 3", opaqueCheck(title, "char_length(title) = 1 AND char_length(title) <= 3")},
		{"char_length(title) >= 1 AND char_length(display_name) <= 3", opaqueCheck(`"title", "display_name"`, "char_length(title) >= 1 AND char_length(display_name) <= 3")},
		{"char_length(title) >= 1 AND title <> ''", opaqueCheck(title, "char_length(title) >= 1 AND title <> ''")},
		{"char_length(title) >= 1 AND char_length(title) <> 5", opaqueCheck(title, "char_length(title) >= 1 AND char_length(title) <> 5")},
		{"ROW(posts.*) IS NOT NULL", opaqueCheck("", "ROW(posts.*) IS NOT NULL")},
		{"char_length(title) >= 1 AND true", opaqueCheck(title, "char_length(title) >= 1 AND true")},

		// not_empty (§10.2): only the comparison with '' is not_empty.
		{"CHECK ((title <> ''::text))", portable(title, notEmpty)},
		{"title <> ''", portable(title, notEmpty)},
		{"title != ''", portable(title, notEmpty)},
		{"'' <> title", portable(title, notEmpty)},
		{"CHECK (((display_name)::text <> ''::text))", portable(`"display_name"`, notEmpty)},
		{"title <> ' '::text", opaqueCheck(title, "title <> ' '::text")},
		{"title <> 5", opaqueCheck(title, "title <> 5")},
		{"qty <> 0", opaqueCheck(`"qty"`, "qty <> 0")},
		{"code <> ''", opaqueCheck(`"code"`, "code <> ''")},

		// pattern (§10.2), only inside the portable subset.
		{"title ~ '^a'", portable(title, `{"pattern": {"regex": "^a"}}`)},
		{"CHECK ((title ~ '^a'::text))", portable(title, `{"pattern": {"regex": "^a"}}`)},
		{`title ~ '\d+'`, opaqueCheck(title, `title ~ '\d+'`)},
		{"title ~* 'a'", opaqueCheck(title, "title ~* 'a'")},
		{"title !~ 'a'", opaqueCheck(title, "title !~ 'a'")},
		{"'a' ~ title", opaqueCheck(title, "'a' ~ title")},
		{"qty ~ 'a'", opaqueCheck(`"qty"`, "qty ~ 'a'")},
		{"char_length(title) ~ 'a'", opaqueCheck(title, "char_length(title) ~ 'a'")},
		{"title ~ ('a' || 'b')", opaqueCheck(title, "title ~ ('a' || 'b')")},

		// range (§10.2): numbers, dates and times.
		{"qty >= 0", portable(`"qty"`, `{"range": {"min": {"int": "0"}, "minInclusive": true}}`)},
		{"0 < qty", portable(`"qty"`, `{"range": {"min": {"int": "0"}}}`)},
		{"qty > 0 AND qty < 100", portable(`"qty"`, `{"range": {"min": {"int": "0"}, "max": {"int": "100"}}}`)},
		{"CHECK (((qty >= 0) AND (qty <= 100)))", portable(`"qty"`, `{"range": {"min": {"int": "0"}, "max": {"int": "100"}, "minInclusive": true, "maxInclusive": true}}`)},
		{"qty BETWEEN -1 AND 10", portable(`"qty"`, `{"range": {"min": {"int": "-1"}, "max": {"int": "10"}, "minInclusive": true, "maxInclusive": true}}`)},
		{"CHECK ((big >= '-5'::bigint))", portable(`"big"`, `{"range": {"min": {"int": "-5"}, "minInclusive": true}}`)},
		{"big <= 5000000000", portable(`"big"`, `{"range": {"max": {"int": "5000000000"}, "maxInclusive": true}}`)},
		{"small <= (5)::smallint", portable(`"small"`, `{"range": {"max": {"int": "5"}, "maxInclusive": true}}`)},
		{"CHECK ((price >= (0)::numeric))", portable(`"price"`, `{"range": {"min": {"decimal": "0"}, "minInclusive": true}}`)},
		{"price > 0.5", portable(`"price"`, `{"range": {"min": {"decimal": "0.5"}}}`)},
		{"price <= '99.99'::numeric", portable(`"price"`, `{"range": {"max": {"decimal": "99.99"}, "maxInclusive": true}}`)},
		{"ratio > 0.5", portable(`"ratio"`, `{"range": {"min": {"float": 0.5}}}`)},
		{"ratio < (1)::double precision", portable(`"ratio"`, `{"range": {"max": {"float": 1}}}`)},
		{"CHECK ((born >= '2000-01-01'::date))", portable(`"born"`, `{"range": {"min": {"string": "2000-01-01"}, "minInclusive": true}}`)},
		{"born >= '2000-01-01'", portable(`"born"`, `{"range": {"min": {"string": "2000-01-01"}, "minInclusive": true}}`)},
		{"at < '2030-01-01 00:00:00'::timestamp without time zone", portable(`"at"`, `{"range": {"max": {"string": "2030-01-01 00:00:00"}}}`)},
		{"t >= '08:00'", portable(`"t"`, `{"range": {"min": {"string": "08:00"}, "minInclusive": true}}`)},
		{"qty >= 1 AND qty >= 2", opaqueCheck(`"qty"`, "qty >= 1 AND qty >= 2")},
		{"qty > 1 AND qty = 5", opaqueCheck(`"qty"`, "qty > 1 AND qty = 5")},
		{"qty > 0 AND qty < 5 AND qty <> 3", opaqueCheck(`"qty"`, "qty > 0 AND qty < 5 AND qty <> 3")},
		{"qty > 0 OR qty < -5", opaqueCheck(`"qty"`, "qty > 0 OR qty < -5")},
		{"NOT (qty > 0)", opaqueCheck(`"qty"`, "NOT (qty > 0)")},
		{"qty > 0 AND big > 0", opaqueCheck(`"qty", "big"`, "qty > 0 AND big > 0")},
		{"qty NOT BETWEEN 1 AND 2", opaqueCheck(`"qty"`, "qty NOT BETWEEN 1 AND 2")},
		{"qty BETWEEN SYMMETRIC 1 AND 2", opaqueCheck(`"qty"`, "qty BETWEEN SYMMETRIC 1 AND 2")},
		{"nosuch BETWEEN 1 AND 2", opaqueCheck(`"nosuch"`, "nosuch BETWEEN 1 AND 2")},
		{"qty OPERATOR(pg_catalog.>) 0", opaqueCheck(`"qty"`, "qty OPERATOR(pg_catalog.>) 0")},
		{"qty >= a", opaqueCheck(`"qty", "a"`, "qty >= a")},
		{"qty >= 1.5", opaqueCheck(`"qty"`, "qty >= 1.5")},
		{"qty > NULL", opaqueCheck(`"qty"`, "qty > NULL")},
		{"qty > true", opaqueCheck(`"qty"`, "qty > true")},
		{"qty >= (1)::text", opaqueCheck(`"qty"`, "qty >= (1)::text")},
		{"price >= 1e5", opaqueCheck(`"price"`, "price >= 1e5")},
		{"price >= (0)::numeric(3,1)", opaqueCheck(`"price"`, "price >= (0)::numeric(3,1)")},
		{"ratio > 'NaN'::double precision", opaqueCheck(`"ratio"`, "ratio > 'NaN'::double precision")},
		{"title >= 'a'", opaqueCheck(title, "title >= 'a'")},
		{"published_at > '2020-01-01'", opaqueCheck(`"published_at"`, "published_at > '2020-01-01'")},
		{"born >= 'infinity'::date", opaqueCheck(`"born"`, "born >= 'infinity'::date")},
		{"born >= 5", opaqueCheck(`"born"`, "born >= 5")},
		{"at > 'x'", opaqueCheck(`"at"`, "at > 'x'")},
		{"t > 'x'", opaqueCheck(`"t"`, "t > 'x'")},

		// membership (§10.2, §8.4).
		{"status IN ('draft', 'published')", portable(`"status"`, `{"membership": {"values": [{"string": "draft"}, {"string": "published"}]}}`)},
		{"CHECK ((status = ANY (ARRAY['draft'::app.post_status, 'published'::app.post_status])))", portable(`"status"`, `{"membership": {"values": [{"string": "draft"}, {"string": "published"}]}}`)},
		{"status = ANY (ARRAY['draft'::post_status]::app.post_status[])", portable(`"status"`, `{"membership": {"values": [{"string": "draft"}]}}`)},
		{"CHECK (((display_name)::text = ANY ((ARRAY['a'::character varying, 'b'::character varying])::text[])))", portable(`"display_name"`, `{"membership": {"values": [{"string": "a"}, {"string": "b"}]}}`)},
		{"qty IN (1, 2, 3)", portable(`"qty"`, `{"membership": {"values": [{"int": "1"}, {"int": "2"}, {"int": "3"}]}}`)},
		{"price IN (1.5, 2)", portable(`"price"`, `{"membership": {"values": [{"decimal": "1.5"}, {"decimal": "2"}]}}`)},
		{"CHECK ((status = 'draft'::app.post_status))", portable(`"status"`, `{"membership": {"values": [{"string": "draft"}]}}`)},
		{"'draft' = status", portable(`"status"`, `{"membership": {"values": [{"string": "draft"}]}}`)},
		{"status NOT IN ('a')", opaqueCheck(`"status"`, "status NOT IN ('a')")},
		{"status <> ALL (ARRAY['a'])", opaqueCheck(`"status"`, "status <> ALL (ARRAY['a'])")},
		{"status <> ANY (ARRAY['a'])", opaqueCheck(`"status"`, "status <> ANY (ARRAY['a'])")},
		{"status = ANY ('{a,b}'::text[])", opaqueCheck(`"status"`, "status = ANY ('{a,b}'::text[])")},
		{"title = ANY ('{a,b}'::text[])", opaqueCheck(title, "title = ANY ('{a,b}'::text[])")},
		{"nosuch = ANY (ARRAY[1])", opaqueCheck(`"nosuch"`, "nosuch = ANY (ARRAY[1])")},
		{"char_length(title) = ANY (ARRAY[1])", opaqueCheck(title, "char_length(title) = ANY (ARRAY[1])")},
		{"ratio IN (1.5)", opaqueCheck(`"ratio"`, "ratio IN (1.5)")},
		{"born = '2020-01-01'", opaqueCheck(`"born"`, "born = '2020-01-01'")},
		{"status IN ('a', 1)", opaqueCheck(`"status"`, "status IN ('a', 1)")},
		{"status = 'x'::app.other_enum", opaqueCheck(`"status"`, "status = 'x'::app.other_enum")},
		{"status = 'x'::other.post_status", opaqueCheck(`"status"`, "status = 'x'::other.post_status")},
		{"title = 'x'::public.citext", opaqueCheck(title, "title = 'x'::public.citext")},

		// §10.4: posts_published_check, both spellings.
		{"CHECK (published_at IS NULL OR status <> 'draft'::app.post_status)",
			opaqueCheck(`"published_at", "status"`, "CHECK (published_at IS NULL OR status <> 'draft'::app.post_status)")},
		{"published_at IS NULL OR status <> 'draft'",
			opaqueCheck(`"published_at", "status"`, "published_at IS NULL OR status <> 'draft'")},
		// Columns come in order of appearance, each once.
		{"b > a OR a > c", opaqueCheck(`"b", "a", "c"`, "b > a OR a > c")},
	}
	for _, c := range cases {
		got, err := RecognizeCheck(c.text, postColumns)
		if err != nil {
			t.Errorf("%s: %v", c.text, err)
			continue
		}
		assertJSON(t, c.text, got, c.want)
	}
}

func opaqueCheck(columns, native string) string {
	js := `{"kind": "CONSTRAINT_KIND_CHECK", `
	if columns != "" {
		js += `"columns": [` + columns + `], `
	}
	return js + `"opaque": {"native": ` + quote(native) + `}}`
}

// TestRecognizeDomainCheck covers §8.5 and §10.4's domain constraints in both
// spellings.
func TestRecognizeDomainCheck(t *testing.T) {
	text := typ(`{"text": {}}`)
	cases := []struct {
		text string
		base *irv1.Type
		want string
	}{
		// email_address_check.
		{"CHECK ((char_length(VALUE) <= 320))", text, portable("", length(`"max": 320, `))},
		{"char_length(VALUE) <= 320", text, portable("", length(`"max": 320, `))},
		// slug_text_check.
		{"CHECK ((VALUE ~ '^[a-z0-9]+(-[a-z0-9]+)*$'::text))", text, portable("", `{"pattern": {"regex": "^[a-z0-9]+(-[a-z0-9]+)*$"}}`)},
		{"VALUE ~ '^[a-z0-9]+(-[a-z0-9]+)*$'", text, portable("", `{"pattern": {"regex": "^[a-z0-9]+(-[a-z0-9]+)*$"}}`)},
		{"VALUE IN ('a', 'b')", text, portable("", `{"membership": {"values": [{"string": "a"}, {"string": "b"}]}}`)},
		{"VALUE > 0", typ(`{"int64": {}}`), portable("", `{"range": {"min": {"int": "0"}}}`)},
		{"char_length(other) <= 3", text, opaqueCheck("", "char_length(other) <= 3")},
		{"char_length(t.value) <= 3", text, opaqueCheck("", "char_length(t.value) <= 3")},
		{"VALUE ~ 'a' OR VALUE IS NULL", text, opaqueCheck("", "VALUE ~ 'a' OR VALUE IS NULL")},
	}
	for _, c := range cases {
		got, err := RecognizeDomainCheck(c.text, c.base)
		if err != nil {
			t.Errorf("%s: %v", c.text, err)
			continue
		}
		assertJSON(t, c.text, got, c.want)
	}
}

func TestRecognizeCheckErrors(t *testing.T) {
	for _, bad := range []string{
		"CHECK (",
		"CHECK (true); DROP TABLE x",
		"UNIQUE (a)",
		"true) NOT VALID, ADD CHECK (true",
	} {
		if _, err := RecognizeCheck(bad, postColumns); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// TestSingleColumnMembership: an enum annotation accepts only a single-column
// membership test (§8.4).
func TestSingleColumnMembership(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"status IN ('new', 'paid', 'shipped')", true},
		{"CHECK (((display_name)::text = ANY ((ARRAY['new'::character varying, 'paid'::character varying])::text[])))", true},
		{"char_length(title) <= 3", false},
		{"status IN ('a') OR qty > 0", false},
	}
	for _, c := range cases {
		got, err := RecognizeCheck(c.text, postColumns)
		if err != nil {
			t.Fatal(err)
		}
		if SingleColumnMembership(got) != c.want {
			t.Errorf("%s: got %v", c.text, !c.want)
		}
	}
	dom, err := RecognizeDomainCheck("VALUE IN ('a')", typ(`{"text": {}}`))
	if err != nil {
		t.Fatal(err)
	}
	if SingleColumnMembership(dom) {
		t.Error("a domain's membership names no column")
	}
}
