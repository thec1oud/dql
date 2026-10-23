package storage_test

import (
	"testing"

	"github.com/thec1oud/dql/runtime/go/storage"
)

func TestClasses(t *testing.T) {
	tests := []struct {
		v    storage.Value
		want storage.Class
		name string
	}{
		{storage.Integer(1), storage.ClassInteger, "integer"},
		{storage.Real(1.5), storage.ClassReal, "real"},
		{storage.Text("three"), storage.ClassText, "text"},
		{storage.Blob{0x01}, storage.ClassBlob, "blob"},
	}
	for _, tt := range tests {
		if got := tt.v.Class(); got != tt.want {
			t.Errorf("%#v.Class() = %v, want %v", tt.v, got, tt.want)
		}
		if got := tt.want.String(); got != tt.name {
			t.Errorf("String() = %q, want %q", got, tt.name)
		}
	}
	if got := storage.Class(0).String(); got != "Class(0)" {
		t.Errorf("String() = %q", got)
	}
}
