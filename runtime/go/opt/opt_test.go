package opt_test

import (
	"encoding/json"
	"testing"

	"github.com/thec1oud/dql/runtime/go/opt"
)

func TestPresence(t *testing.T) {
	var zero opt.Opt[int]
	if zero != opt.None[int]() {
		t.Fatal("the zero Opt is not None")
	}
	if v, ok := zero.Get(); ok || v != 0 {
		t.Fatalf("None.Get() = %v, %v", v, ok)
	}
	if zero.IsSome() || !zero.IsZero() {
		t.Fatal("None reports present")
	}
	s := opt.Some(0)
	if v, ok := s.Get(); !ok || v != 0 {
		t.Fatalf("Some(0).Get() = %v, %v", v, ok)
	}
	if !s.IsSome() || s.IsZero() {
		t.Fatal("Some(0) reports absent")
	}
	if got := zero.Or(5); got != 5 {
		t.Fatalf("None.Or(5) = %d", got)
	}
	if got := s.Or(5); got != 0 {
		t.Fatalf("Some(0).Or(5) = %d", got)
	}
}

func TestThreeStates(t *testing.T) {
	// Opt[*T] keeps absent, NULL and a value apart (§14.1).
	absent := opt.None[*string]()
	null := opt.Some[*string](nil)
	ann := "Ann"
	value := opt.Some(&ann)
	if absent == null || null == value || absent == value {
		t.Fatal("states collapse")
	}
	if v, ok := null.Get(); !ok || v != nil {
		t.Fatal("NULL is not a present nil")
	}
}

func TestString(t *testing.T) {
	if got := opt.Some("a").String(); got != "Some(a)" {
		t.Fatalf("got %q", got)
	}
	if got := opt.None[string]().String(); got != "None" {
		t.Fatalf("got %q", got)
	}
}

func TestMap(t *testing.T) {
	double := func(n int) int { return 2 * n }
	if got := opt.Map(opt.Some(2), double); got != opt.Some(4) {
		t.Fatalf("got %v", got)
	}
	if got := opt.Map(opt.None[int](), double); got != opt.None[int]() {
		t.Fatalf("got %v", got)
	}
}

type draft struct {
	Title       opt.Opt[string]  `json:"title,omitzero"`
	DisplayName opt.Opt[*string] `json:"display_name,omitzero"`
}

func TestMarshalJSON(t *testing.T) {
	ann := "Ann"
	tests := []struct {
		in   draft
		want string
	}{
		{draft{}, `{}`},
		{draft{Title: opt.Some("")}, `{"title":""}`},
		{draft{DisplayName: opt.Some[*string](nil)}, `{"display_name":null}`},
		{draft{DisplayName: opt.Some(&ann)}, `{"display_name":"Ann"}`},
	}
	for _, tt := range tests {
		b, err := json.Marshal(tt.in)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tt.want {
			t.Errorf("Marshal(%+v) = %s, want %s", tt.in, b, tt.want)
		}
	}
	// Without omitzero an absent value encodes as null.
	b, err := json.Marshal(struct{ A opt.Opt[int] }{})
	if err != nil || string(b) != `{"A":null}` {
		t.Fatalf("got %s, %v", b, err)
	}
	if _, err := json.Marshal(opt.Some(func() {})); err == nil {
		t.Fatal("an unencodable value encoded")
	}
}

func TestUnmarshalJSON(t *testing.T) {
	var d draft
	if err := json.Unmarshal([]byte(`{}`), &d); err != nil {
		t.Fatal(err)
	}
	if d.Title.IsSome() || d.DisplayName.IsSome() {
		t.Fatal("missing fields are present")
	}

	d = draft{}
	if err := json.Unmarshal([]byte(`{"title":"","display_name":null}`), &d); err != nil {
		t.Fatal(err)
	}
	if d.Title != opt.Some("") {
		t.Fatalf("title = %v", d.Title)
	}
	if v, ok := d.DisplayName.Get(); !ok || v != nil {
		t.Fatalf("display_name = %v", d.DisplayName)
	}

	d = draft{}
	if err := json.Unmarshal([]byte(`{"display_name":"Ann"}`), &d); err != nil {
		t.Fatal(err)
	}
	if v, ok := d.DisplayName.Get(); !ok || *v != "Ann" {
		t.Fatalf("display_name = %v", d.DisplayName)
	}
}

func TestUnmarshalJSONNull(t *testing.T) {
	// null for a non-null field is an error, never a silent zero value.
	var d draft
	if err := json.Unmarshal([]byte(`{"title": null }`), &d); err == nil {
		t.Fatal("null accepted for a non-null field")
	}
	var s opt.Opt[[]int]
	if err := json.Unmarshal([]byte(`null`), &s); err == nil {
		t.Fatal("null accepted for a slice")
	}

	// Interfaces and types that decode JSON themselves take null.
	var a opt.Opt[any]
	if err := json.Unmarshal([]byte(`null`), &a); err != nil {
		t.Fatal(err)
	}
	if v, ok := a.Get(); !ok || v != nil {
		t.Fatalf("got %v", a)
	}
	var r opt.Opt[json.RawMessage]
	if err := json.Unmarshal([]byte(`null`), &r); err != nil {
		t.Fatal(err)
	}
	if v, ok := r.Get(); !ok || string(v) != "null" {
		t.Fatalf("got %v", r)
	}
}

func TestUnmarshalJSONMismatch(t *testing.T) {
	var o opt.Opt[int]
	if err := json.Unmarshal([]byte(`"x"`), &o); err == nil {
		t.Fatal("a string decoded into an int")
	}
	if o.IsSome() {
		t.Fatal("a failed decode made the value present")
	}
}
