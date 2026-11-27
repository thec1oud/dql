package plugin

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// Negotiate decides whether a plugin may serve a bundle. It refuses a plugin
// of the wrong kind, one whose IR version range excludes the bundle's
// version, and one that lacks a feature the bundle requires (§3.3).
func Negotiate(hello *irv1.CoreHello, hs *irv1.PluginHandshake, kind irv1.PluginKind) error {
	p := hs.GetPlugin()
	if p.GetName() == "" {
		return fmt.Errorf("plugin: handshake names no plugin")
	}
	if p.GetKind() != kind {
		return fmt.Errorf("plugin %s is a %s plugin, not %s", p.GetName(), p.GetKind(), kind)
	}
	r := hs.GetIrVersions()
	minimum, err := parseVersion(r.GetMin())
	if err != nil {
		return fmt.Errorf("plugin %s: %w", p.GetName(), err)
	}
	v := hello.GetIrVersion()
	have := [3]uint64{uint64(v.GetMajor()), uint64(v.GetMinor()), uint64(v.GetPatch())}
	if slices.Compare(have[:], minimum[:]) < 0 || have[0] > uint64(r.GetMaxMajor()) {
		return fmt.Errorf("plugin %s reads IR %s up to major %d, and the bundle is IR %d.%d.%d",
			p.GetName(), r.GetMin(), r.GetMaxMajor(), have[0], have[1], have[2])
	}
	var missing []string
	for _, f := range hello.GetRequiredFeatures() {
		if !slices.Contains(hs.GetFeatures(), f) {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("plugin %s lacks required features: %s", p.GetName(), strings.Join(missing, ", "))
	}
	return nil
}

// parseVersion parses a MAJOR.MINOR.PATCH version.
func parseVersion(s string) ([3]uint64, error) {
	var v [3]uint64
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("invalid IR version %q: want MAJOR.MINOR.PATCH", s)
	}
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return v, fmt.Errorf("invalid IR version %q: want MAJOR.MINOR.PATCH", s)
		}
		v[i] = n
	}
	return v, nil
}
