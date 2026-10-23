package pgsql

import (
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// shopTypes resolves the named types of the running example (§1.3) and a few
// more for the cases the example lacks.
var shopTypes = Types{Catalog: "shop", Resolve: func(ns, name string) (Resolved, bool) {
	switch ns + "." + name {
	case "app.post_status":
		return Resolved{Kind: KindEnum}, true
	case "app.email_address", ".email_address":
		return Resolved{Namespace: "app", Kind: KindDomain}, true
	case "app.money_amount":
		return Resolved{Kind: KindComposite}, true
	case "public.citext", ".citext":
		return Resolved{Namespace: "public", Kind: KindOther}, true
	case "app.floatrange":
		return Resolved{Kind: KindRange, Subtype: &Native{Namespace: "pg_catalog", Name: "float8"}}, true
	case "app.floatmultirange":
		return Resolved{Kind: KindMultirange, Subtype: &Native{Namespace: "pg_catalog", Name: "float8"}}, true
	case "app.textrange":
		return Resolved{Kind: KindRange, Subtype: &Native{Namespace: "pg_catalog", Name: "varchar", Modifiers: intMod(3)}}, true
	case "app.citextrange":
		return Resolved{Kind: KindRange, Subtype: &Native{Namespace: "public", Name: "citext"}}, true
	case "app.norange":
		return Resolved{Kind: KindRange}, true
	case "app.badrange":
		return Resolved{Kind: KindRange, Subtype: &Native{Namespace: "pg_catalog", Name: "int4", Modifiers: intMod(1)}}, true
	}
	return Resolved{}, false
}}

func nat(ns, obj, raw string) string {
	return `"native": {"name": {"namespace": "` + ns + `", "object": "` + obj + `"}, "raw": "` + raw + `"}`
}

func natMods(ns, obj, mods, raw string) string {
	return `"native": {"name": {"namespace": "` + ns + `", "object": "` + obj + `"}, "modifiers": [` + mods + `], "raw": "` + raw + `"}`
}

// TestLogicalTypeFromText maps types spelled as in DDL (dpg's form, and
// format_type's) to logical types (§8.1, §8.2, §8.6, §8.8).
func TestLogicalTypeFromText(t *testing.T) {
	cases := []struct{ text, want string }{
		{"boolean", `{"bool": {}, ` + nat("pg_catalog", "bool", "boolean") + `}`},
		{"smallint", `{"int16": {}, ` + nat("pg_catalog", "int2", "smallint") + `}`},
		{"integer", `{"int32": {}, ` + nat("pg_catalog", "int4", "integer") + `}`},
		{"int4", `{"int32": {}, ` + nat("pg_catalog", "int4", "int4") + `}`},
		{"bigint", `{"int64": {}, ` + nat("pg_catalog", "int8", "bigint") + `}`},
		{"real", `{"float32": {}, ` + nat("pg_catalog", "float4", "real") + `}`},
		{"double precision", `{"float64": {}, ` + nat("pg_catalog", "float8", "double precision") + `}`},
		{"numeric", `{"decimal": {}, ` + nat("pg_catalog", "numeric", "numeric") + `}`},
		{"numeric(12,2)", `{"decimal": {"precision": 12, "scale": 2}, ` + natMods("pg_catalog", "numeric", `{"int": 12}, {"int": 2}`, "numeric(12,2)") + `}`},
		{"decimal(12)", `{"decimal": {"precision": 12, "scale": 0}, ` + natMods("pg_catalog", "numeric", `{"int": 12}, {"int": 0}`, "decimal(12)") + `}`},
		{"numeric(5,-2)", `{"opaque": {}, ` + natMods("pg_catalog", "numeric", `{"int": 5}, {"int": -2}`, "numeric(5,-2)") + `}`},
		{"money", `{"money": {}, ` + nat("pg_catalog", "money", "money") + `}`},
		{"text", `{"text": {}, ` + nat("pg_catalog", "text", "text") + `}`},
		// §8.2: users.display_name.
		{"character varying(80)", `{
			"varchar": {"length": 80, "unit": "LENGTH_UNIT_CHARACTERS"},
			"native": {"name": {"namespace": "pg_catalog", "object": "varchar"}, "modifiers": [{"int": 80}], "raw": "character varying(80)"}}`},
		{"character varying", `{"text": {}, ` + nat("pg_catalog", "varchar", "character varying") + `}`},
		{"char(3)", `{"char": {"length": 3, "unit": "LENGTH_UNIT_CHARACTERS"}, ` + natMods("pg_catalog", "bpchar", `{"int": 3}`, "char(3)") + `}`},
		{"character", `{"char": {"length": 1, "unit": "LENGTH_UNIT_CHARACTERS"}, ` + natMods("pg_catalog", "bpchar", `{"int": 1}`, "character") + `}`},
		{"bpchar", `{"opaque": {}, ` + nat("pg_catalog", "bpchar", "bpchar") + `}`},
		{`"char"`, `{"opaque": {}, ` + nat("pg_catalog", "char", `\"char\"`) + `}`},
		{"bytea", `{"bytes": {}, ` + nat("pg_catalog", "bytea", "bytea") + `}`},
		// §8.8: users.created_at.
		{"timestamp with time zone", `{
			"instant": {"precision": 6},
			"native": {"name": {"namespace": "pg_catalog", "object": "timestamptz"}, "raw": "timestamp with time zone"}}`},
		{"timestamptz(3)", `{"instant": {"precision": 3}, ` + natMods("pg_catalog", "timestamptz", `{"int": 3}`, "timestamptz(3)") + `}`},
		{"timestamp", `{"localDatetime": {"precision": 6}, ` + nat("pg_catalog", "timestamp", "timestamp") + `}`},
		{"timestamp(0) without time zone", `{"localDatetime": {"precision": 0}, ` + natMods("pg_catalog", "timestamp", `{"int": 0}`, "timestamp(0) without time zone") + `}`},
		{"date", `{"date": {}, ` + nat("pg_catalog", "date", "date") + `}`},
		{"time", `{"localTime": {"precision": 6}, ` + nat("pg_catalog", "time", "time") + `}`},
		{"time(3) with time zone", `{"timeWithOffset": {"precision": 3}, ` + natMods("pg_catalog", "timetz", `{"int": 3}`, "time(3) with time zone") + `}`},
		{"interval", `{"interval": {"precision": 6}, ` + nat("pg_catalog", "interval", "interval") + `}`},
		{"interval(2)", `{"interval": {"precision": 2}, ` + natMods("pg_catalog", "interval", `{"int": 32767}, {"int": 2}`, "interval(2)") + `}`},
		{"interval day to second(3)", `{"interval": {"precision": 3}, ` + natMods("pg_catalog", "interval", `{"int": 7176}, {"int": 3}`, "interval day to second(3)") + `}`},
		{"interval year", `{"interval": {"precision": 6}, ` + natMods("pg_catalog", "interval", `{"int": 4}`, "interval year") + `}`},
		{"uuid", `{"uuid": {}, ` + nat("pg_catalog", "uuid", "uuid") + `}`},
		{"json", `{"json": {}, ` + nat("pg_catalog", "json", "json") + `}`},
		{"jsonb", `{"jsonb": {}, ` + nat("pg_catalog", "jsonb", "jsonb") + `}`},
		{"xml", `{"xml": {}, ` + nat("pg_catalog", "xml", "xml") + `}`},
		{"inet", `{"inet": {}, ` + nat("pg_catalog", "inet", "inet") + `}`},
		{"cidr", `{"cidr": {}, ` + nat("pg_catalog", "cidr", "cidr") + `}`},
		{"macaddr", `{"macaddr": {}, ` + nat("pg_catalog", "macaddr", "macaddr") + `}`},
		{"macaddr8", `{"opaque": {}, ` + nat("pg_catalog", "macaddr8", "macaddr8") + `}`},
		{"bit(8)", `{"bit": {"length": 8}, ` + natMods("pg_catalog", "bit", `{"int": 8}`, "bit(8)") + `}`},
		{"bit", `{"bit": {"length": 1}, ` + natMods("pg_catalog", "bit", `{"int": 1}`, "bit") + `}`},
		{"bit varying(5)", `{"bit": {"length": 5, "varying": true}, ` + natMods("pg_catalog", "varbit", `{"int": 5}`, "bit varying(5)") + `}`},
		{"varbit", `{"bit": {"varying": true}, ` + nat("pg_catalog", "varbit", "varbit") + `}`},
		{"int4range", `{"range": {"element": {"int32": {}}}, ` + nat("pg_catalog", "int4range", "int4range") + `}`},
		{"int8range", `{"range": {"element": {"int64": {}}}, ` + nat("pg_catalog", "int8range", "int8range") + `}`},
		{"numrange", `{"range": {"element": {"decimal": {}}}, ` + nat("pg_catalog", "numrange", "numrange") + `}`},
		{"tsrange", `{"range": {"element": {"localDatetime": {"precision": 6}}}, ` + nat("pg_catalog", "tsrange", "tsrange") + `}`},
		{"tstzrange", `{"range": {"element": {"instant": {"precision": 6}}}, ` + nat("pg_catalog", "tstzrange", "tstzrange") + `}`},
		{"daterange", `{"range": {"element": {"date": {}}}, ` + nat("pg_catalog", "daterange", "daterange") + `}`},
		{"int4multirange", `{"multirange": {"element": {"int32": {}}}, ` + nat("pg_catalog", "int4multirange", "int4multirange") + `}`},
		{"int8multirange", `{"multirange": {"element": {"int64": {}}}, ` + nat("pg_catalog", "int8multirange", "int8multirange") + `}`},
		{"nummultirange", `{"multirange": {"element": {"decimal": {}}}, ` + nat("pg_catalog", "nummultirange", "nummultirange") + `}`},
		{"tsmultirange", `{"multirange": {"element": {"localDatetime": {"precision": 6}}}, ` + nat("pg_catalog", "tsmultirange", "tsmultirange") + `}`},
		{"tstzmultirange", `{"multirange": {"element": {"instant": {"precision": 6}}}, ` + nat("pg_catalog", "tstzmultirange", "tstzmultirange") + `}`},
		{"datemultirange", `{"multirange": {"element": {"date": {}}}, ` + nat("pg_catalog", "datemultirange", "datemultirange") + `}`},
		// §8.6: posts.labels.
		{"text[]", `{
			"array": {"element": {"text": {}}, "elementNullable": true, "dimensions": 1},
			"native": {"name": {"namespace": "pg_catalog", "object": "_text"}, "raw": "text[]"}}`},
		{"integer[][]", `{"array": {"element": {"int32": {}}, "elementNullable": true, "dimensions": 2}, ` + nat("pg_catalog", "_int4", "integer[][]") + `}`},
		{"integer ARRAY", `{"array": {"element": {"int32": {}}, "elementNullable": true, "dimensions": 1}, ` + nat("pg_catalog", "_int4", "integer ARRAY") + `}`},
		{"character varying(80)[]", `{"array": {"element": {"varchar": {"length": 80, "unit": "LENGTH_UNIT_CHARACTERS"}}, "elementNullable": true, "dimensions": 1}, ` +
			natMods("pg_catalog", "_varchar", `{"int": 80}`, "character varying(80)[]") + `}`},
		{"app.post_status[]", `{"array": {"element": {"named": {"enum": {"catalog": "shop", "namespace": "app", "object": "post_status"}}}, "elementNullable": true, "dimensions": 1}, ` +
			nat("app", "_post_status", "app.post_status[]") + `}`},
		// An opaque element keeps its native type (§8.3).
		{"citext[]", `{"array": {"element": {"opaque": {}, "native": {"name": {"namespace": "public", "object": "citext"}}}, "elementNullable": true, "dimensions": 1}, ` +
			nat("public", "_citext", "citext[]") + `}`},
		{"bpchar[]", `{"array": {"element": {"opaque": {}, "native": {"name": {"namespace": "pg_catalog", "object": "bpchar"}}}, "elementNullable": true, "dimensions": 1}, ` +
			nat("pg_catalog", "_bpchar", "bpchar[]") + `}`},
		{"app.post_status", `{"named": {"enum": {"catalog": "shop", "namespace": "app", "object": "post_status"}}, ` + nat("app", "post_status", "app.post_status") + `}`},
		{"app.email_address", `{"named": {"domain": {"catalog": "shop", "namespace": "app", "object": "email_address"}}, ` + nat("app", "email_address", "app.email_address") + `}`},
		{"email_address", `{"named": {"domain": {"catalog": "shop", "namespace": "app", "object": "email_address"}}, ` + nat("app", "email_address", "email_address") + `}`},
		{"app.money_amount", `{"named": {"composite": {"catalog": "shop", "namespace": "app", "object": "money_amount"}}, ` + nat("app", "money_amount", "app.money_amount") + `}`},
		// §8.3: users.nickname.
		{"citext", `{"opaque": {}, "native": {"name": {"namespace": "public", "object": "citext"}, "raw": "citext"}}`},
		{"geometry(Point, 4326)", `{"opaque": {}, ` + natMods("", "geometry", `{"string": "point"}, {"int": 4326}`, "geometry(Point, 4326)") + `}`},
		{"tsvector", `{"opaque": {}, ` + nat("pg_catalog", "tsvector", "tsvector") + `}`},
		{"app.floatrange", `{"range": {"element": {"float64": {}}}, ` + nat("app", "floatrange", "app.floatrange") + `}`},
		{"app.floatmultirange", `{"multirange": {"element": {"float64": {}}}, ` + nat("app", "floatmultirange", "app.floatmultirange") + `}`},
		{"app.textrange", `{"range": {"element": {"varchar": {"length": 3, "unit": "LENGTH_UNIT_CHARACTERS"}}}, ` + nat("app", "textrange", "app.textrange") + `}`},
		{"app.citextrange", `{"range": {"element": {"opaque": {}, "native": {"name": {"namespace": "public", "object": "citext"}}}}, ` + nat("app", "citextrange", "app.citextrange") + `}`},
		{"app.norange", `{"opaque": {}, ` + nat("app", "norange", "app.norange") + `}`},
	}
	for _, c := range cases {
		n, err := ParseTypeText(c.text)
		if err != nil {
			t.Errorf("%s: %v", c.text, err)
			continue
		}
		got, err := shopTypes.LogicalType(n)
		if err != nil {
			t.Errorf("%s: %v", c.text, err)
			continue
		}
		assertJSON(t, c.text, got, c.want)
	}
}

// TestLogicalTypeFromCatalog maps types as pg-live reads them from pg_type and
// pg_attribute, which must equal dpg's text form (§8.2).
func TestLogicalTypeFromCatalog(t *testing.T) {
	cases := []struct {
		native Native
		text   string
	}{
		{Native{Namespace: "pg_catalog", Name: "varchar", Modifiers: decode(t, "varchar", 84), Raw: "character varying(80)"}, "character varying(80)"},
		{Native{Namespace: "pg_catalog", Name: "numeric", Modifiers: decode(t, "numeric", 12<<16|2+4), Raw: "numeric(12,2)"}, "numeric(12,2)"},
		{Native{Namespace: "pg_catalog", Name: "numeric", Modifiers: decode(t, "numeric", 12<<16+4), Raw: "numeric(12)"}, "numeric(12)"},
		{Native{Namespace: "pg_catalog", Name: "timestamptz", Modifiers: decode(t, "timestamptz", -1), Raw: "timestamp with time zone"}, "timestamp with time zone"},
		{Native{Namespace: "pg_catalog", Name: "interval", Modifiers: decode(t, "interval", 7176<<16|3), Raw: "interval day to second(3)"}, "interval day to second(3)"},
		{Native{Namespace: "pg_catalog", Name: "interval", Modifiers: decode(t, "interval", 4<<16|0xFFFF), Raw: "interval year"}, "interval year"},
		{Native{Namespace: "pg_catalog", Name: "bpchar", Modifiers: decode(t, "bpchar", 5), Raw: "character"}, "character"},
		{Native{Namespace: "pg_catalog", Name: "bit", Modifiers: decode(t, "bit", 1), Raw: "bit"}, "bit"},
		{Native{Namespace: "pg_catalog", Name: "text", Array: true, Dimensions: 1, ArrayName: "_text", Raw: "text[]"}, "text[]"},
		{Native{Namespace: "pg_catalog", Name: "int4", Array: true, Raw: "integer[]"}, "integer[]"},
		{Native{Namespace: "public", Name: "citext", Raw: "citext"}, "citext"},
		{Native{Namespace: "app", Name: "post_status", Raw: "app.post_status"}, "app.post_status"},
	}
	for _, c := range cases {
		got, err := shopTypes.LogicalType(c.native)
		if err != nil {
			t.Fatalf("%s: %v", c.text, err)
		}
		n, err := ParseTypeText(c.text)
		if err != nil {
			t.Fatalf("%s: %v", c.text, err)
		}
		want, _ := shopTypes.LogicalType(n)
		if !proto.Equal(got, want) {
			t.Errorf("%s: catalog form %v, text form %v", c.text, got, want)
		}
	}
}

func decode(t *testing.T, name string, typmod int32) []*irv1.TypeModifier {
	t.Helper()
	m, ok := DecodeTypmod(name, typmod)
	if !ok {
		t.Fatalf("DecodeTypmod(%s, %d) not decodable", name, typmod)
	}
	return m
}

func TestLogicalTypeUnresolved(t *testing.T) {
	got, err := Types{}.LogicalType(Native{Name: "mystery", Raw: "mystery"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, "mystery", got, `{"opaque": {}, `+nat("", "mystery", "mystery")+`}`)
	got, err = Types{}.LogicalType(Native{Name: "int4", Raw: "int4"})
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, "int4", got, `{"int32": {}, `+nat("pg_catalog", "int4", "int4")+`}`)
}

func TestLogicalTypeErrors(t *testing.T) {
	cases := []struct {
		name   string
		native Native
	}{
		{"string modifier", Native{Namespace: "pg_catalog", Name: "varchar", Modifiers: []*irv1.TypeModifier{{Kind: &irv1.TypeModifier_String_{String_: "x"}}}}},
		{"too many modifiers", Native{Namespace: "pg_catalog", Name: "int4", Modifiers: intMod(1)}},
		{"negative precision", Native{Namespace: "pg_catalog", Name: "numeric", Modifiers: append(intMod(-1), intMod(2)...)}},
		{"negative length", Native{Namespace: "pg_catalog", Name: "varchar", Modifiers: intMod(-1)}},
		{"bad range subtype", Native{Namespace: "app", Name: "badrange"}},
	}
	for _, c := range cases {
		if _, err := shopTypes.LogicalType(c.native); err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
}

func TestParseTypeText(t *testing.T) {
	n, err := ParseTypeText("serial")
	if err != nil || n.Namespace != "pg_catalog" || n.Name != "int4" || !n.Serial {
		t.Errorf("serial: %+v %v", n, err)
	}
	n, err = ParseTypeText("bigserial")
	if err != nil || n.Name != "int8" || !n.Serial {
		t.Errorf("bigserial: %+v %v", n, err)
	}
	n, err = ParseTypeText("foo('x')")
	if err != nil || n.Namespace != "" || n.Modifiers[0].GetString_() != "x" {
		t.Errorf("foo('x'): %+v %v", n, err)
	}
	for _, bad := range []string{
		"setof integer",
		"a.b.c",
		"integer; SELECT 1",
		"integer, ADD COLUMN d integer",
		"integer USING 1",
		`text COLLATE "C"`,
		"numeric(1+1)",
		"foo(t.*)",
		"numeric(",
	} {
		if _, err := ParseTypeText(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestDecodeTypmod(t *testing.T) {
	cases := []struct {
		name   string
		typmod int32
		want   []int32
		ok     bool
	}{
		{"varchar", 84, []int32{80}, true},
		{"bpchar", 7, []int32{3}, true},
		{"numeric", 12<<16 | 2 + 4, []int32{12, 2}, true},
		{"numeric", (5<<16 | (-2 & 0x7FF)) + 4, []int32{5, -2}, true},
		{"timestamptz", 3, []int32{3}, true},
		{"timestamp", 0, []int32{0}, true},
		{"time", 2, []int32{2}, true},
		{"timetz", 2, []int32{2}, true},
		{"bit", 8, []int32{8}, true},
		{"varbit", 5, []int32{5}, true},
		{"interval", 0x7FFF<<16 | 3, []int32{0x7FFF, 3}, true},
		{"interval", 7176<<16 | 0xFFFF, []int32{7176}, true},
		{"varchar", -1, nil, true},
		{"geometry", 1234, nil, false},
	}
	for _, c := range cases {
		got, ok := DecodeTypmod(c.name, c.typmod)
		var ints []int32
		for _, m := range got {
			ints = append(ints, m.GetInt())
		}
		if ok != c.ok || len(ints) != len(c.want) {
			t.Errorf("%s %d: got %v %v, want %v %v", c.name, c.typmod, ints, ok, c.want, c.ok)
			continue
		}
		for i := range ints {
			if ints[i] != c.want[i] {
				t.Errorf("%s %d: got %v, want %v", c.name, c.typmod, ints, c.want)
			}
		}
	}
}
