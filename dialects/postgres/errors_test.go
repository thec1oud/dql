package postgres_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/thec1oud/dql/dialects/postgres"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

func TestSessionSetupAndErrorCodes(t *testing.T) {
	p := postgres.Profile()
	if !slices.Equal(p.GetSessionSetup(), []string{"SET TIME ZONE 'UTC'"}) {
		t.Errorf("session setup %q", p.GetSessionSetup())
	}
	byCode := map[string]irv1.ErrorKind{}
	var codes []string
	for _, c := range p.GetErrorCodes() {
		codes = append(codes, c.GetCode())
		byCode[c.GetCode()] = c.GetKind()
	}
	if !slices.IsSorted(codes) {
		t.Errorf("codes are not sorted: %v", codes)
	}
	for code, want := range map[string]irv1.ErrorKind{
		"23505": irv1.ErrorKind_ERROR_KIND_UNIQUE_VIOLATION,
		"23503": irv1.ErrorKind_ERROR_KIND_FOREIGN_KEY_VIOLATION,
		"23514": irv1.ErrorKind_ERROR_KIND_CHECK_VIOLATION,
		"23502": irv1.ErrorKind_ERROR_KIND_NOT_NULL_VIOLATION,
		"23P01": irv1.ErrorKind_ERROR_KIND_EXCLUSION_VIOLATION,
		"40001": irv1.ErrorKind_ERROR_KIND_SERIALIZATION_FAILURE,
		"40P01": irv1.ErrorKind_ERROR_KIND_DEADLOCK,
		"55P03": irv1.ErrorKind_ERROR_KIND_LOCK_TIMEOUT,
		"57014": irv1.ErrorKind_ERROR_KIND_TIMEOUT,
		"08006": irv1.ErrorKind_ERROR_KIND_CONNECTION,
	} {
		if byCode[code] != want {
			t.Errorf("%s is %v, want %v", code, byCode[code], want)
		}
	}
}

func col(name string, nullable, sensitive bool) *irv1.Column {
	c := &irv1.Column{Name: &irv1.Identifier{Stored: name}, Nullable: nullable}
	if sensitive {
		c.Annotations = []*irv1.Annotation{{Key: "core.sensitive"}}
	}
	return c
}

func describe(rs []*irv1.ViolationReport) string {
	var out []string
	for _, r := range rs {
		o := r.GetObject()
		s := fmt.Sprintf("%s.%s.%s %s %s", o.GetCatalog(), o.GetNamespace(), o.GetObject(), strings.TrimPrefix(r.GetKind().String(), "ERROR_KIND_"), r.GetReported())
		if r.GetRule() != r.GetReported() {
			s += " rule " + r.GetRule()
		}
		if r.GetRedact() {
			s += " redact"
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

// Every rule gets a report under its own name, sorted by object, kind and
// name. A key or row that may hold a sensitive value is redacted.
func TestViolations(t *testing.T) {
	users := &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: "users"}
	cats := []*irv1.Catalog{{
		Name: &irv1.Identifier{Stored: "shop"},
		Namespaces: []*irv1.Namespace{{
			Name: &irv1.Identifier{Stored: "app"},
			Tables: []*irv1.Table{
				{
					Name: &irv1.Identifier{Stored: "users"}, Kind: irv1.TableKind_RELATION_KIND_TABLE,
					Columns: []*irv1.Column{col("id", false, false), col("email", false, true), col("bio", true, false)},
					Constraints: []*irv1.Constraint{
						{Name: "users_bio_check", Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK, Columns: []string{"bio"}},
					},
					Uniqueness: []*irv1.Uniqueness{
						{Name: "users_email_key", Columns: []string{"email"}},
						{Name: "users_lower_bio_key", Expressions: []*irv1.IndexExpression{{Native: "lower(bio)"}}},
						{Name: "users_pkey", Columns: []string{"id"}},
					},
				},
				{
					Name: &irv1.Identifier{Stored: "posts"}, Kind: irv1.TableKind_RELATION_KIND_TABLE,
					Columns: []*irv1.Column{col("id", false, false), col("author_id", false, false), col("author_email", true, false), col("during", true, false)},
					Constraints: []*irv1.Constraint{
						{Name: "posts_during_excl", Kind: irv1.ConstraintKind_CONSTRAINT_KIND_EXCLUSION, Columns: []string{"during"}},
					},
					Uniqueness: []*irv1.Uniqueness{
						{Name: "posts_lower_title_key", Expressions: []*irv1.IndexExpression{{Native: "lower(title)"}}},
					},
					ForeignKeys: []*irv1.ForeignKey{
						{Name: &irv1.Identifier{Stored: "posts_author_email_fkey"}, Columns: []string{"author_email"}, References: &irv1.ColumnSet{Table: users, Columns: []string{"email"}}},
						{Name: &irv1.Identifier{Stored: "posts_author_id_fkey"}, Columns: []string{"author_id"}, References: &irv1.ColumnSet{Table: users, Columns: []string{"id"}}},
					},
				},
				{Name: &irv1.Identifier{Stored: "user_view"}, Kind: irv1.TableKind_RELATION_KIND_VIEW, Columns: []*irv1.Column{col("id", false, false)}},
			},
			Domains: []*irv1.Domain{{
				Name:        &irv1.Identifier{Stored: "email"},
				Constraints: []*irv1.Constraint{{Name: "email_check", Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK}},
			}},
		}},
	}}
	want := `shop.app.email CHECK_VIOLATION email_check
shop.app.posts UNIQUE_VIOLATION posts_lower_title_key
shop.app.posts FOREIGN_KEY_VIOLATION posts_author_email_fkey redact
shop.app.posts FOREIGN_KEY_VIOLATION posts_author_id_fkey
shop.app.posts NOT_NULL_VIOLATION author_id
shop.app.posts NOT_NULL_VIOLATION id
shop.app.posts EXCLUSION_VIOLATION posts_during_excl
shop.app.users UNIQUE_VIOLATION users_email_key redact
shop.app.users UNIQUE_VIOLATION users_lower_bio_key redact
shop.app.users UNIQUE_VIOLATION users_pkey
shop.app.users CHECK_VIOLATION users_bio_check redact
shop.app.users NOT_NULL_VIOLATION email redact
shop.app.users NOT_NULL_VIOLATION id redact`
	if got := describe(postgres.Violations(cats)); got != want {
		t.Errorf("violations\n got:\n%s\nwant:\n%s", got, want)
	}

	res, err := postgres.New().Invoke(context.Background(), &irv1.Invoke{Bundle: &irv1.Bundle{Dialect: irv1.Dialect_DIALECT_POSTGRES, Catalogs: cats}})
	if err != nil {
		t.Fatal(err)
	}
	if got := describe(res.GetBundle().GetCapabilities().GetViolations()); got != want {
		t.Errorf("invoke's violations\n got:\n%s", got)
	}
}
