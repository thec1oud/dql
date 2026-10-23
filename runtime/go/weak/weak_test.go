package weak_test

import (
	"testing"

	"github.com/thec1oud/dql/runtime/go/storage"
	"github.com/thec1oud/dql/runtime/go/weak"
)

func TestTyped(t *testing.T) {
	w := weak.Of("signup")
	if v, ok := w.Get(); !ok || v != "signup" {
		t.Fatalf("Get() = %q, %v", v, ok)
	}
	if w.Raw() != nil {
		t.Fatal("a typed value has a raw side")
	}
	if got := w.String(); got != "signup" {
		t.Fatalf("String() = %q", got)
	}

	var zero weak.Weak[int64]
	if v, ok := zero.Get(); !ok || v != 0 {
		t.Fatalf("zero Get() = %d, %v", v, ok)
	}
}

func TestRaw(t *testing.T) {
	// §8.10: another writer stored TEXT in an INTEGER column.
	w := weak.Raw[int64](storage.Text("three"))
	if v, ok := w.Get(); ok || v != 0 {
		t.Fatalf("Get() = %d, %v", v, ok)
	}
	if got := w.Raw(); got != storage.Text("three") {
		t.Fatalf("Raw() = %#v", got)
	}
	if got := w.String(); got != "raw text(three)" {
		t.Fatalf("String() = %q", got)
	}

	// A nullable weak column is a pointer around the value.
	var attempts *weak.Weak[int64] = &w
	if _, ok := attempts.Get(); ok {
		t.Fatal("raw value reads as typed")
	}
}

func TestRawNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Raw(nil) did not panic")
		}
	}()
	weak.Raw[int64](nil)
}
