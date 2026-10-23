// Package annotate owns the annotation vocabulary, precedence, provenance and
// annotation checks (design record §7). Source plugins read annotations from
// comments and config; this package merges them into effective annotations
// and checks them against the object they are on.
package annotate

import (
	"slices"
	"strings"
)

// Key prefixes in the IR (§7.2). Core keys are written without their prefix
// in comments and config.
const (
	CorePrefix   = "core."
	PluginPrefix = "x."
)

// Form is the shape of an annotation's value (§7.2, §7.4).
type Form int

const (
	// FormNone takes no value; the IR leaves the value absent.
	FormNone Form = iota + 1
	// FormString is a non-empty string.
	FormString
	// FormWords is a non-empty word list, as core/naming splits it (§4.2).
	FormWords
	// FormList is a non-empty list of distinct non-empty strings.
	FormList
	// FormStruct is a struct with named fields.
	FormStruct
	// FormChoice is a string from a fixed set.
	FormChoice
)

// Kind is the kind of object an annotation is on.
type Kind int

const (
	KindTable Kind = iota + 1
	// KindView is a view or a materialized view (§6.4).
	KindView
	// KindColumn is a table column. A view's columns are KindViewColumn,
	// because §7.2 lists them apart from columns.
	KindColumn
	KindViewColumn
	// KindAttribute is a composite-type attribute (§8.5).
	KindAttribute
	// KindResult is a function's result or one of its result columns (§6.5).
	KindResult
	KindCheck
	KindExclusion
	KindForeignKey
	KindFunction
	KindProcedure
	KindEnum
	KindDomain
	KindComposite
	KindSequence
	KindNamespace
)

var kindNames = map[Kind]string{
	KindTable:      "table",
	KindView:       "view",
	KindColumn:     "column",
	KindViewColumn: "view column",
	KindAttribute:  "composite attribute",
	KindResult:     "function result",
	KindCheck:      "check constraint",
	KindExclusion:  "exclusion constraint",
	KindForeignKey: "foreign key",
	KindFunction:   "function",
	KindProcedure:  "procedure",
	KindEnum:       "enum",
	KindDomain:     "domain",
	KindComposite:  "composite type",
	KindSequence:   "sequence",
	KindNamespace:  "namespace",
}

// String names the kind in diagnostics.
func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "unknown object"
}

// Key is one key of the core vocabulary (§7.2).
type Key struct {
	// Name is the key as written in comments and config, without "core.".
	Name string
	// Applies lists the kinds of object the key may be on. Some keys narrow
	// this further by type or by fact; Target says how.
	Applies []Kind
	// Target is the "Applies to" column of §7.2, with its article, for diagnostics.
	Target string
	Form   Form
	// Choices holds the allowed values of a FormChoice key, in record order.
	Choices []string
	// Fields holds a FormStruct key's allowed fields, sorted.
	Fields []string
	// See is the section that defines the key's effect.
	See string
}

// IR returns the key as the IR writes it.
func (k Key) IR() string { return CorePrefix + k.Name }

// Allows reports whether the key may be on an object of kind kind.
func (k Key) Allows(kind Kind) bool { return slices.Contains(k.Applies, kind) }

var (
	columnOnly = []Kind{KindColumn}
	// Every kind with a name; a function result has none (§7.2 "Any named object").
	named = []Kind{KindTable, KindView, KindColumn, KindViewColumn, KindAttribute, KindCheck,
		KindExclusion, KindForeignKey, KindFunction, KindProcedure, KindEnum, KindDomain,
		KindComposite, KindSequence, KindNamespace}
	foreignKey = []Kind{KindForeignKey}
)

// vocabulary is §7.2, sorted by name.
var vocabulary = []Key{
	{Name: "array-elements-nonnull", Applies: columnOnly, Target: "an array column", Form: FormNone, See: "§8.6"},
	{Name: "as", Applies: columnOnly, Target: "a column", Form: FormChoice, Choices: logicalTypes(), See: "§7.2, §13"},
	{Name: "bool", Applies: columnOnly, Target: "a MySQL TINYINT(1) column", Form: FormNone, See: "§8.7"},
	{Name: "bound", Applies: columnOnly, Target: "a column", Form: FormNone, See: "§14.2"},
	{Name: "collection-policy", Applies: foreignKey, Target: "a foreign key", Form: FormChoice,
		Choices: []string{"merge", "replace", "append"}, See: "§14.6"},
	{Name: "db-filled", Applies: columnOnly, Target: "a column", Form: FormNone, See: "§14.2"},
	{Name: "db-updated", Applies: columnOnly, Target: "a column", Form: FormNone, See: "§14.2"},
	{Name: "enum", Applies: []Kind{KindCheck}, Target: "a single-column membership check constraint",
		Form: FormString, See: "§8.4"},
	{Name: "immutable", Applies: columnOnly, Target: "a column", Form: FormNone, See: "§14.2"},
	{Name: "inverse-name", Applies: foreignKey, Target: "a foreign key", Form: FormWords, See: "§4.5"},
	{Name: "logical-key", Applies: []Kind{KindTable, KindView}, Target: "a table without a primary key, or view",
		Form: FormList, See: "§11.1"},
	{Name: "name", Applies: named, Target: "a named object", Form: FormWords, See: "§4.4"},
	{Name: "nonnull", Applies: []Kind{KindColumn, KindViewColumn, KindAttribute, KindResult},
		Target: "a column, view column, composite attribute or function result", Form: FormNone, See: "§13"},
	{Name: "public", Applies: []Kind{KindFunction, KindProcedure}, Target: "a function or procedure",
		Form: FormNone, See: "§6.5"},
	{Name: "read-only", Applies: columnOnly, Target: "a column", Form: FormNone, See: "§14.2"},
	{Name: "relation", Applies: columnOnly, Target: "a column", Form: FormStruct,
		Fields: []string{"inverse-name", "name", "to"}, See: "§12.1, §5.3"},
	{Name: "relation-name", Applies: foreignKey, Target: "a foreign key", Form: FormWords, See: "§4.5"},
	{Name: "removal", Applies: foreignKey, Target: "a foreign key", Form: FormChoice,
		Choices: []string{"delete", "unlink"}, See: "§14.6"},
	{Name: "sensitive", Applies: columnOnly, Target: "a column", Form: FormNone, See: "§14.2"},
	{Name: "soft-delete", Applies: columnOnly, Target: "a nullable timestamp or boolean column", Form: FormNone, See: "§21.3"},
	{Name: "version", Applies: columnOnly, Target: "an integer column", Form: FormNone, See: "§14.2"},
	{Name: "write-only", Applies: columnOnly, Target: "a column", Form: FormNone, See: "§14.2"},
}

// Vocabulary returns the core vocabulary, sorted by name.
func Vocabulary() []Key { return slices.Clone(vocabulary) }

// Lookup finds a core key by its IR spelling, such as "core.write-only".
func Lookup(ir string) (Key, bool) {
	name, ok := strings.CutPrefix(ir, CorePrefix)
	if !ok {
		return Key{}, false
	}
	i, found := slices.BinarySearchFunc(vocabulary, name, func(k Key, n string) int {
		return strings.Compare(k.Name, n)
	})
	if !found {
		return Key{}, false
	}
	return vocabulary[i], true
}

// IRKey turns a key as written in a comment or config into its IR spelling:
// plugin keys keep theirs, every other key is a core key (§7.2).
func IRKey(written string) string {
	if strings.HasPrefix(written, PluginPrefix) {
		return written
	}
	return CorePrefix + written
}

// IsPlugin reports whether key is a well-formed plugin key, x.<plugin>.<key>.
func IsPlugin(key string) bool {
	rest, ok := strings.CutPrefix(key, PluginPrefix)
	plugin, name, dotted := strings.Cut(rest, ".")
	return ok && dotted && plugin != "" && name != ""
}
