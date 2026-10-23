package introspect

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dql/testkit/determinism"
	"github.com/thec1oud/dql/testkit/golden"
)

// marshal writes s as indented JSON, leaving <, > and & unescaped so
// definitions stay readable in the golden file.
func marshal(s *Snapshot) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(s)
	return buf.Bytes(), err
}

func read(t *testing.T, conn Conn, namespaces ...string) *Snapshot {
	t.Helper()
	s, err := Read(context.Background(), conn, namespaces)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The shop schema of design record §1.3, pinned as a golden file. It holds no
// OIDs, timestamps or server versions (§3.2 rule 3).
func TestShopGolden(t *testing.T) {
	got, err := marshal(read(t, shopDB, "app"))
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, filepath.Join("testdata", "shop.json"), got)
}

func TestShopIsDeterministic(t *testing.T) {
	err := determinism.Repeat(3, func() ([]byte, error) {
		s, err := Read(context.Background(), shopDB, []string{"app"})
		if err != nil {
			return nil, err
		}
		return marshal(s)
	})
	if err != nil {
		t.Fatal(err)
	}
}
