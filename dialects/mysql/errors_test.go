package mysql_test

import (
	"slices"
	"testing"

	"github.com/thec1oud/dql/dialects/mysql"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestSessionSetupAndErrorCodes(t *testing.T) {
	p := mysql.Profile()
	if !slices.Equal(p.GetSessionSetup(), []string{
		"SET time_zone = '+00:00'",
		"SET SESSION sql_mode = CONCAT_WS(',', @@SESSION.sql_mode, 'STRICT_ALL_TABLES')",
	}) {
		t.Errorf("session setup %v", p.GetSessionSetup())
	}
	kinds := map[string]irv1.ErrorKind{}
	var codes []string
	for _, c := range p.GetErrorCodes() {
		kinds[c.GetCode()] = c.GetKind()
		codes = append(codes, c.GetCode())
	}
	if !slices.IsSorted(codes) {
		t.Error("codes are not sorted")
	}
	for code, want := range map[string]irv1.ErrorKind{
		"1062": irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,
		"1048": irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION,
		"1452": irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION,
		"3819": irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION,
		"4025": irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION,
		"1213": irv1.ErrorKind_ERROR_KIND_DEADLOCK,
		"1205": irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT,
		"3572": irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT,
		"3024": irv1.ErrorKind_ERROR_KIND_TIMEOUT,
		"2013": irv1.ErrorKind_ERROR_KIND_CONNECTION,
	} {
		if kinds[code] != want {
			t.Errorf("%s is %v, want %v", code, kinds[code], want)
		}
	}
}

// Every rule of a base table is reported by its own name; only a
// duplicate's message holds values, so only it is redacted, when its key
// has a sensitive column or an expression.
func TestViolations(t *testing.T) {
	sensitive := []*irv1.Annotation{{Key: "core.sensitive"}}
	users := &irv1.Table{
		Name: ident("users"), Kind: irv1.TableKind_RELATION_KIND_TABLE,
		Columns: []*irv1.Column{
			{Name: ident("id")}, {Name: ident("email"), Annotations: sensitive}, {Name: ident("nick"), Nullable: true},
		},
		Constraints: []*irv1.Constraint{{Name: "users_nick_check"}},
		Uniqueness: []*irv1.Uniqueness{
			{Name: "PRIMARY", Columns: []string{"id"}},
			{Name: "users_email", Columns: []string{"email"}},
			{Name: "users_lower_nick", Expressions: []*irv1.IndexExpression{{Native: "lower(`nick`)"}}},
		},
		ForeignKeys: []*irv1.ForeignKey{{Name: ident("users_manager"), Columns: []string{"id"}}},
	}
	cats := []*irv1.Catalog{{Name: ident("shop"), Namespaces: []*irv1.Namespace{{Name: &irv1.Identifier{}, Tables: []*irv1.Table{
		users, {Name: ident("v"), Kind: irv1.TableKind_RELATION_KIND_VIEW, Columns: []*irv1.Column{{Name: ident("id")}}},
	}}}}}
	type row struct {
		kind   irv1.ErrorKind
		rule   string
		redact bool
	}
	var got []row
	for _, v := range mysql.Violations(cats) {
		if v.GetObject().GetObject() != "users" || v.GetReported() != v.GetRule() {
			t.Errorf("report %v", v)
		}
		got = append(got, row{v.GetKind(), v.GetRule(), v.GetRedact()})
	}
	want := []row{
		{irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION, "PRIMARY", false},
		{irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION, "users_email", true},
		{irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION, "users_lower_nick", true},
		{irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION, "users_manager", false},
		{irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION, "users_nick_check", false},
		{irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION, "email", false},
		{irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION, "id", false},
	}
	if !slices.Equal(got, want) {
		t.Errorf("violations\n got %v\nwant %v", got, want)
	}
}

func TestLimits(t *testing.T) {
	p := mysql.Profile()
	if p.GetMaxParams() != 65535 || p.GetReplication() != nil ||
		!slices.Equal(p.GetConsistencyLevels(), []irv1.ConsistencyLevel{irv1.ConsistencyLevel_CONSISTENCY_PRIMARY, irv1.ConsistencyLevel_CONSISTENCY_ANY}) ||
		!slices.Equal(p.GetStatementModes(), []irv1.StatementMode{irv1.StatementMode_STATEMENT_MODE_PREPARE, irv1.StatementMode_STATEMENT_MODE_SIMPLE}) {
		t.Errorf("profile %v", p)
	}
}
