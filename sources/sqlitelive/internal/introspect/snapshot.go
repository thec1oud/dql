package introspect

// Snapshot is everything sqlite-live reads from one database's main schema:
// the raw material of the catalog IR, the extensions of §6.2 and the SQL
// text that carries checks and the comments of annotations (§7.1). It
// holds no statistics or page counts, so the same schema always yields the
// same Snapshot (§3.2 rule 3).
//
// Columns and index parts keep database order (§3.2 rule 1). Every other
// list is sorted by name, compared bytewise.
type Snapshot struct {
	Database Database `json:"database"`
	Tables   []Table  `json:"tables,omitempty"`
}

// Database is the library's version and the database's text encoding.
type Database struct {
	Version  string `json:"version"`
	Encoding string `json:"encoding"`
}

// Table is a table or a view, with the statement sqlite_schema stores.
type Table struct {
	Name string `json:"name"`
	// View is set for a view.
	View         bool         `json:"view,omitempty"`
	SQL          string       `json:"sql"`
	Strict       bool         `json:"strict,omitempty"`
	WithoutRowid bool         `json:"withoutRowid,omitempty"`
	Columns      []Column     `json:"columns"`
	Indexes      []Index      `json:"indexes,omitempty"`
	ForeignKeys  []ForeignKey `json:"foreignKeys,omitempty"`
	Triggers     []Trigger    `json:"triggers,omitempty"`
}

// Column is a column as PRAGMA table_xinfo reports it.
type Column struct {
	Name string `json:"name"`
	// Type is the declared type, empty for none.
	Type    string `json:"type,omitempty"`
	NotNull bool   `json:"notNull,omitempty"`
	// Default is dflt_value, nil for none.
	Default *string `json:"default,omitempty"`
	// PrimaryKey is the column's position in the primary key from 1, or 0.
	PrimaryKey int `json:"primaryKey,omitempty"`
	// Hidden is 2 for a virtual generated column and 3 for a stored one.
	Hidden int `json:"hidden,omitempty"`
}

// Index is an index as PRAGMA index_list and index_xinfo report it.
type Index struct {
	Name   string `json:"name"`
	Unique bool   `json:"unique,omitempty"`
	// Origin is c for CREATE INDEX, u for a UNIQUE constraint and pk for
	// the primary key.
	Origin  string      `json:"origin"`
	Partial bool        `json:"partial,omitempty"`
	Parts   []IndexPart `json:"parts"`
	// SQL is the CREATE INDEX statement, empty for one a constraint
	// creates.
	SQL string `json:"sql,omitempty"`
}

// IndexPart is a key element of an index.
type IndexPart struct {
	// Column is empty for an expression.
	Column     string `json:"column,omitempty"`
	Descending bool   `json:"descending,omitempty"`
	Collation  string `json:"collation"`
}

// ForeignKey is a foreign key as PRAGMA foreign_key_list reports it.
// RefColumns is empty when the key references its target's primary key.
type ForeignKey struct {
	Columns    []string `json:"columns"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns,omitempty"`
	OnUpdate   string   `json:"onUpdate"`
	OnDelete   string   `json:"onDelete"`
}

// Trigger is a table's trigger.
type Trigger struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
}
