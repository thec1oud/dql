package introspect

// Snapshot is everything pg-live reads from one database: the raw material of
// the catalog IR, the extensions of §6.2 and the comments that carry
// annotations (§7.1). It holds names and text as the server reports them and
// no OIDs, timestamps or server versions, so the same schema always yields
// the same Snapshot (§3.2 rule 3).
//
// Columns, composite attributes, routine arguments and constraint, index and
// statistics column lists keep database order (§3.2 rule 1). Every other list
// is sorted by stored name, compared bytewise: objects by schema then name,
// overloads by their identity arguments, grants by grantee, privilege and
// grantor (§3.2 rule 2).
//
// Text fields are empty where the catalog holds NULL. Grants are the explicit
// ACL items only: an object whose ACL is NULL has its owner's default
// privileges and no Grants.
type Snapshot struct {
	Database   Database    `json:"database"`
	Extensions []Extension `json:"extensions,omitempty"`
	Schemas    []Schema    `json:"schemas,omitempty"`
	Collations []Collation `json:"collations,omitempty"`
	Relations  []Relation  `json:"relations,omitempty"`
	Enums      []Enum      `json:"enums,omitempty"`
	Domains    []Domain    `json:"domains,omitempty"`
	Composites []Composite `json:"composites,omitempty"`
	Ranges     []Range     `json:"ranges,omitempty"`
	// BaseTypes are the other non-array types in the schemas, such as those an
	// extension installs; the IR treats them as opaque (§8.3).
	BaseTypes  []BaseType   `json:"baseTypes,omitempty"`
	Sequences  []Sequence   `json:"sequences,omitempty"`
	Routines   []Routine    `json:"routines,omitempty"`
	Statistics []Statistics `json:"statistics,omitempty"`
}

// Database is the connected database's encoding and default collation, which
// is what a column's "default" collation means (§8.9). Its name is left out:
// the catalog name comes from config.
type Database struct {
	Encoding string `json:"encoding"`
	// LocaleProvider is datlocprovider: c (libc) or i (ICU).
	LocaleProvider string `json:"localeProvider"`
	Collate        string `json:"collate,omitempty"`
	Ctype          string `json:"ctype,omitempty"`
	ICULocale      string `json:"icuLocale,omitempty"`
	ICURules       string `json:"icuRules,omitempty"`
}

// Extension is an installed extension. Every installed extension is listed,
// whichever schema it lives in, because types it installs elsewhere may
// appear in the schemas read.
type Extension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Schema  string `json:"schema"`
}

// Schema is one namespace that was asked for and exists.
type Schema struct {
	Name    string  `json:"name"`
	Owner   string  `json:"owner"`
	Comment string  `json:"comment,omitempty"`
	Grants  []Grant `json:"grants,omitempty"`
}

// ObjectName names an object in a schema.
type ObjectName struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

// Grant is one privilege of one ACL item. Grantee is PUBLIC for the public
// pseudo-role.
type Grant struct {
	Grantee   string `json:"grantee"`
	Privilege string `json:"privilege"`
	Grantor   string `json:"grantor"`
	Grantable bool   `json:"grantable,omitempty"`
}

// TypeRef is a reference to a pg_type as used by a column, attribute,
// argument, result or domain.
type TypeRef struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	// Kind is typtype: b base, c composite, d domain, e enum, p pseudo,
	// r range, m multirange.
	Kind string `json:"kind"`
	// Category is typcategory, such as A for arrays and S for strings.
	Category string `json:"category"`
	// Typmod is the type modifier, -1 when there is none.
	Typmod int32 `json:"typmod"`
	// Formatted is format_type's spelling, the native type's raw text (§8.2).
	Formatted string `json:"formatted"`
	// Element is the element type of an array type (§8.6).
	Element *ElementRef `json:"element,omitempty"`
	// Dimensions is the declared number of array dimensions; Postgres does
	// not enforce it.
	Dimensions int16 `json:"dimensions,omitempty"`
}

// ElementRef is the element type of an array.
type ElementRef struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
}

// CollationRef is a collation as used by a column, attribute or domain, with
// the facts §8.9 derives comparison behavior from.
type CollationRef struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	// Provider is collprovider: d (the database default), c (libc) or i (ICU).
	Provider      string `json:"provider"`
	Deterministic bool   `json:"deterministic"`
	Collate       string `json:"collate,omitempty"`
	Ctype         string `json:"ctype,omitempty"`
	ICULocale     string `json:"icuLocale,omitempty"`
	ICURules      string `json:"icuRules,omitempty"`
}

// Collation is a collation defined in one of the schemas read.
type Collation struct {
	CollationRef
	Owner   string `json:"owner"`
	Comment string `json:"comment,omitempty"`
}

// Relation is a table, view, materialized view or partitioned table.
// Partition children are relations too, with PartitionOf set.
type Relation struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	// Kind is relkind: r table, p partitioned table, v view, m materialized view.
	Kind string `json:"kind"`
	// Persistence is relpersistence: p permanent, u unlogged.
	Persistence string `json:"persistence"`
	Owner       string `json:"owner"`
	Comment     string `json:"comment,omitempty"`
	// Tablespace is empty for the database's default tablespace.
	Tablespace string `json:"tablespace,omitempty"`
	// Options are reloptions, as name=value.
	Options []string `json:"options,omitempty"`
	// PartitionKey is a partitioned table's pg_get_partkeydef.
	PartitionKey string `json:"partitionKey,omitempty"`
	// PartitionOf and PartitionBound describe a partition child.
	PartitionOf    *ObjectName `json:"partitionOf,omitempty"`
	PartitionBound string      `json:"partitionBound,omitempty"`
	// Inherits lists plain inheritance parents in inhseqno order.
	Inherits         []ObjectName `json:"inherits,omitempty"`
	RowSecurity      bool         `json:"rowSecurity,omitempty"`
	ForceRowSecurity bool         `json:"forceRowSecurity,omitempty"`
	// Definition is a view's or materialized view's pg_get_viewdef.
	Definition string `json:"definition,omitempty"`
	// DependsOn lists the relations a view's rewrite rule reads (§6.4).
	DependsOn []ObjectName `json:"dependsOn,omitempty"`
	// Extension names the extension the relation belongs to, if any.
	Extension   string       `json:"extension,omitempty"`
	Columns     []Column     `json:"columns,omitempty"`
	Constraints []Constraint `json:"constraints,omitempty"`
	Indexes     []Index      `json:"indexes,omitempty"`
	Triggers    []Trigger    `json:"triggers,omitempty"`
	Policies    []Policy     `json:"policies,omitempty"`
	Grants      []Grant      `json:"grants,omitempty"`
}

// Column is a user column, in attnum order.
type Column struct {
	Name string `json:"name"`
	// Ordinal is attnum. It has gaps where columns were dropped.
	Ordinal int16   `json:"ordinal"`
	Type    TypeRef `json:"type"`
	NotNull bool    `json:"notNull,omitempty"`
	// Default is the default expression's text. A generated column's
	// expression is in Generation instead.
	Default string `json:"default,omitempty"`
	// Identity is attidentity: a (always) or d (by default) (§11.3).
	Identity string `json:"identity,omitempty"`
	// IdentitySequence is the sequence behind an identity column; it lives
	// in the table's schema.
	IdentitySequence string `json:"identitySequence,omitempty"`
	// Generated is attgenerated: s (stored), or v (virtual) where the server
	// has it, with the expression's text in Generation.
	Generated  string        `json:"generated,omitempty"`
	Generation string        `json:"generation,omitempty"`
	Collation  *CollationRef `json:"collation,omitempty"`
	Comment    string        `json:"comment,omitempty"`
	Grants     []Grant       `json:"grants,omitempty"`
}

// Constraint is a table constraint.
type Constraint struct {
	Name string `json:"name"`
	// Kind is contype: c check, f foreign key, n not-null, p primary key,
	// u unique, t constraint trigger, x exclusion.
	Kind string `json:"kind"`
	// Columns is conkey in database order.
	Columns []string `json:"columns,omitempty"`
	// Definition is pg_get_constraintdef.
	Definition string `json:"definition"`
	// References is the target of a foreign key.
	References        *ForeignKey `json:"references,omitempty"`
	Deferrable        bool        `json:"deferrable,omitempty"`
	InitiallyDeferred bool        `json:"initiallyDeferred,omitempty"`
	NotValid          bool        `json:"notValid,omitempty"`
	NoInherit         bool        `json:"noInherit,omitempty"`
	// Index is the index behind a primary key, unique or exclusion constraint.
	Index string `json:"index,omitempty"`
	// NullsNotDistinct is set on a primary key or unique constraint whose
	// index treats NULLs as equal (§11.1).
	NullsNotDistinct bool `json:"nullsNotDistinct,omitempty"`
	// ExclusionOperators are an exclusion constraint's operators, one per
	// column, in column order.
	ExclusionOperators []string `json:"exclusionOperators,omitempty"`
	Comment            string   `json:"comment,omitempty"`
}

// ForeignKey is where a foreign key points and how it behaves.
type ForeignKey struct {
	Schema  string   `json:"schema"`
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
	// Match is confmatchtype: s (simple), f (full) or p (partial) (§12.3).
	Match string `json:"match"`
	// OnUpdate and OnDelete are confupdtype and confdeltype: a (no action),
	// r (restrict), c (cascade), n (set null) or d (set default).
	OnUpdate string `json:"onUpdate"`
	OnDelete string `json:"onDelete"`
	// OnDeleteColumns limits ON DELETE SET NULL or SET DEFAULT to columns.
	OnDeleteColumns []string `json:"onDeleteColumns,omitempty"`
}

// Index is an index on a relation, unique or not (§6.1, §6.2, §11.2).
type Index struct {
	Name    string `json:"name"`
	Method  string `json:"method"`
	Unique  bool   `json:"unique,omitempty"`
	Primary bool   `json:"primary,omitempty"`
	// Keys are the index's key and INCLUDE columns, in index order.
	Keys []IndexKey `json:"keys"`
	// Predicate is a partial index's WHERE clause.
	Predicate        string `json:"predicate,omitempty"`
	NullsNotDistinct bool   `json:"nullsNotDistinct,omitempty"`
	// Constraint names the constraint the index backs, if any.
	Constraint string `json:"constraint,omitempty"`
	// Definition is pg_get_indexdef.
	Definition string   `json:"definition"`
	Options    []string `json:"options,omitempty"`
	Tablespace string   `json:"tablespace,omitempty"`
	Comment    string   `json:"comment,omitempty"`
}

// IndexKey is one index column: a table column or an expression.
type IndexKey struct {
	Column     string `json:"column,omitempty"`
	Expression string `json:"expression,omitempty"`
	// Included is set on INCLUDE columns, which are not part of the key.
	Included bool `json:"included,omitempty"`
}

// Trigger is a user trigger; internal triggers such as those behind foreign
// keys are left out.
type Trigger struct {
	Name string `json:"name"`
	// Timing is BEFORE, AFTER or INSTEAD OF.
	Timing string `json:"timing"`
	// Events are INSERT, UPDATE, DELETE and TRUNCATE, sorted.
	Events []string `json:"events"`
	// Level is ROW or STATEMENT.
	Level    string     `json:"level"`
	Function ObjectName `json:"function"`
	// Enabled is tgenabled: O (origin and local), D (disabled), R (replica),
	// A (always).
	Enabled    string `json:"enabled"`
	Definition string `json:"definition"`
	Comment    string `json:"comment,omitempty"`
}

// Policy is a row-level security policy.
type Policy struct {
	Name string `json:"name"`
	// Command is polcmd: r select, a insert, w update, d delete, * all.
	Command    string `json:"command"`
	Permissive bool   `json:"permissive"`
	// Roles are sorted; PUBLIC stands for every role.
	Roles     []string `json:"roles"`
	Using     string   `json:"using,omitempty"`
	WithCheck string   `json:"withCheck,omitempty"`
	Comment   string   `json:"comment,omitempty"`
}

// Enum is an enum type with its labels in enumsortorder (§8.4).
type Enum struct {
	Schema    string   `json:"schema"`
	Name      string   `json:"name"`
	Owner     string   `json:"owner"`
	Comment   string   `json:"comment,omitempty"`
	Extension string   `json:"extension,omitempty"`
	Labels    []string `json:"labels"`
	Grants    []Grant  `json:"grants,omitempty"`
}

// Domain is a domain type (§8.5).
type Domain struct {
	Schema    string        `json:"schema"`
	Name      string        `json:"name"`
	Owner     string        `json:"owner"`
	Comment   string        `json:"comment,omitempty"`
	Extension string        `json:"extension,omitempty"`
	Base      TypeRef       `json:"base"`
	NotNull   bool          `json:"notNull,omitempty"`
	Default   string        `json:"default,omitempty"`
	Collation *CollationRef `json:"collation,omitempty"`
	Checks    []DomainCheck `json:"checks,omitempty"`
	Grants    []Grant       `json:"grants,omitempty"`
}

// DomainCheck is a domain's check constraint.
type DomainCheck struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
	NotValid   bool   `json:"notValid,omitempty"`
	Comment    string `json:"comment,omitempty"`
}

// Composite is a composite type with its attributes in order (§8.5).
type Composite struct {
	Schema     string      `json:"schema"`
	Name       string      `json:"name"`
	Owner      string      `json:"owner"`
	Comment    string      `json:"comment,omitempty"`
	Extension  string      `json:"extension,omitempty"`
	Attributes []Attribute `json:"attributes"`
	Grants     []Grant     `json:"grants,omitempty"`
}

// Attribute is one attribute of a composite type.
type Attribute struct {
	Name      string        `json:"name"`
	Ordinal   int16         `json:"ordinal"`
	Type      TypeRef       `json:"type"`
	Collation *CollationRef `json:"collation,omitempty"`
	Comment   string        `json:"comment,omitempty"`
}

// Range is a range type and the multirange type Postgres creates with it.
type Range struct {
	Schema     string        `json:"schema"`
	Name       string        `json:"name"`
	Owner      string        `json:"owner"`
	Comment    string        `json:"comment,omitempty"`
	Extension  string        `json:"extension,omitempty"`
	Subtype    TypeRef       `json:"subtype"`
	Multirange ObjectName    `json:"multirange"`
	Collation  *CollationRef `json:"collation,omitempty"`
	// Canonical and SubtypeDiff name the support functions, if any.
	Canonical   string  `json:"canonical,omitempty"`
	SubtypeDiff string  `json:"subtypeDiff,omitempty"`
	Grants      []Grant `json:"grants,omitempty"`
}

// BaseType is any other named type: a base or pseudo type.
type BaseType struct {
	Schema    string  `json:"schema"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Category  string  `json:"category"`
	Owner     string  `json:"owner"`
	Comment   string  `json:"comment,omitempty"`
	Extension string  `json:"extension,omitempty"`
	Grants    []Grant `json:"grants,omitempty"`
}

// Sequence is a sequence, standalone, owned by a column, or behind an
// identity column.
type Sequence struct {
	Schema    string  `json:"schema"`
	Name      string  `json:"name"`
	Owner     string  `json:"owner"`
	Comment   string  `json:"comment,omitempty"`
	DataType  TypeRef `json:"dataType"`
	Start     int64   `json:"start"`
	Increment int64   `json:"increment"`
	Min       int64   `json:"min"`
	Max       int64   `json:"max"`
	Cache     int64   `json:"cache"`
	Cycle     bool    `json:"cycle,omitempty"`
	// OwnedBy is the column the sequence belongs to (OWNED BY, serial or
	// identity), and Identity is set when it is an identity sequence.
	OwnedBy  *ColumnName `json:"ownedBy,omitempty"`
	Identity bool        `json:"identity,omitempty"`
	Grants   []Grant     `json:"grants,omitempty"`
}

// ColumnName names a column of a relation.
type ColumnName struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	Column string `json:"column"`
}

// Routine is one overload of a function, procedure, aggregate or window
// function (§4.6, §6.5).
type Routine struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	// Kind is prokind: f function, p procedure, a aggregate, w window.
	Kind string `json:"kind"`
	// IdentityArguments is pg_get_function_identity_arguments, which tells
	// overloads apart.
	IdentityArguments string `json:"identityArguments"`
	Owner             string `json:"owner"`
	// Comment holds the @public marker (§6.5).
	Comment   string     `json:"comment,omitempty"`
	Extension string     `json:"extension,omitempty"`
	Language  string     `json:"language"`
	Arguments []Argument `json:"arguments,omitempty"`
	// DefaultCount is pronargdefaults: the last DefaultCount input arguments
	// have defaults.
	DefaultCount int16 `json:"defaultCount,omitempty"`
	// Returns is the result type; Result is pg_get_function_result's text.
	Returns    *TypeRef `json:"returns,omitempty"`
	Result     string   `json:"result,omitempty"`
	ReturnsSet bool     `json:"returnsSet,omitempty"`
	Strict     bool     `json:"strict,omitempty"`
	// Volatility is provolatile: i immutable, s stable, v volatile.
	Volatility string `json:"volatility"`
	// Parallel is proparallel: s safe, r restricted, u unsafe.
	Parallel        string `json:"parallel"`
	SecurityDefiner bool   `json:"securityDefiner,omitempty"`
	Leakproof       bool   `json:"leakproof,omitempty"`
	// Source is prosrc; SQLBody is a BEGIN ATOMIC body.
	Source  string  `json:"source,omitempty"`
	SQLBody string  `json:"sqlBody,omitempty"`
	Grants  []Grant `json:"grants,omitempty"`
}

// Argument is one routine argument, in declaration order. RETURNS TABLE
// columns are arguments with mode t.
type Argument struct {
	Name string `json:"name,omitempty"`
	// Mode is i (in), o (out), b (inout), v (variadic) or t (table column).
	Mode       string  `json:"mode"`
	Type       TypeRef `json:"type"`
	HasDefault bool    `json:"hasDefault,omitempty"`
	Default    string  `json:"default,omitempty"`
}

// Statistics is an extended statistics object.
type Statistics struct {
	Schema  string     `json:"schema"`
	Name    string     `json:"name"`
	Owner   string     `json:"owner"`
	Comment string     `json:"comment,omitempty"`
	Table   ObjectName `json:"table"`
	// Kinds are stxkind letters, sorted: d ndistinct, e expressions,
	// f dependencies, m mcv.
	Kinds       []string `json:"kinds"`
	Columns     []string `json:"columns,omitempty"`
	Expressions []string `json:"expressions,omitempty"`
	Definition  string   `json:"definition"`
}
