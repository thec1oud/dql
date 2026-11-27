package annotate

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func unmarshal(t *testing.T, js string, m proto.Message) {
	t.Helper()
	if err := protojson.Unmarshal([]byte(js), m); err != nil {
		t.Fatal(err)
	}
}

func equal(t *testing.T, got, want proto.Message) {
	t.Helper()
	if !proto.Equal(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

// §7.5: two comment annotations on users.password_hash, sorted by key.
func TestTraceWriteOnlySensitive(t *testing.T) {
	at := origin(irv1.OriginKind_ORIGIN_DB_COMMENT, "COMMENT ON COLUMN app.users.password_hash")
	read := []*irv1.Annotation{
		{Key: "core.write-only", Origin: at},
		{Key: "core.sensitive", Origin: at},
	}
	anns, ds := Merge(q("users", "password_hash"), read)
	wantCodes(t, ds)
	col := &irv1.Column{
		Name:        &irv1.Identifier{Stored: "password_hash", Words: []string{"password", "hash"}},
		Ordinal:     5,
		Type:        textType,
		Annotations: anns,
	}
	want := &irv1.Column{}
	unmarshal(t, `{
	  "name": { "stored": "password_hash", "words": ["password", "hash"] },
	  "ordinal": 5,
	  "type": { "text": {} },
	  "nullable": false,
	  "annotations": [
	    { "key": "core.sensitive",
	      "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live",
	                  "location": "COMMENT ON COLUMN app.users.password_hash" } },
	    { "key": "core.write-only",
	      "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live",
	                  "location": "COMMENT ON COLUMN app.users.password_hash" } }
	  ]
	}`, want)
	equal(t, col, want)
	wantCodes(t, Check(column(textType, true), anns))
}

// §7.6: config inverse-name: profile beats comment @inverse-name(user_profile),
// and the comment is kept as superseded.
func TestTraceConfigOverridesComment(t *testing.T) {
	fk := q("profiles", "profiles_user_id_fkey")
	read := []*irv1.Annotation{
		{Key: "core.inverse-name", Value: words("user", "profile"),
			Origin: origin(irv1.OriginKind_ORIGIN_DB_COMMENT, "COMMENT ON CONSTRAINT profiles_user_id_fkey ON app.profiles")},
		{Key: "core.inverse-name", Value: words("profile"),
			Origin: origin(irv1.OriginKind_ORIGIN_CONFIG, "dql.yaml#annotations.shop.app.profiles.profiles_user_id_fkey")},
	}
	anns, ds := Merge(fk, read)
	wantCodes(t, ds)
	rel := &irv1.Relation{
		Name:        &irv1.Identifier{Stored: "profile", Words: []string{"profile"}},
		Kind:        irv1.RelationKind_RELATION_KIND_INVERSE_TO_ONE,
		Via:         fk,
		Annotations: anns,
	}
	want := &irv1.Relation{}
	unmarshal(t, `{
	  "name": { "stored": "profile", "words": ["profile"] },
	  "kind": "RELATION_KIND_INVERSE_TO_ONE",
	  "via": { "catalog": "shop", "namespace": "app", "object": "profiles", "member": "profiles_user_id_fkey" },
	  "annotations": [
	    {
	      "key": "core.inverse-name",
	      "value": { "words": ["profile"] },
	      "origin": { "kind": "ORIGIN_CONFIG", "plugin": "pg-live",
	                  "location": "dql.yaml#annotations.shop.app.profiles.profiles_user_id_fkey" },
	      "superseded": [
	        { "key": "core.inverse-name", "value": { "words": ["user", "profile"] },
	          "origin": { "kind": "ORIGIN_DB_COMMENT", "plugin": "pg-live",
	                      "location": "COMMENT ON CONSTRAINT profiles_user_id_fkey ON app.profiles" } }
	      ]
	    }
	  ]
	}`, want)
	equal(t, rel, want)
	wantCodes(t, Check(Target{Kind: KindForeignKey, Name: fk, NotNull: true}, anns))
}

// §7.7: a plugin annotation passes through untouched.
func TestTracePluginAnnotation(t *testing.T) {
	read := []*irv1.Annotation{config("x.ts.time-type", str("date"))}
	anns, ds := Merge(q("posts", "published_at"), read)
	wantCodes(t, ds)
	want := &irv1.Annotation{}
	unmarshal(t, `{ "key": "x.ts.time-type", "value": { "string": "date" },
	  "origin": { "kind": "ORIGIN_CONFIG", "plugin": "pg-live", "location": "dql.yaml" } }`, want)
	equal(t, anns[0], want)
	wantCodes(t, Check(column(instant, false), anns))
}

// §7.1: config, then comment, then default; losers highest first.
func TestMergePrecedence(t *testing.T) {
	read := []*irv1.Annotation{
		inferred("core.name", words("profiles")),
		comment("core.name", words("user", "profile")),
		comment("core.nonnull", nil),
		config("core.name", words("profile")),
		inferred("core.collection-policy", str("merge")),
	}
	before := make([]*irv1.Annotation, len(read))
	for i, a := range read {
		before[i] = proto.CloneOf(a)
	}
	anns, ds := Merge(q("profiles", ""), read)
	wantCodes(t, ds)

	var keys []string
	for _, a := range anns {
		keys = append(keys, a.GetKey())
	}
	if want := []string{"core.collection-policy", "core.name", "core.nonnull"}; !equalStrings(keys, want) {
		t.Errorf("keys = %q, want %q", keys, want)
	}
	name := anns[1]
	equal(t, name.GetValue(), words("profile"))
	if len(name.GetSuperseded()) != 2 ||
		name.GetSuperseded()[0].GetOrigin().GetKind() != irv1.OriginKind_ORIGIN_DB_COMMENT ||
		name.GetSuperseded()[1].GetOrigin().GetKind() != irv1.OriginKind_ORIGIN_INFERRED {
		t.Errorf("superseded = %v", name.GetSuperseded())
	}
	equal(t, name.GetSuperseded()[0].GetOrigin(), read[1].GetOrigin())
	for i := range read {
		equal(t, read[i], before[i])
	}
}

// A default alone is effective, and a comment beats it.
func TestMergeCommentOverDefault(t *testing.T) {
	anns, _ := Merge(q("t", ""), []*irv1.Annotation{inferred("core.name", words("a")), comment("core.name", words("b"))})
	equal(t, anns[0].GetValue(), words("b"))
	equal(t, anns[0].GetSuperseded()[0].GetValue(), words("a"))
	anns, _ = Merge(q("t", ""), []*irv1.Annotation{inferred("core.name", words("a"))})
	if len(anns) != 1 || len(anns[0].GetSuperseded()) != 0 {
		t.Errorf("anns = %v", anns)
	}
}

func TestMergeSameSource(t *testing.T) {
	target := q("users", "password_hash")
	// @nonnull @nonnull: the first wins, with a warning.
	anns, ds := Merge(target, []*irv1.Annotation{comment("core.nonnull", nil), comment("core.nonnull", nil)})
	wantCodes(t, ds, CodeDuplicate)
	if len(anns) != 1 || len(anns[0].GetSuperseded()) != 1 {
		t.Errorf("anns = %v", anns)
	}
	// Two values from one source conflict.
	anns, ds = Merge(target, []*irv1.Annotation{comment("core.name", words("a")), comment("core.name", words("b"))})
	wantCodes(t, ds, CodeConflict)
	equal(t, anns[0].GetValue(), words("a"))
	// Lower-precedence duplicates are only superseded.
	_, ds = Merge(target, []*irv1.Annotation{
		comment("core.name", words("a")), comment("core.name", words("b")), config("core.name", words("c")),
	})
	wantCodes(t, ds)
}

// §7.1: every annotation records where it came from.
func TestMergeOriginMissing(t *testing.T) {
	anns, ds := Merge(q("t", ""), []*irv1.Annotation{
		{Key: "core.name", Value: words("a")},
		comment("core.name", words("b")),
	})
	wantCodes(t, ds, CodeOriginMissing)
	equal(t, anns[0].GetValue(), words("b"))
	equal(t, anns[0].GetSuperseded()[0].GetValue(), words("a"))
}

func TestMergeNothing(t *testing.T) {
	anns, ds := Merge(q("t", ""), nil)
	if len(anns) != 0 || len(ds) != 0 {
		t.Error("merging nothing produced output")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
