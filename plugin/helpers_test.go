package plugin

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

var echoBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dql-plugin")
	if err != nil {
		panic(err)
	}
	echoBin = filepath.Join(dir, "dql-plugin-echo")
	build := exec.Command("go", "build", "-o", echoBin, "github.com/thec1oud/dql/plugin/cmd/dql-plugin-echo")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func v1() *irv1.IrVersion {
	return &irv1.IrVersion{Major: proto.Uint32(1), Minor: proto.Uint32(0), Patch: proto.Uint32(0)}
}

func hello(features ...string) *irv1.CoreHello {
	return &irv1.CoreHello{IrVersion: v1(), RequiredFeatures: features}
}

// frames encodes messages as one framed stream.
func frames(t *testing.T, msgs ...proto.Message) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, m := range msgs {
		if err := WriteMessage(&buf, m); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

var errWrite = errors.New("write failed")

// failAfter accepts n writes, then fails.
type failAfter struct {
	n int
	bytes.Buffer
}

func (w *failAfter) Write(b []byte) (int, error) {
	if w.n == 0 {
		return 0, errWrite
	}
	w.n--
	return w.Buffer.Write(b)
}

func (w *failAfter) Close() error { return nil }

var _ io.WriteCloser = (*failAfter)(nil)
