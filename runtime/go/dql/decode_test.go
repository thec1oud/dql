package dql_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/thec1oud/dql/runtime/go/dql"
)

func TestDecodeError(t *testing.T) {
	// §13: NULL in a view column asserted nonnull.
	err := error(&dql.DecodeError{
		Shape:     "published_post.full.flat",
		Field:     "title",
		Reason:    dql.NullIntoNonNull,
		Assertion: "core.nonnull from COMMENT ON COLUMN app.published_posts.title",
	})
	want := "dql: DecodeError published_post.full.flat field title: null_into_non_null, " +
		"asserted by core.nonnull from COMMENT ON COLUMN app.published_posts.title"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	var de *dql.DecodeError
	if !errors.As(fmt.Errorf("scan: %w", err), &de) || de.Reason != dql.NullIntoNonNull {
		t.Error("errors.As does not find the DecodeError")
	}
	if dql.ClassOf(err) != dql.ClassDecodeError {
		t.Errorf("class = %q", dql.ClassOf(err))
	}

	err = &dql.DecodeError{Shape: "post.full.flat", Reason: dql.OffType, Origin: dql.Origin{Query: "posts.list"}}
	if want := "dql: DecodeError post.full.flat: off_type in posts.list"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}
