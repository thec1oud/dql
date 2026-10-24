package dql_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/thec1oud/dql/runtime/go/dql"
)

func TestBits(t *testing.T) {
	b, err := dql.ParseBits("1010011")
	if err != nil {
		t.Fatal(err)
	}
	if b.Len() != 7 || b.String() != "1010011" {
		t.Fatalf("got %d, %s", b.Len(), b)
	}
	if !bytes.Equal(b.Bytes(), []byte{0xa6}) {
		t.Fatalf("Bytes() = %x", b.Bytes())
	}
	want := []bool{true, false, true, false, false, true, true}
	for i, w := range want {
		if b.Bit(i) != w {
			t.Errorf("Bit(%d) = %v", i, !w)
		}
	}
	out := b.Bytes()
	out[0] = 0 // Bytes copies
	if b.String() != "1010011" {
		t.Fatal("Bytes shares its value")
	}

	var zero dql.Bits
	if zero.Len() != 0 || zero.String() != "" || !zero.Equal(dql.Bits{}) {
		t.Fatalf("zero Bits = %q", zero)
	}
	if e, err := dql.ParseBits(""); err != nil || !e.Equal(zero) {
		t.Fatalf("ParseBits(\"\") = %v, %v", e, err)
	}
}

func TestNewBits(t *testing.T) {
	data := []byte{0xa7, 0xff}
	b, err := dql.NewBits(data, 7)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 0 // NewBits copies
	// The eighth bit of 0xa7 is padding and is dropped.
	if p, _ := dql.ParseBits("1010011"); !b.Equal(p) {
		t.Fatalf("NewBits = %s", b)
	}
	if b16, _ := dql.NewBits([]byte{0xf0, 0x0f}, 16); b16.String() != "1111000000001111" {
		t.Fatalf("NewBits(16) = %s", b16)
	}
	for _, n := range []int{-1, 17} {
		if _, err := dql.NewBits([]byte{1, 2}, n); err == nil {
			t.Errorf("NewBits(n=%d) succeeded", n)
		}
	}
}

func TestBitsEqual(t *testing.T) {
	a, _ := dql.ParseBits("10")
	b, _ := dql.ParseBits("100")
	c, _ := dql.ParseBits("11")
	if a.Equal(b) || a.Equal(c) || !a.Equal(a) {
		t.Fatal("Equal")
	}
}

func TestBitsErrors(t *testing.T) {
	if _, err := dql.ParseBits("1012"); err == nil {
		t.Error("ParseBits(1012) succeeded")
	}
	b, _ := dql.ParseBits("1")
	for _, i := range []int{-1, 1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Bit(%d) did not panic", i)
				}
			}()
			b.Bit(i)
		}()
	}
}

func TestBitsText(t *testing.T) {
	var v struct{ B dql.Bits }
	if err := json.Unmarshal([]byte(`{"B":"0110"}`), &v); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(v)
	if err != nil || string(out) != `{"B":"0110"}` {
		t.Fatalf("round trip = %s, %v", out, err)
	}
	if err := json.Unmarshal([]byte(`{"B":"x"}`), &v); err == nil {
		t.Error("x decoded")
	}
}
