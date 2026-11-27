package roundtrip

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"strings"

	"github.com/thec1oud/dql/plugin"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/ddl"
)

// invoke runs a source plugin and returns its one catalog.
func invoke(t *testing.T, p plugin.Plugin) *irv1.Catalog {
	t.Helper()
	res, err := p.Invoke(context.Background(), &irv1.Invoke{})
	if err != nil {
		t.Fatal(err)
	}
	return res.GetBundle().GetCatalogs()[0]
}

// roundTrip writes first as DDL, applies it with apply, which reads the
// result, and fails unless the two catalogs are the same.
func roundTrip(t *testing.T, d irv1.Dialect, first *irv1.Catalog, apply func(ddl string) *irv1.Catalog) {
	t.Helper()
	text, err := ddl.Write(d, first)
	if err != nil {
		t.Fatal(err)
	}
	second := apply(text)
	if diff := diff(render(t, first), render(t, second)); diff != "" {
		t.Errorf("the catalog changed in a round trip (-read +written):\n%s\nDDL:\n%s", diff, text)
	}
}

// render prints c for comparison, without the fingerprint of its input.
func render(t *testing.T, c *irv1.Catalog) string {
	t.Helper()
	c = proto.Clone(c).(*irv1.Catalog)
	c.GetProvenance().InputFingerprint = ""
	b, err := protojson.MarshalOptions{Multiline: true}.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// diff lists the lines of a and b that differ, by line number, as - and +.
func diff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out strings.Builder
	for i := range max(len(al), len(bl)) {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			fmt.Fprintf(&out, "%d\n- %s\n+ %s\n", i+1, x, y)
		}
	}
	return out.String()
}

// renamed returns c with every from in its text replaced by to.
func renamed(t *testing.T, c *irv1.Catalog, from, to string) *irv1.Catalog {
	t.Helper()
	b, err := protojson.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	out := &irv1.Catalog{}
	if err := protojson.Unmarshal([]byte(strings.ReplaceAll(string(b), from, to)), out); err != nil {
		t.Fatal(err)
	}
	return out
}
