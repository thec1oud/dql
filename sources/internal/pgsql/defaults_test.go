package pgsql

import "testing"

func constant(native, value string) string {
	js := `{"class": "DEFAULT_CONSTANT", "native": ` + quote(native)
	if value != "" {
		js += `, "value": ` + value
	}
	return js + `}`
}

func expression(native string) string {
	return `{"class": "DEFAULT_EXPRESSION", "native": ` + quote(native) + `}`
}

func serial(native, seq string) string {
	return `{"class": "DEFAULT_IDENTITY_BY_DEFAULT", "native": ` + quote(native) + `, "sequence": ` + seq + `}`
}

// TestClassifyDefault covers §11.3's classes in pg-live's stored spelling
// (pg_get_expr, with casts) and dpg's (as written).
func TestClassifyDefault(t *testing.T) {
	cases := []struct{ text, want string }{
		// §11.3: posts.status and users.created_at.
		{"'draft'::app.post_status", `{"class": "DEFAULT_CONSTANT", "native": "'draft'::app.post_status", "value": {"string": "draft"}}`},
		{"'draft'", constant("'draft'", `{"string": "draft"}`)},
		{"now()", `{"class": "DEFAULT_EXPRESSION", "native": "now()"}`},
		{"gen_random_uuid()", expression("gen_random_uuid()")},
		{"CURRENT_TIMESTAMP", expression("CURRENT_TIMESTAMP")},
		{"lower('X')", expression("lower('X')")},
		{"'a'::text || 'b'::text", expression("'a'::text || 'b'::text")},
		{"- 1::integer", expression("- 1::integer")},

		{"1", constant("1", `{"int": "1"}`)},
		{"-5", constant("-5", `{"int": "-5"}`)},
		{"5000000000", constant("5000000000", `{"int": "5000000000"}`)},
		{"'-1'::integer", constant("'-1'::integer", `{"int": "-1"}`)},
		{"(7)::bigint", constant("(7)::bigint", `{"int": "7"}`)},
		{"'abc'::integer", constant("'abc'::integer", "")},
		{"1.5", constant("1.5", `{"decimal": "1.5"}`)},
		{"-1.5", constant("-1.5", `{"decimal": "-1.5"}`)},
		{"1e5", constant("1e5", "")},
		{"(0)::numeric", constant("(0)::numeric", `{"decimal": "0"}`)},
		{"'1.50'::numeric(10,2)", constant("'1.50'::numeric(10,2)", `{"decimal": "1.50"}`)},
		{"1.5::double precision", constant("1.5::double precision", `{"decimal": "1.5"}`)},
		{"'NaN'::double precision", constant("'NaN'::double precision", "")},
		{"true", constant("true", `{"bool": true}`)},
		{"false", constant("false", `{"bool": false}`)},
		{"true::boolean", constant("true::boolean", `{"bool": true}`)},
		{"'t'::boolean", constant("'t'::boolean", `{"bool": true}`)},
		{"' Off '::boolean", constant("' Off '::boolean", `{"bool": false}`)},
		{"'maybe'::boolean", constant("'maybe'::boolean", "")},
		{"true::text", constant("true::text", `{"string": "true"}`)},
		{"1::text", constant("1::text", `{"string": "1"}`)},
		{"'x'::text", constant("'x'::text", `{"string": "x"}`)},
		{"' '::text", constant("' '::text", `{"string": " "}`)},
		{"''::character varying", constant("''::character varying", `{"string": ""}`)},
		{"('x'::character varying)::text", constant("('x'::character varying)::text", `{"string": "x"}`)},
		{"'{}'::text[]", constant("'{}'::text[]", `{"string": "{}"}`)},
		{"'2020-01-01'::date", constant("'2020-01-01'::date", `{"string": "2020-01-01"}`)},
		{"NULL", constant("NULL", "")},
		{"NULL::text", constant("NULL::text", "")},
		{"B'101'", constant("B'101'", "")},

		// Serial columns default to their sequence.
		{"nextval('app.users_id_seq'::regclass)", serial("nextval('app.users_id_seq'::regclass)", `{"namespace": "app", "object": "users_id_seq"}`)},
		{"nextval('users_id_seq')", serial("nextval('users_id_seq')", `{"object": "users_id_seq"}`)},
		{`pg_catalog.nextval('"App"."Seq""x"'::regclass)`, serial(`pg_catalog.nextval('"App"."Seq""x"'::regclass)`, `{"namespace": "App", "object": "Seq\"x"}`)},
		{"nextval('APP.Seq')", serial("nextval('APP.Seq')", `{"namespace": "app", "object": "seq"}`)},
		{"nextval('a.b.c')", expression("nextval('a.b.c')")},
		{"nextval('a..b')", expression("nextval('a..b')")},
		{"nextval(''::regclass)", expression("nextval(''::regclass)")},
		{`nextval('"unterminated')`, expression(`nextval('"unterminated')`)},
		{`nextval('a"b')`, expression(`nextval('a"b')`)},
		{"nextval(5)", expression("nextval(5)")},
		{"nextval('s'::text)", expression("nextval('s'::text)")},
		{"nextval('s', 1)", expression("nextval('s', 1)")},
		{"currval('s'::regclass)", expression("currval('s'::regclass)")},
		{"app.nextval('s')", expression("app.nextval('s')")},
	}
	for _, c := range cases {
		got, err := ClassifyDefault(c.text)
		if err != nil {
			t.Errorf("%s: %v", c.text, err)
			continue
		}
		assertJSON(t, c.text, got, c.want)
	}
}

func TestClassifyDefaultErrors(t *testing.T) {
	for _, bad := range []string{
		"now(",
		"1; DROP TABLE x",
		"1, ALTER COLUMN d DROP DEFAULT",
	} {
		if _, err := ClassifyDefault(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}
