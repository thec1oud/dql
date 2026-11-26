package introspect

// Snapshot is everything mysql-live reads from one database: the raw
// material of the catalog IR, the extensions of §6.2 and the comments that
// carry annotations (§7.1). It holds names and text as the server reports
// them and no table statistics, auto-increment counters or timestamps, so
// the same schema always yields the same Snapshot (§3.2 rule 3).
//
// Columns and index parts keep database order (§3.2 rule 1). Every other
// list is sorted by name, compared bytewise.
//
// Text fields are empty where information_schema holds NULL, except
// Column.Default, which is nil for no default.
type Snapshot struct {
	Database Database  `json:"database"`
	Tables   []Table   `json:"tables,omitempty"`
	Routines []Routine `json:"routines,omitempty"`
}

// Database is the server's flavor and the database's defaults. Its name is
// left out: the catalog name comes from config.
type Database struct {
	// Flavor is mysql or mariadb.
	Flavor       string      `json:"flavor"`
	CharacterSet string      `json:"characterSet"`
	Collation    string      `json:"collation"`
	Collations   []Collation `json:"collations,omitempty"`
}

// Collation is one collation the database's columns use, with the facts
// comparisons depend on (§8.9).
type Collation struct {
	Name string `json:"name"`
	// PadSpace is set when trailing spaces are ignored in comparisons.
	PadSpace bool `json:"padSpace,omitempty"`
}

// Table is a base table or a view.
type Table struct {
	Name string `json:"name"`
	// View is set for a view.
	View          bool         `json:"view,omitempty"`
	Comment       string       `json:"comment,omitempty"`
	Engine        string       `json:"engine,omitempty"`
	Collation     string       `json:"collation,omitempty"`
	CreateOptions string       `json:"createOptions,omitempty"`
	Columns       []Column     `json:"columns"`
	Checks        []Check      `json:"checks,omitempty"`
	Indexes       []Index      `json:"indexes,omitempty"`
	ForeignKeys   []ForeignKey `json:"foreignKeys,omitempty"`
	Triggers      []Trigger    `json:"triggers,omitempty"`
	ViewDef       *View        `json:"viewDef,omitempty"`
}

// Column is a table or view column.
type Column struct {
	Name string `json:"name"`
	// DataType is the bare type name, such as bigint or varchar.
	DataType string `json:"dataType"`
	// ColumnType is the full type as the server prints it, such as
	// bigint unsigned, varchar(80) or enum('a','b').
	ColumnType string `json:"columnType"`
	Nullable   bool   `json:"nullable,omitempty"`
	// Default is the default as information_schema prints it: a constant
	// unquoted on MySQL and quoted on MariaDB, or an expression.
	Default *string `json:"default,omitempty"`
	// Extra is information_schema's EXTRA: auto_increment,
	// DEFAULT_GENERATED, on update ..., STORED GENERATED, VIRTUAL GENERATED.
	Extra        string `json:"extra,omitempty"`
	Generation   string `json:"generation,omitempty"`
	CharacterSet string `json:"characterSet,omitempty"`
	Collation    string `json:"collation,omitempty"`
	Comment      string `json:"comment,omitempty"`
}

// Check is a check constraint, its clause as the server prints it.
type Check struct {
	Name     string `json:"name"`
	Clause   string `json:"clause"`
	Enforced bool   `json:"enforced"`
}

// Index is an index, unique or not; the primary key is the index PRIMARY.
type Index struct {
	Name    string      `json:"name"`
	Unique  bool        `json:"unique,omitempty"`
	Type    string      `json:"type"`
	Visible bool        `json:"visible"`
	Comment string      `json:"comment,omitempty"`
	Parts   []IndexPart `json:"parts"`
}

// IndexPart is one element of an index.
type IndexPart struct {
	Column string `json:"column,omitempty"`
	// Prefix is the length of a prefix index part, 0 for the whole column.
	Prefix     uint32 `json:"prefix,omitempty"`
	Expression string `json:"expression,omitempty"`
	Descending bool   `json:"descending,omitempty"`
}

// ForeignKey is a foreign key constraint. References name a table in the
// same database, or in another one, which is another catalog.
type ForeignKey struct {
	Name     string   `json:"name"`
	Columns  []string `json:"columns"`
	RefDB    string   `json:"refDb"`
	RefTable string   `json:"refTable"`
	RefCols  []string `json:"refColumns"`
	OnUpdate string   `json:"onUpdate"`
	OnDelete string   `json:"onDelete"`
}

// Trigger is a table's trigger.
type Trigger struct {
	Name      string `json:"name"`
	Timing    string `json:"timing"`
	Event     string `json:"event"`
	Order     uint32 `json:"order"`
	Statement string `json:"statement"`
}

// View is a view's definition.
type View struct {
	Definition   string `json:"definition"`
	Updatable    bool   `json:"updatable,omitempty"`
	SecurityType string `json:"securityType"`
	CheckOption  string `json:"checkOption"`
	// Reads are the tables and views the view reads, as database.name.
	Reads []string `json:"reads,omitempty"`
}

// Routine is a stored function or procedure.
type Routine struct {
	Name string `json:"name"`
	// Type is FUNCTION or PROCEDURE.
	Type    string `json:"type"`
	Comment string `json:"comment,omitempty"`
	// Returns is a function's result type, as DTD_IDENTIFIER prints it.
	Returns       string     `json:"returns,omitempty"`
	Parameters    []Argument `json:"parameters,omitempty"`
	Deterministic bool       `json:"deterministic,omitempty"`
	DataAccess    string     `json:"dataAccess"`
	SecurityType  string     `json:"securityType"`
	Definition    string     `json:"definition,omitempty"`
}

// Argument is a routine's parameter.
type Argument struct {
	Name string `json:"name"`
	// Mode is IN, OUT or INOUT; empty for a function's.
	Mode string `json:"mode,omitempty"`
	Type string `json:"type"`
}
