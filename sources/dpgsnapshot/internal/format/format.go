// Package format reads dpg's committed snapshot files: one JSON file per
// database, written by dpg after each apply (dpg RFC §16). It decodes the
// file into Go structs that mirror dpg's own, and does nothing else: turning
// them into the catalog IR is the plugin's job.
//
// dpg itself knows nothing about this package. Only the snapshot format is
// read, and its version is checked before anything else (design record §2.4).
package format

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

// Supported names the snapshot format versions this package reads: dpg's
// current format, 0.2, at any patch level.
const Supported = "0.2.x"

// supported matches the versions Supported names. A pre-release or build
// suffix is a different format as far as this package knows.
var supported = regexp.MustCompile(`^0\.2\.(0|[1-9][0-9]*)$`)

// Snapshot is one decoded snapshot file.
type Snapshot struct {
	// Version is the file's dpg_version, the snapshot format version.
	Version  string
	Cluster  string
	Database string // empty for a cluster-level snapshot
	// SourceRevision is the git commit dpg recorded, or empty.
	SourceRevision string
	// Migration is the last migration of dpg's migration log, or empty.
	Migration string
	// Objects holds every entry of the file's objects map, sorted by key so
	// nothing downstream depends on map order (§3.2 rule 4).
	Objects []Entry
	// Fingerprint identifies the file's exact bytes (see Fingerprint).
	Fingerprint string
}

// Entry is one object of a snapshot, under the key dpg stored it by: the
// object's qualified name, with the argument types for a function.
type Entry struct {
	Key    string
	Object *Object
}

// file is the top-level JSON object exactly as dpg writes it.
type file struct {
	DPGVersion string `json:"dpg_version"`
	Cluster    string `json:"cluster"`
	Database   string `json:"database"`
	// applied_at is when dpg last applied the snapshot. It is accepted and
	// never stored, so no timestamp can reach the IR (§3.2 rule 3).
	AppliedAt      discard            `json:"applied_at"`
	SourceRevision string             `json:"source_revision"`
	Migration      string             `json:"migration"`
	Objects        map[string]*Object `json:"objects"`
}

// discard accepts any JSON value and keeps nothing of it.
type discard struct{}

func (*discard) UnmarshalJSON([]byte) error { return nil }

// VersionError reports a snapshot whose format version is not Supported.
type VersionError struct {
	Found   string // the dpg_version found
	Missing bool   // the file has no dpg_version at all
}

func (e *VersionError) Error() string {
	if e.Missing {
		return "dpg snapshot has no dpg_version; supported snapshot format versions: " + Supported
	}
	return fmt.Sprintf("dpg snapshot format version %q is not supported; supported snapshot format versions: %s", e.Found, Supported)
}

// Load reads and decodes the snapshot file at path.
func Load(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("dpg snapshot: %w", err)
	}
	s, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Parse decodes one snapshot file's content.
//
// The format version is checked first, so a snapshot from another dpg
// format is refused by its version rather than by whatever field it happens
// to add (§2.4). The content is then decoded strictly: a field or object
// kind this package does not know is an error, never silently dropped
// (§6.3).
func Parse(data []byte) (*Snapshot, error) {
	var probe struct {
		DPGVersion *string `json:"dpg_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("dpg snapshot is not a valid snapshot file: %w", err)
	}
	if probe.DPGVersion == nil {
		return nil, &VersionError{Missing: true}
	}
	if !supported.MatchString(*probe.DPGVersion) {
		return nil, &VersionError{Found: *probe.DPGVersion}
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f file
	// The probe accepted the bytes as one JSON value, so only a field or
	// type mismatch can fail here.
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("dpg snapshot %s: %w", *probe.DPGVersion, err)
	}

	s := &Snapshot{
		Version:        f.DPGVersion,
		Cluster:        f.Cluster,
		Database:       f.Database,
		SourceRevision: f.SourceRevision,
		Migration:      f.Migration,
		Fingerprint:    Fingerprint(data),
	}
	keys := make([]string, 0, len(f.Objects))
	for k := range f.Objects {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if err := check(f.Objects[k]); err != nil {
			return nil, fmt.Errorf("dpg snapshot %s: objects[%q]: %w", f.DPGVersion, k, err)
		}
		s.Objects = append(s.Objects, Entry{Key: k, Object: f.Objects[k]})
	}
	return s, nil
}

// opaqueKinds are the kinds dpg stores as Opaque (dpg RFC §16.3.1).
var opaqueKinds = []string{
	"aggregate", "cast", "collation", "event_trigger", "fdw", "operator",
	"operator_class", "operator_family", "procedure", "publication", "server",
	"statistics", "subscription", "tablespace", "ts_config", "ts_dict",
	"ts_parser", "ts_template", "user_mapping",
}

// check verifies that o is one object of a kind this package knows, with
// exactly the payload for that kind.
func check(o *Object) error {
	if o == nil {
		return fmt.Errorf("object is null")
	}
	set := map[string]bool{
		"table":                o.Table != nil,
		"view":                 o.View != nil,
		"function":             o.Function != nil,
		"type":                 o.Type != nil,
		"schema":               o.Schema != nil,
		"extension":            o.Extension != nil,
		"sequence":             o.Sequence != nil,
		"role":                 o.Role != nil,
		"virtual_type":         o.VirtualType != nil,
		"default_privileges":   o.DefaultPrivileges != nil,
		"parameter_privileges": o.ParameterPrivileges != nil,
		"opaque":               o.Opaque != nil,
	}
	field := o.Kind
	if slices.Contains(opaqueKinds, o.Kind) {
		field = "opaque"
	} else if _, ok := set[o.Kind]; !ok || o.Kind == "opaque" {
		return fmt.Errorf("unknown object kind %q", o.Kind)
	}
	var fields []string
	for name, isSet := range set {
		if isSet {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	if len(fields) != 1 || fields[0] != field {
		return fmt.Errorf("kind %q needs exactly the field %q, found [%s]", o.Kind, field, strings.Join(fields, ", "))
	}
	if field == "opaque" && o.Opaque.Kind != o.Kind {
		return fmt.Errorf("kind %q holds an opaque record of kind %q", o.Kind, o.Opaque.Kind)
	}
	return nil
}

// Fingerprint identifies a snapshot file by the SHA-256 of its exact bytes,
// as "sha256:<hex>". It is the input fingerprint of the catalog's provenance
// (§3.1, §3.2 rule 3).
func Fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
