package dql_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/thec1oud/dql/runtime/go/dql"
	"github.com/thec1oud/dql/runtime/go/storage"
	"github.com/thec1oud/dql/runtime/go/weak"
)

// reason returns err's DecodeReason, Field and Shape, or empty ones for nil.
func reason(t *testing.T, err error) (dql.DecodeReason, string, string) {
	t.Helper()
	if err == nil {
		return "", "", ""
	}
	var e *dql.DecodeError
	if !errors.As(err, &e) {
		t.Fatalf("%T is not a *DecodeError", err)
	}
	return e.Reason, e.Field, e.Shape
}

type status string

func (s status) Valid() bool { return s == "draft" || s == "published" }

type post struct {
	ID     int64
	Title  string
	Tags   [][]*string
	Author *author
	Status status
}

type author struct {
	ID   int64
	Name *string
}

func decodeAuthor(r dql.Row) (author, error) {
	d := dql.NewDecoder("user.full.flat", r, 2)
	var out author
	out.ID = dql.Field(d, 0, "id", "", dql.Int[int64])
	out.Name = dql.Nullable(d, 1, "name", dql.String)
	return out, d.Err()
}

func decodePost(r dql.Row) (post, error) {
	d := dql.NewDecoder("published_post.full.nested", r, 5)
	var out post
	out.ID = dql.Field(d, 0, "id", "", dql.Int[int64])
	out.Title = dql.Field(d, 1, "title", "core.nonnull from COMMENT ON COLUMN app.published_posts.title", dql.String)
	out.Tags = dql.Field(d, 2, "tags", "", dql.Array(dql.Array(dql.NullableElem(dql.String))))
	out.Status = dql.Field(d, 3, "status", "", dql.Enum(status.Valid))
	out.Author = dql.Nullable(d, 4, "author", dql.Nest(decodeAuthor))
	return out, d.Err()
}

// §13: decoding never produces a silent zero value; each case is a
// DecodeError naming the shape, the field path and the reason.
func TestDecode(t *testing.T) {
	ok := dql.Row{int64(1), "Hello", []any{[]any{"a", nil}}, "draft", dql.Row{int64(7), "Ann"}}
	p, err := decodePost(ok)
	if err != nil || p.ID != 1 || p.Title != "Hello" || *p.Tags[0][0] != "a" || p.Tags[0][1] != nil || p.Status != "draft" || *p.Author.Name != "Ann" {
		t.Fatalf("decodePost = %+v, %v", p, err)
	}
	with := func(i int, v any) dql.Row {
		r := append(dql.Row(nil), ok...)
		r[i] = v
		return r
	}
	for _, c := range []struct {
		name   string
		row    dql.Row
		reason dql.DecodeReason
		field  string
		shape  string
		assert string
	}{
		{"null into asserted non-null", with(1, nil), dql.NullIntoNonNull, "title", "published_post.full.nested", "core.nonnull from COMMENT ON COLUMN app.published_posts.title"},
		{"null into declared non-null", with(0, nil), dql.NullIntoNonNull, "id", "published_post.full.nested", ""},
		{"unknown enum label", with(3, "archived"), dql.UnknownEnumLabel, "status", "published_post.full.nested", ""},
		{"too few dimensions", with(2, []any{"a"}), dql.DimensionMismatch, "tags[0]", "published_post.full.nested", ""},
		{"too many dimensions", with(2, []any{[]any{[]any{"a"}}}), dql.DimensionMismatch, "tags[0][0]", "published_post.full.nested", ""},
		{"not an array", with(2, "a"), dql.DimensionMismatch, "tags", "published_post.full.nested", ""},
		{"off-type element", with(2, []any{[]any{"a", int64(1)}}), dql.OffType, "tags[0][1]", "published_post.full.nested", ""},
		{"off-type value", with(0, "1"), dql.OffType, "id", "published_post.full.nested", ""},
		{"nested error keeps its shape", with(4, dql.Row{nil, "Ann"}), dql.NullIntoNonNull, "author.id", "user.full.flat", ""},
		{"nested off type", with(4, dql.Row{int64(7), int64(1)}), dql.OffType, "author.name", "user.full.flat", ""},
		{"nest not a row", with(4, "x"), dql.OffType, "author", "published_post.full.nested", ""},
		{"nested row of the wrong length", with(4, dql.Row{int64(7)}), dql.OffType, "author", "user.full.flat", ""},
		{"row of the wrong length", ok[:4], dql.OffType, "", "published_post.full.nested", ""},
	} {
		p, err := decodePost(c.row)
		r, f, s := reason(t, err)
		var e *dql.DecodeError
		errors.As(err, &e)
		if r != c.reason || f != c.field || s != c.shape || e.Assertion != c.assert || p.ID != 0 && c.field == "id" {
			t.Errorf("%s: %v (%q %q %q)", c.name, err, r, f, s)
		}
	}
	// A nullable nest and field take NULL.
	if p, err := decodePost(with(4, nil)); err != nil || p.Author != nil {
		t.Errorf("NULL author: %+v, %v", p, err)
	}
	if a, err := decodeAuthor(dql.Row{int64(1), nil}); err != nil || a.Name != nil {
		t.Errorf("NULL name: %+v, %v", a, err)
	}
}

func TestMany(t *testing.T) {
	decode := func(v any) ([]author, error) {
		d := dql.NewDecoder("user.full.nested", dql.Row{v}, 1)
		out := dql.Many(d, 0, "posts_to_author", decodeAuthor)
		return out, d.Err()
	}
	rows, err := decode([]dql.Row{{int64(1), nil}, {int64(2), "B"}})
	if err != nil || len(rows) != 2 || rows[1].ID != 2 {
		t.Fatalf("Many = %+v, %v", rows, err)
	}
	if rows, err := decode(nil); err != nil || rows != nil {
		t.Errorf("NULL rows: %+v, %v", rows, err)
	}
	if _, err := decode([]dql.Row{{int64(1), nil}, {nil, nil}}); err == nil || err.(*dql.DecodeError).Field != "posts_to_author[1].id" {
		t.Errorf("nested error: %v", err)
	}
	if _, err := decode("x"); err == nil || err.(*dql.DecodeError).Field != "posts_to_author" {
		t.Errorf("not rows: %v", err)
	}
	// Decoding stops at the first error.
	d := dql.NewDecoder("s", dql.Row{nil, int64(1), []dql.Row{}}, 3)
	if dql.Field(d, 0, "a", "", dql.Int[int64]) != 0 || dql.Nullable(d, 1, "b", dql.Int[int64]) != nil || dql.Many(d, 2, "c", decodeAuthor) != nil ||
		d.Err().(*dql.DecodeError).Field != "a" {
		t.Errorf("decoding went on: %v", d.Err())
	}
	if dql.Field(d, 1, "b", "", dql.Int[int64]) != 0 {
		t.Errorf("Field after an error")
	}
}

func TestConverters(t *testing.T) {
	check := func(name string, got any, err error, want any, fails bool) {
		t.Helper()
		if fails {
			if r, _, _ := reason(t, err); r != dql.OffType {
				t.Errorf("%s: %v, want OffType", name, err)
			}
			return
		}
		if err != nil || got != want {
			t.Errorf("%s = %v, %v; want %v", name, got, err, want)
		}
	}
	i8, err := dql.Int[int8](int64(-128))
	check("Int[int8](-128)", i8, err, int8(-128), false)
	_, err = dql.Int[int8](int64(128))
	check("Int[int8](128)", nil, err, nil, true)
	i64, err := dql.Int[int64](uint64(math.MaxInt64))
	check("Int[int64](uint max)", i64, err, int64(math.MaxInt64), false)
	_, err = dql.Int[int64](uint64(math.MaxInt64 + 1))
	check("Int[int64](2^63)", nil, err, nil, true)
	_, err = dql.Int[int32](1.0)
	check("Int[int32](float)", nil, err, nil, true)
	u16, err := dql.Uint[uint16](int64(65535))
	check("Uint[uint16](65535)", u16, err, uint16(65535), false)
	_, err = dql.Uint[uint16](int64(-1))
	check("Uint[uint16](-1)", nil, err, nil, true)
	_, err = dql.Uint[uint8](int64(256))
	check("Uint[uint8](256)", nil, err, nil, true)
	u64, err := dql.Uint[uint64](uint64(math.MaxUint64))
	check("Uint[uint64](max)", u64, err, uint64(math.MaxUint64), false)
	_, err = dql.Uint[uint32](uint64(1 << 32))
	check("Uint[uint32](2^32)", nil, err, nil, true)
	_, err = dql.Uint[uint32]("1")
	check("Uint[uint32](text)", nil, err, nil, true)
	f32, err := dql.Float[float32](0.5)
	check("Float[float32](0.5)", f32, err, float32(0.5), false)
	_, err = dql.Float[float32](0.1)
	check("Float[float32](0.1)", nil, err, nil, true)
	nan, err := dql.Float[float32](math.NaN())
	if err != nil || nan == nan {
		t.Errorf("Float[float32](NaN) = %v, %v", nan, err)
	}
	_, err = dql.Float[float64](int64(1))
	check("Float[float64](int)", nil, err, nil, true)
	for v, want := range map[any]bool{true: true, int64(0): false, int64(1): true, uint64(1): true, uint64(0): false} {
		b, err := dql.Bool(v)
		check("Bool", b, err, want, false)
	}
	for _, v := range []any{int64(2), uint64(2), "true"} {
		_, err := dql.Bool(v) // 2 in a TINYINT(1) annotated bool
		check("Bool", nil, err, nil, true)
	}
	s, err := dql.String("a")
	check("String", s, err, "a", false)
	_, err = dql.String([]byte("a"))
	check("String(bytes)", nil, err, nil, true)
	b, err := dql.Bytes([]byte("a"))
	if err != nil || string(b) != "a" {
		t.Errorf("Bytes = %q, %v", b, err)
	}
	now := time.Now()
	tm, err := dql.As[time.Time](now)
	check("As[time.Time]", tm, err, now, false)
	_, err = dql.As[time.Time]("now")
	check("As[time.Time](text)", nil, err, nil, true)
	_, err = dql.Enum(status.Valid)(int64(1))
	check("Enum(int)", nil, err, nil, true)
	// An element that does not convert fails as itself.
	_, err = dql.Array(dql.Elem(dql.String))([]any{"a", int64(1)})
	if r, f, _ := reason(t, err); r != dql.OffType || f != "[1]" {
		t.Errorf("Array: %v", err)
	}
	if _, err := dql.Array(dql.Elem(dql.String))([]any{nil}); err == nil || err.(*dql.DecodeError).Reason != dql.NullElementIntoNonNull {
		t.Errorf("NULL element: %v", err)
	}
	if _, err := dql.Array(dql.NullableElem(dql.String))([]any{[]any{}}); err == nil || err.(*dql.DecodeError).Reason != dql.DimensionMismatch {
		t.Errorf("nested element: %v", err)
	}
	if _, err := dql.Array(dql.Elem(dql.String))([]any{[]any{}}); err == nil || err.(*dql.DecodeError).Reason != dql.DimensionMismatch {
		t.Errorf("nested non-null element: %v", err)
	}
	if v, err := dql.Array(dql.Elem(dql.String))([]any{"a"}); err != nil || v[0] != "a" {
		t.Errorf("non-null elements: %v, %v", v, err)
	}
}

// §8.10, §13: an off-type value in a weak column is its raw side, not an
// error.
func TestWeak(t *testing.T) {
	conv := dql.Weak(dql.Int[int64])
	for _, c := range []struct {
		v   any
		raw storage.Value
	}{
		{storage.Integer(3), nil},
		{storage.Real(1.5), storage.Real(1.5)},
		{storage.Text("3"), storage.Text("3")},
		{storage.Blob("x"), storage.Blob("x")},
	} {
		w, err := conv(c.v)
		if err != nil || w.Raw() != nil && c.raw == nil || c.raw != nil && w.String() != weak.Raw[int64](c.raw).String() {
			t.Errorf("Weak(%v) = %v, %v", c.v, w, err)
		}
	}
	if n, ok := dql.Must(conv(storage.Integer(3))).Get(); !ok || n != 3 {
		t.Errorf("typed weak value %d, %t", n, ok)
	}
	if _, err := conv(int64(3)); err == nil {
		t.Errorf("Weak took a value that is not a storage.Value")
	}
	if _, err := dql.Weak(dql.Float[float64])(storage.Real(1.5)); err != nil {
		t.Errorf("Weak[float64]: %v", err)
	}
	if w, _ := dql.Weak(dql.String)(storage.Text("a")); w.Raw() != nil {
		t.Errorf("Weak[string] of text is raw")
	}
	if w, _ := dql.Weak(dql.Bytes)(storage.Blob("a")); w.Raw() != nil {
		t.Errorf("Weak[[]byte] of a blob is raw")
	}
}

func TestDecoderErr(t *testing.T) {
	if err := dql.NewDecoder("s", dql.Row{}, 0).Err(); err != nil {
		t.Errorf("Err = %v", err)
	}
	if err := dql.NewDecoder("s", "x", 0).Err(); err == nil {
		t.Errorf("a value that is not a Row decoded")
	}
}
