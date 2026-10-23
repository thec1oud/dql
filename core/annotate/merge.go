package annotate

import (
	"cmp"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/thec1oud/dql/core/diagnostics"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// rank orders origins by precedence: config, then comment, then default
// (§7.1). A default is an inferred annotation. An annotation without an
// origin ranks below all of them.
func rank(a *irv1.Annotation) int {
	switch a.GetOrigin().GetKind() {
	case irv1.OriginKind_ORIGIN_CONFIG:
		return 3
	case irv1.OriginKind_ORIGIN_DB_COMMENT:
		return 2
	case irv1.OriginKind_ORIGIN_INFERRED:
		return 1
	}
	return 0
}

func byPrecedence(a, b *irv1.Annotation) int { return cmp.Compare(rank(b), rank(a)) }

// Merge turns every annotation read for one target, from config, comments
// and defaults, into its effective annotations (§7.1, trace §7.6).
//
// For each key the highest-precedence annotation wins. The others are kept
// in its superseded list, highest precedence first, so every generated
// behavior can be traced to its cause. Origins are kept as they are. The
// result is sorted by key (§3.2, rule 2), and the inputs are not modified.
//
// §7.1 does not rank two annotations from one source: when one source gives
// a key twice, the first one read wins, with a warning when the values agree
// and an error when they differ.
func Merge(target *irv1.QualifiedName, anns []*irv1.Annotation) ([]*irv1.Annotation, []*irv1.Diagnostic) {
	var ds []*irv1.Diagnostic
	groups := map[string][]*irv1.Annotation{}
	var keys []string
	for _, a := range anns {
		a = proto.CloneOf(a)
		if rank(a) == 0 {
			ds = append(ds, diagnostics.Errorf(CodeOriginMissing, target,
				"annotation %s on %s does not say where it was read; the source plugin must set its origin (§7.1)",
				a.GetKey(), diagnostics.Name(target)))
		}
		if _, seen := groups[a.GetKey()]; !seen {
			keys = append(keys, a.GetKey())
		}
		groups[a.GetKey()] = append(groups[a.GetKey()], a)
	}
	slices.SortFunc(keys, strings.Compare)

	out := make([]*irv1.Annotation, 0, len(keys))
	for _, k := range keys {
		g := groups[k]
		slices.SortStableFunc(g, byPrecedence)
		win := g[0]
		for _, lost := range g[1:] {
			if rank(lost) != rank(win) {
				continue
			}
			if proto.Equal(lost.GetValue(), win.GetValue()) {
				ds = append(ds, diagnostics.Warningf(CodeDuplicate, target,
					"%s is given twice on %s, at %s and %s; remove one of them",
					k, diagnostics.Name(target), win.GetOrigin().GetLocation(), lost.GetOrigin().GetLocation()))
			} else {
				ds = append(ds, diagnostics.Errorf(CodeConflict, target,
					"%s is given twice on %s with different values, at %s and %s; keep only the one you mean",
					k, diagnostics.Name(target), win.GetOrigin().GetLocation(), lost.GetOrigin().GetLocation()))
			}
		}
		win.Superseded = append(win.Superseded, g[1:]...)
		slices.SortStableFunc(win.Superseded, byPrecedence)
		out = append(out, win)
	}
	diagnostics.Sort(ds)
	return out, ds
}
