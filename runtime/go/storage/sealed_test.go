package storage

import "testing"

func TestSealed(t *testing.T) {
	// The unexported method keeps the union closed; calling it is a no-op.
	for _, v := range []Value{Integer(0), Real(0), Text(""), Blob(nil)} {
		v.sealed()
	}
}
