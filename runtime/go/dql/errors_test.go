package dql_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/thec1oud/dql/runtime/go/dql"
)

var origin = dql.Origin{Dialect: "postgres", Code: "23505", Query: "users.insert"}

var tenantEmailKey = dql.Constraint{
	Name: "shop.app.users.users_tenant_email_key",
	Kind: dql.ConstraintUnique,
}

func TestErrors(t *testing.T) {
	violation := dql.Violation{
		Origin:     origin,
		Constraint: tenantEmailKey,
		Detail:     "Key (tenant_id, email)=(1, a@x.com) already exists.",
	}
	tests := []struct {
		err   dql.Error
		class dql.Class
		msg   string
	}{
		{&dql.NotFound{Origin: dql.Origin{Query: "users.get"}}, dql.ClassNotFound,
			"dql: NotFound in users.get"},
		{&dql.TooManyRows{Origin: dql.Origin{Query: "users.one"}}, dql.ClassTooManyRows,
			"dql: TooManyRows in users.one"},
		{&dql.UniqueViolation{violation}, dql.ClassUniqueViolation,
			"dql: UniqueViolation of shop.app.users.users_tenant_email_key in users.insert (postgres 23505)"},
		{&dql.ForeignKeyViolation{violation}, dql.ClassForeignKeyViolation,
			"dql: ForeignKeyViolation of shop.app.users.users_tenant_email_key in users.insert (postgres 23505)"},
		{&dql.CheckViolation{violation}, dql.ClassCheckViolation,
			"dql: CheckViolation of shop.app.users.users_tenant_email_key in users.insert (postgres 23505)"},
		{&dql.ExclusionViolation{violation}, dql.ClassExclusionViolation,
			"dql: ExclusionViolation of shop.app.users.users_tenant_email_key in users.insert (postgres 23505)"},
		{&dql.NotNullViolation{Violation: violation}, dql.ClassNotNullViolation,
			"dql: NotNullViolation of shop.app.users.users_tenant_email_key in users.insert (postgres 23505)"},
		{&dql.NotNullViolation{Column: "shop.app.users.email", Violation: dql.Violation{Origin: dql.Origin{Dialect: "sqlite"}}},
			dql.ClassNotNullViolation, "dql: NotNullViolation in shop.app.users.email (sqlite)"},
		{&dql.SerializationFailure{Origin: dql.Origin{Code: "40001"}}, dql.ClassSerializationFailure,
			"dql: SerializationFailure (40001)"},
		{&dql.Deadlock{}, dql.ClassDeadlock, "dql: Deadlock"},
		{&dql.LockTimeout{}, dql.ClassLockTimeout, "dql: LockTimeout"},
		{&dql.ConcurrentModification{Origin: dql.Origin{Query: "posts.patch"}}, dql.ClassConcurrentModification,
			"dql: ConcurrentModification in posts.patch"},
		{&dql.PoolExhausted{Source: "primary"}, dql.ClassPoolExhausted, "dql: PoolExhausted on source primary"},
		{&dql.ReplicaUnavailable{}, dql.ClassReplicaUnavailable, "dql: ReplicaUnavailable"},
		{&dql.ConnectionError{Source: "primary", Err: io.EOF}, dql.ClassConnectionError,
			"dql: ConnectionError on source primary: EOF"},
		{&dql.ConnectionError{}, dql.ClassConnectionError, "dql: ConnectionError"},
		{&dql.Timeout{Err: context.DeadlineExceeded}, dql.ClassTimeout,
			"dql: Timeout: context deadline exceeded"},
		{&dql.Canceled{Err: context.Canceled}, dql.ClassCanceled, "dql: Canceled: context canceled"},
		{&dql.DatabaseError{Origin: dql.Origin{Dialect: "postgres", Code: "22P02"},
			Message: `invalid input syntax for type integer: "abc"`}, dql.ClassDatabaseError,
			"dql: DatabaseError (postgres 22P02)"},
	}
	for _, tt := range tests {
		if got := tt.err.Class(); got != tt.class {
			t.Errorf("%T.Class() = %q, want %q", tt.err, got, tt.class)
		}
		if got := tt.err.Error(); got != tt.msg {
			t.Errorf("%T.Error() = %q, want %q", tt.err, got, tt.msg)
		}
		wrapped := fmt.Errorf("creating user: %w", tt.err)
		if got := dql.ClassOf(wrapped); got != tt.class {
			t.Errorf("ClassOf(%T) = %q", tt.err, got)
		}
	}
}

func TestClassOfOther(t *testing.T) {
	if got := dql.ClassOf(nil); got != "" {
		t.Errorf("ClassOf(nil) = %q", got)
	}
	if got := dql.ClassOf(io.EOF); got != "" {
		t.Errorf("ClassOf(EOF) = %q", got)
	}
}

func TestUnwrap(t *testing.T) {
	if err := error(&dql.Timeout{Err: context.DeadlineExceeded}); !errors.Is(err, context.DeadlineExceeded) {
		t.Error("Timeout does not unwrap")
	}
	if err := error(&dql.Canceled{Err: context.Canceled}); !errors.Is(err, context.Canceled) {
		t.Error("Canceled does not unwrap")
	}
	if err := error(&dql.ConnectionError{Err: io.EOF}); !errors.Is(err, io.EOF) {
		t.Error("ConnectionError does not unwrap")
	}
}

func TestIsViolation(t *testing.T) {
	err := fmt.Errorf("insert: %w", &dql.UniqueViolation{dql.Violation{Constraint: tenantEmailKey}})
	if !dql.IsViolation(err, tenantEmailKey) {
		t.Error("IsViolation is false for its constraint")
	}
	other := dql.Constraint{Name: "shop.app.users.users_pkey", Kind: dql.ConstraintPrimaryKey}
	if dql.IsViolation(err, other) {
		t.Error("IsViolation is true for another constraint")
	}
	if dql.IsViolation(&dql.NotFound{}, tenantEmailKey) {
		t.Error("IsViolation is true for NotFound")
	}
	var nf *dql.NotFound
	if !errors.As(fmt.Errorf("get: %w", &dql.NotFound{}), &nf) {
		t.Error("errors.As does not find NotFound")
	}
	v := &dql.CheckViolation{dql.Violation{Constraint: other}}
	if v.Violated() != other {
		t.Error("Violated")
	}
}
