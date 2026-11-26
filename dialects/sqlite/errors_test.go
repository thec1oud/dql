package sqlite_test

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/thec1oud/dql/dialects/sqlite"
	sqliteextv1 "github.com/thec1oud/dql/proto/dql/ext/sqlite/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestSessionSetupAndErrorCodes(t *testing.T) {
	p := sqlite.Profile()
	if !slices.Equal(p.GetSessionSetup(), []string{"PRAGMA foreign_keys = ON", "PRAGMA case_sensitive_like = ON"}) {
		t.Errorf("session setup %v", p.GetSessionSetup())
	}
	kinds := map[string]irv1.ErrorKind{}
	for _, c := range p.GetErrorCodes() {
		kinds[c.GetCode()] = c.GetKind()
	}
	for code, want := range map[string]irv1.ErrorKind{
		"2067": irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,
		"1555": irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,
		"1299": irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION,
		"787":  irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION,
		"275":  irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION,
		"5":    irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT,
		"517":  irv1.ErrorKind_ERROR_KIND_SERIALIZATION_FAILURE,
		"9":    irv1.ErrorKind_ERROR_KIND_TIMEOUT,
		"14":   irv1.ErrorKind_ERROR_KIND_CONNECTION,
	} {
		if kinds[code] != want {
			t.Errorf("%s is %v, want %v", code, kinds[code], want)
		}
	}
}

// SQLite names a key's columns, an expression index by its name, a
// not-null column, and a check by its name or, unnamed, its expression. A
// foreign-key violation names nothing, so it has no report, and a second
// key on the same columns is not reported.
func TestViolations(t *testing.T) {
	check := func(expr string, named bool) []*anypb.Any {
		a, _ := anypb.New(&sqliteextv1.Check{Expression: expr, Named: named})
		return []*anypb.Any{a}
	}
	users := &irv1.Table{
		Name: ident("users"), Kind: irv1.TableKind_RELATION_KIND_TABLE,
		Columns: []*irv1.Column{{Name: ident("id")}, {Name: ident("email")}, {Name: ident("nick"), Nullable: true}},
		Constraints: []*irv1.Constraint{
			{Name: "users_nick_check", Extensions: check("length(nick) < 9", false)},
			{Name: "named", Extensions: check("nick <> ''", true)},
			{Name: "foreign"},
		},
		Uniqueness: []*irv1.Uniqueness{
			{Name: "users_email_key", Columns: []string{"id", "email"}},
			{Name: "users_lower_nick", Expressions: []*irv1.IndexExpression{{Native: "lower(nick)"}}},
			{Name: "users_pkey", Columns: []string{"id", "email"}},
		},
		ForeignKeys: []*irv1.ForeignKey{{Name: ident("users_manager"), Columns: []string{"id"}}},
	}
	cats := []*irv1.Catalog{{Name: ident("shop"), Namespaces: []*irv1.Namespace{{Name: &irv1.Identifier{}, Tables: []*irv1.Table{
		users, {Name: ident("v"), Kind: irv1.TableKind_RELATION_KIND_VIEW, Columns: []*irv1.Column{{Name: ident("id")}}},
	}}}}}
	type row struct {
		kind           irv1.ErrorKind
		reported, rule string
	}
	var got []row
	for _, v := range sqlite.Violations(cats) {
		if v.GetObject().GetObject() != "users" || v.GetRedact() {
			t.Errorf("report %v", v)
		}
		got = append(got, row{v.GetKind(), v.GetReported(), v.GetRule()})
	}
	want := []row{
		{irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION, "id, email", "users_email_key"},
		{irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION, "users_lower_nick", "users_lower_nick"},
		{irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION, "foreign", "foreign"},
		{irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION, "length(nick) < 9", "users_nick_check"},
		{irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION, "named", "named"},
		{irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION, "email", "email"},
		{irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION, "id", "id"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("violations\n got %v\nwant %v", got, want)
	}
}

func TestLimits(t *testing.T) {
	p := sqlite.Profile()
	if p.GetMaxParams() != 32766 || p.GetReplication() != nil ||
		!slices.Equal(p.GetConsistencyLevels(), []irv1.ConsistencyLevel{irv1.ConsistencyLevel_CONSISTENCY_PRIMARY}) ||
		!slices.Equal(p.GetStatementModes(), []irv1.StatementMode{irv1.StatementMode_STATEMENT_MODE_PREPARE}) {
		t.Errorf("profile %v", p)
	}
}
