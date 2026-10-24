package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thec1oud/dql/core/bundle"
	"github.com/thec1oud/dql/core/shapes"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
	"github.com/thec1oud/dql/testkit/determinism"
)

// recordShapes are the custom shapes of §14.8, as dql.yaml declares them.
const recordShapes = `shapes:
  user_summary:
    from: user.full
    pick: [id, email, display_name]
  user_summary_named:
    from: user_summary
    nonnull: [display_name]
  post_with_tags:
    from: post.full
    nest:
      tags: tag.full
      author: user_summary
  signup:
    from: user.draft
    pick: [email, password_hash]
  quick_invite:
    from: user.draft
    pick: [email]
    bind: [password_hash]
`

// shopConfig is the running example's dql.yaml with extra appended.
func shopConfig(t *testing.T, extra string) string {
	t.Helper()
	t.Setenv("SHOP_DATABASE_URL", shopURL)
	cfg, err := os.ReadFile(filepath.Join(shopDir, "dql.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return write(t, "dql.yaml", string(cfg)+extra)
}

// generateShapes runs dql generate on the running example with extra
// appended to its dql.yaml, and returns the exit code, stderr and the bundle
// it wrote, if any.
func generateShapes(t *testing.T, extra string) (int, string, *irv1.Bundle) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "bundle.json")
	code, _, stderr := dql("generate", "-config", shopConfig(t, extra), "-bundle", out)
	raw, err := os.ReadFile(out)
	if err != nil {
		return code, stderr, nil
	}
	b := &irv1.Bundle{}
	if err := bundle.UnmarshalJSON(raw, b); err != nil {
		t.Fatal(err)
	}
	return code, stderr, b
}

// spell gives the dotted spelling of a shape id of the running example.
func spell(b *irv1.Bundle, id *irv1.ShapeId) string {
	words := map[string][]string{}
	for _, tb := range b.GetCatalogs()[0].GetNamespaces()[0].GetTables() {
		words[tb.GetName().GetStored()] = tb.GetHostName().GetWords()
	}
	return shapes.Spelling(id, words[id.GetEntity().GetObject()])
}

// dql generate builds the shape layer from the catalogs and the shapes: and
// conversions: of dql.yaml (§14), and the bundle carries it.
func TestGenerateShapeLayer(t *testing.T) {
	code, stderr, b := generateShapes(t, recordShapes+`conversions:
  - { from: user.full.nested, to: user_summary }
  - { from: user.full, to: user_summary }
`)
	if code != 0 || !strings.HasPrefix(stderr, "dql generate: wrote the bundle to ") {
		t.Fatalf("%d %q", code, stderr)
	}
	var names []string
	for _, s := range b.GetShapes().GetShapes() {
		names = append(names, spell(b, s.GetId()))
	}
	for _, want := range []string{"user.full.flat", "user.draft.nested", "post.draft.flat.for.author", "user_summary", "quick_invite", "post_with_tags"} {
		if !slices.Contains(names, want) {
			t.Errorf("no shape %s in %v", want, names)
		}
	}
	origins := map[string]irv1.ConversionOrigin{}
	for _, c := range b.GetShapes().GetConversions() {
		origins[spell(b, c.GetFrom())+" "+spell(b, c.GetTo())] = c.GetOrigin()
	}
	for pair, want := range map[string]irv1.ConversionOrigin{
		"user.full.nested user_summary": irv1.ConversionOrigin_CONVERSION_ORIGIN_DECLARED,
		// Declared, but the lineage already holds it.
		"user.full.flat user_summary": irv1.ConversionOrigin_CONVERSION_ORIGIN_LINEAGE,
		"user.full.flat user.key":     irv1.ConversionOrigin_CONVERSION_ORIGIN_PRESET,
	} {
		if got := origins[pair]; got != want {
			t.Errorf("%s: origin %v, want %v", pair, got, want)
		}
	}
	if len(b.GetShapes().GetUpserts()) == 0 || len(b.GetShapes().GetMerges()) == 0 || len(b.GetShapes().GetUnions()) == 0 {
		t.Errorf("upserts, merges or unions missing: %v", b.GetShapes())
	}
}

// Errors of the shape layer are printed and fail generation, and no bundle
// is written: a custom shape that breaks the write rule (§14.8), a field the
// from shape lacks, and declared conversions to an unknown shape or an
// Impossible one (§14.9).
func TestGenerateShapeLayerFailures(t *testing.T) {
	for _, c := range []struct {
		name, extra string
		want        []string
	}{
		{"write rule", "shapes:\n  quick_invite: { from: user.draft, pick: [email] }\n",
			[]string{"error write-shape-incomplete: shape derived from user.draft drops required field password_hash; keep it or bind it\n"}},
		{"unknown field", "shapes:\n  user_summary: { from: user.full, pick: [nope] }\n",
			[]string{"error shape-field-unknown: "}},
		{"conversions", "shapes:\n  user_summary: { from: user.full, pick: [id, email] }\nconversions:\n" +
			"  - { from: user.fulll, to: user_summary }\n  - { from: user_summary, to: user.full }\n",
			[]string{
				"error conversion-impossible: conversion from user_summary to user.full.flat is impossible",
				"error conversion-shape-unknown: conversion from user.fulll to user_summary names shape user.fulll, which does not exist",
			}},
	} {
		code, stderr, b := generateShapes(t, c.extra)
		for _, w := range c.want {
			if code != 1 || b != nil || !strings.Contains(stderr, w) {
				t.Errorf("%s: %d %q; want %q", c.name, code, stderr, w)
			}
		}
	}

	defer func(f func([]*irv1.Catalog, []shapes.Custom) (*irv1.ShapeLayer, []*irv1.Diagnostic, error)) {
		buildShapes = f
	}(buildShapes)
	buildShapes = func([]*irv1.Catalog, []shapes.Custom) (*irv1.ShapeLayer, []*irv1.Diagnostic, error) {
		return nil, nil, fmt.Errorf("boom")
	}
	if code, stderr, b := generateShapes(t, ""); code != 1 || b != nil || stderr != "dql generate: boom\n" {
		t.Errorf("layer fails: %d %q", code, stderr)
	}
}

// dql inspect prints the shape layer with the rest of the bundle, as the
// canonical JSON dql generate wrote.
func TestInspectShapeLayer(t *testing.T) {
	out := filepath.Join(t.TempDir(), "bundle.json")
	if code, _, stderr := dql("generate", "-config", shopConfig(t, recordShapes), "-bundle", out); code != 0 {
		t.Fatalf("%d %q", code, stderr)
	}
	raw, _ := os.ReadFile(out)
	code, stdout, stderr := dql("inspect", out)
	if code != 0 || stdout != string(raw) || !strings.Contains(stdout, `"custom": "quick_invite"`) {
		t.Errorf("%d %q", code, stderr)
	}
}

// TestGenerateDeterministic runs the dql executable on the running example,
// with the custom shapes of §14.8, repeatedly and under each environment of
// guide §6.2, a Turkish locale included, and requires identical bundles.
func TestGenerateDeterministic(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "dql")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	path := shopConfig(t, recordShapes)
	for _, envs := range [][][]string{{determinism.Environments[0], determinism.Environments[0]}, determinism.Environments} {
		if err := determinism.Command(envs, bin, "generate", "-config", path, "-bundle", "/dev/stdout"); err != nil {
			t.Error(err)
		}
	}
}
