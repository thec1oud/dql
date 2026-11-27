package bundle

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/determinism"
	"github.com/thec1oud/dql/testkit/golden"
)

func emptyCatalogBundle() *irv1.Bundle {
	return &irv1.Bundle{
		Version: Version(),
		Dialect: irv1.Dialect_DIALECT_POSTGRES,
		Catalogs: []*irv1.Catalog{{
			Name: &irv1.Identifier{Stored: "shop", Words: []string{"shop"}},
			Provenance: &irv1.Provenance{
				Source:           "pg-live",
				SourceVersion:    "1.0.0",
				InputFingerprint: "sha256:0000",
				Fidelity:         irv1.SourceFidelity_SOURCE_FIDELITY_FULL,
			},
		}},
	}
}

// M0 gate: an empty catalog round-trips through binary and JSON byte-identically.
func TestEmptyCatalogRoundTrip(t *testing.T) {
	b := emptyCatalogBundle()

	bin, err := MarshalBinary(b)
	if err != nil {
		t.Fatal(err)
	}
	js, err := MarshalJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("testdata", "empty-catalog.json"), js)

	fromBin := &irv1.Bundle{}
	if err := UnmarshalBinary(bin, fromBin); err != nil {
		t.Fatal(err)
	}
	fromJSON := &irv1.Bundle{}
	if err := UnmarshalJSON(js, fromJSON); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(fromBin, b) || !proto.Equal(fromJSON, b) {
		t.Fatal("decoded bundle differs from the original")
	}

	bin2, _ := MarshalBinary(fromJSON)
	js2, _ := MarshalJSON(fromBin)
	if !bytes.Equal(bin, bin2) {
		t.Error("binary differs after a JSON round trip")
	}
	if !bytes.Equal(js, js2) {
		t.Error("JSON differs after a binary round trip")
	}
}

func TestEncodingIsDeterministic(t *testing.T) {
	for name, enc := range map[string]func(proto.Message) ([]byte, error){
		"binary": MarshalBinary,
		"json":   MarshalJSON,
	} {
		err := determinism.Repeat(20, func() ([]byte, error) { return enc(emptyCatalogBundle()) })
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMarshalJSONRejectsInvalidUTF8(t *testing.T) {
	b := &irv1.Bundle{RequiredFeatures: []string{"\xff"}}
	if _, err := MarshalJSON(b); err == nil {
		t.Fatal("expected an error for invalid UTF-8")
	}
}

func TestMarshalJSONReportsIndentFailure(t *testing.T) {
	defer func(f func(*bytes.Buffer, []byte, string, string) error) { indent = f }(indent)
	indent = func(*bytes.Buffer, []byte, string, string) error { return io.ErrUnexpectedEOF }
	if _, err := MarshalJSON(emptyCatalogBundle()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v", err)
	}
}

func TestUnmarshalJSONRejectsUnknownFields(t *testing.T) {
	if err := UnmarshalJSON([]byte(`{"nope": 1}`), &irv1.Bundle{}); err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}
