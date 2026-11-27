package introspect

// Snapshot is everything sqlserver-live reads from one database: the raw
// material of the catalog IR, the extensions of §6.2 and the
// MS_Description extended properties that carry annotations (§7.1). It
// holds names and text as the server reports them and no statistics,
// identity counters or object IDs, so the same schema always yields the
// same Snapshot (§3.2 rule 3).
//
// Columns, parameters and index key parts keep database order (§3.2 rule
// 1). Every other list is sorted by name, compared bytewise.
type Snapshot struct {
	Database Database `json:"database"`
	Schemas  []Schema `json:"schemas,omitempty"`
}

// Database is the server's version and the database's settings. Its name is
// left out: the catalog name comes from config.
type Database struct {
	Version               string `json:"version"`
	Collation             string `json:"collation"`
	CompatibilityLevel    uint32 `json:"compatibilityLevel"`
	ReadCommittedSnapshot bool   `json:"readCommittedSnapshot,omitempty"`
	SnapshotIsolation     bool   `json:"snapshotIsolation,omitempty"`
}

// Schema is a schema users made, or dbo. Its objects are user objects only.
type Schema struct {
	Name     string    `json:"name"`
	Comment  string    `json:"comment,omitempty"`
	Tables   []Table   `json:"tables,omitempty"`
	Routines []Routine `json:"routines,omitempty"`
}

// Table is a base table or a view.
type Table struct {
	Name string `json:"name"`
	// View is set for a view.
	View        bool         `json:"view,omitempty"`
	Comment     string       `json:"comment,omitempty"`
	Columns     []Column     `json:"columns"`
	Checks      []Check      `json:"checks,omitempty"`
	Indexes     []Index      `json:"indexes,omitempty"`
	ForeignKeys []ForeignKey `json:"foreignKeys,omitempty"`
	Triggers    []Trigger    `json:"triggers,omitempty"`
	ViewDef     *View        `json:"viewDef,omitempty"`
}

// Type is a column's or a parameter's type: a system type, with an alias
// type's name where it was declared with one.
type Type struct {
	// Name is the system type's name, such as nvarchar or decimal.
	Name string `json:"type"`
	// MaxLength is sys.columns' max_length: bytes, or -1 for (max).
	MaxLength int16 `json:"maxLength"`
	Precision uint8 `json:"precision,omitempty"`
	Scale     uint8 `json:"scale,omitempty"`
	// Alias is an alias type's schema.name, empty for a system type.
	Alias string `json:"alias,omitempty"`
}

// Column is a table or view column.
type Column struct {
	Name string `json:"name"`
	Type
	Nullable  bool   `json:"nullable,omitempty"`
	Collation string `json:"collation,omitempty"`
	// Identity is set for an IDENTITY column.
	Identity *Identity `json:"identity,omitempty"`
	// Computed is set for a computed column.
	Computed *Computed `json:"computed,omitempty"`
	// Default is the column's default constraint, if it has one.
	Default *Default `json:"default,omitempty"`
	// RowGUID is set for the table's ROWGUIDCOL.
	RowGUID bool   `json:"rowGuid,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// Identity is an IDENTITY column's seed and increment, as text.
type Identity struct {
	Seed      string `json:"seed"`
	Increment string `json:"increment"`
}

// Computed is a computed column's expression, as SQL Server prints it.
type Computed struct {
	Definition string `json:"definition"`
	Persisted  bool   `json:"persisted,omitempty"`
}

// Default is a default constraint, its expression as SQL Server prints it.
type Default struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

// Check is a check constraint, its expression as SQL Server prints it.
type Check struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
	Disabled   bool   `json:"disabled,omitempty"`
	NotTrusted bool   `json:"notTrusted,omitempty"`
	Comment    string `json:"comment,omitempty"`
}

// Index is an index. A primary key or unique constraint is the index it is
// enforced by, named as the constraint.
type Index struct {
	Name string `json:"name"`
	// Type is sys.indexes' type_desc, such as CLUSTERED or NONCLUSTERED.
	Type             string `json:"type"`
	Unique           bool   `json:"unique,omitempty"`
	PrimaryKey       bool   `json:"primaryKey,omitempty"`
	UniqueConstraint bool   `json:"uniqueConstraint,omitempty"`
	// Filter is a filtered index's WHERE clause, as SQL Server prints it.
	Filter   string      `json:"filter,omitempty"`
	Disabled bool        `json:"disabled,omitempty"`
	Parts    []IndexPart `json:"parts"`
	Included []string    `json:"included,omitempty"`
	// Comment is the constraint's for a key constraint, else the index's.
	Comment string `json:"comment,omitempty"`
}

// IndexPart is one key column of an index.
type IndexPart struct {
	Column     string `json:"column"`
	Descending bool   `json:"descending,omitempty"`
}

// ForeignKey is a foreign key constraint.
type ForeignKey struct {
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	RefSchema string   `json:"refSchema"`
	RefTable  string   `json:"refTable"`
	RefCols   []string `json:"refColumns"`
	// OnUpdate and OnDelete are NO_ACTION, CASCADE, SET_NULL or SET_DEFAULT.
	OnUpdate   string `json:"onUpdate"`
	OnDelete   string `json:"onDelete"`
	Disabled   bool   `json:"disabled,omitempty"`
	NotTrusted bool   `json:"notTrusted,omitempty"`
	Comment    string `json:"comment,omitempty"`
}

// Trigger is a table's DML trigger.
type Trigger struct {
	Name      string `json:"name"`
	InsteadOf bool   `json:"insteadOf,omitempty"`
	// Events are INSERT, UPDATE and DELETE, in that order, as far as it fires on them.
	Events     []string `json:"events"`
	Disabled   bool     `json:"disabled,omitempty"`
	Definition string   `json:"definition"`
}

// View is a view's definition.
type View struct {
	Definition  string `json:"definition"`
	CheckOption bool   `json:"checkOption,omitempty"`
	SchemaBound bool   `json:"schemaBound,omitempty"`
	// Reads are the tables and views the view reads, as schema.name, or
	// database.schema.name in another database.
	Reads []string `json:"reads,omitempty"`
}

// Routine is a function or procedure written in T-SQL.
type Routine struct {
	Name string `json:"name"`
	// Type is sys.objects' type_desc: SQL_SCALAR_FUNCTION,
	// SQL_INLINE_TABLE_VALUED_FUNCTION, SQL_TABLE_VALUED_FUNCTION or
	// SQL_STORED_PROCEDURE.
	Type    string `json:"type"`
	Comment string `json:"comment,omitempty"`
	// Returns is a scalar function's result type.
	Returns *Type `json:"returns,omitempty"`
	// Columns are a table-valued function's result columns.
	Columns     []Column    `json:"columns,omitempty"`
	Parameters  []Parameter `json:"parameters,omitempty"`
	SchemaBound bool        `json:"schemaBound,omitempty"`
	Definition  string      `json:"definition,omitempty"`
}

// Parameter is a routine's parameter, its name without the @.
type Parameter struct {
	Name string `json:"name"`
	Type
	Output bool `json:"output,omitempty"`
}
