package sqlserver_test

import (
	"slices"
	"testing"

	"github.com/thec1oud/dql/dialects/sqlserver"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestSessionSetupAndErrorCodes(t *testing.T) {
	p := sqlserver.Profile()
	if !slices.Equal(p.GetSessionSetup(), []string{"SET ANSI_NULLS ON", "SET QUOTED_IDENTIFIER ON", "SET ARITHABORT ON"}) {
		t.Errorf("session setup %v", p.GetSessionSetup())
	}
	kinds := map[string]irv1.ErrorKind{}
	for _, c := range p.GetErrorCodes() {
		kinds[c.GetCode()] = c.GetKind()
	}
	for code, want := range map[string]irv1.ErrorKind{
		"2627":      irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,
		"2601":      irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,
		"515":       irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION,
		"547":       irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION,
		"547/CHECK": irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION,
		"1205":      irv1.ErrorKind_ERROR_KIND_DEADLOCK,
		"1222":      irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT,
		"3960":      irv1.ErrorKind_ERROR_KIND_SERIALIZATION_FAILURE,
		"10054":     irv1.ErrorKind_ERROR_KIND_CONNECTION,
	} {
		if kinds[code] != want {
			t.Errorf("%s is %v, want %v", code, kinds[code], want)
		}
	}
}

// SQL Server names every rule it reports: a key, a check and a foreign key
// by name, a not-null violation by its column. A duplicate on a sensitive
// column is redacted, and views have no reports.
func TestViolations(t *testing.T) {
	users := &irv1.Table{
		Name: ident("users"), Kind: irv1.TableKind_RELATION_KIND_TABLE,
		Columns: []*irv1.Column{
			{Name: ident("id")},
			{Name: ident("token"), Annotations: []*irv1.Annotation{{Key: "core.sensitive"}}},
			{Name: ident("nick"), Nullable: true},
		},
		Constraints: []*irv1.Constraint{{Name: "users_nick_check"}},
		Uniqueness: []*irv1.Uniqueness{
			{Name: "users_pkey", Columns: []string{"id"}},
			{Name: "users_token_key", Columns: []string{"token"}},
		},
		ForeignKeys: []*irv1.ForeignKey{{Name: ident("users_manager"), Columns: []string{"id"}}},
	}
	cats := []*irv1.Catalog{{Name: ident("shop"), Namespaces: []*irv1.Namespace{{Name: ident("app"), Tables: []*irv1.Table{
		users, {Name: ident("v"), Kind: irv1.TableKind_RELATION_KIND_VIEW, Columns: []*irv1.Column{{Name: ident("id")}}},
	}}}}}
	type row struct {
		kind           irv1.ErrorKind
		reported, rule string
		redact         bool
	}
	var got []row
	for _, v := range sqlserver.Violations(cats) {
		if o := v.GetObject(); o.GetCatalog() != "shop" || o.GetNamespace() != "app" || o.GetObject() != "users" {
			t.Errorf("report %v", v)
		}
		got = append(got, row{v.GetKind(), v.GetReported(), v.GetRule(), v.GetRedact()})
	}
	want := []row{
		{irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION, "users_pkey", "users_pkey", false},
		{irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION, "users_token_key", "users_token_key", true},
		{irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION, "users_manager", "users_manager", false},
		{irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION, "users_nick_check", "users_nick_check", false},
		{irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION, "id", "id", false},
		{irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION, "token", "token", false},
	}
	if !slices.Equal(got, want) {
		t.Errorf("violations\n got %v\nwant %v", got, want)
	}
}

func TestLimits(t *testing.T) {
	p := sqlserver.Profile()
	if p.GetMaxParams() != 2100 || p.GetReplication() != nil ||
		!slices.Equal(p.GetConsistencyLevels(), []irv1.ConsistencyLevel{irv1.ConsistencyLevel_CONSISTENCY_PRIMARY}) ||
		!slices.Equal(p.GetStatementModes(), []irv1.StatementMode{irv1.StatementMode_STATEMENT_MODE_PREPARE}) {
		t.Errorf("profile %v", p)
	}
}
