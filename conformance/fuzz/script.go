package fuzz

import (
	"fmt"
	"slices"
	"strings"
)

// Dialects are the dialects a catalog is written for, Postgres first: it
// is the reference the others are compared with.
var Dialects = []string{"postgres", "mysql", "mariadb", "sqlite", "sqlserver"}

// lacks are the features each dialect lacks: SQLite has no boolean type
// and no exact decimal.
var lacks = map[string][]string{"sqlite": {"booleans", "decimals"}}

// Lacks returns the features the catalog uses that dialect lacks; the
// catalog is not written for it then.
func (c *Catalog) Lacks(dialect string) []string {
	var ls []string
	for _, f := range c.Features() {
		if slices.Contains(lacks[dialect], f) {
			ls = append(ls, f)
		}
	}
	return ls
}

// style is how a dialect writes a catalog.
type style struct {
	// typ writes a column's type, with any collation and annotation.
	typ func(Column) string
	// length writes the length of a text column, and how many units each
	// character counts for.
	length func(col string) (string, int)
	// text and boolean write values.
	text    func(string) string
	boolean func(bool) string
	// boolNote follows a boolean column's type and nullability.
	boolNote string
	// strict ends a table definition.
	strict string
	// maxLength writes a text column's length as a check, for a dialect
	// whose text type has none.
	maxLength bool
}

var styles = map[string]style{
	"postgres": {
		typ: func(c Column) string {
			return typeName(c, "bigint", "varchar(%d)", "boolean", "numeric(%d,%d)")
		},
		length:  func(col string) (string, int) { return "char_length(" + col + ")", 1 },
		text:    quote(""),
		boolean: func(b bool) string { return strings.ToUpper(fmt.Sprint(b)) },
	},
	"mysql":   mysqlStyle,
	"mariadb": mysqlStyle,
	"sqlite": {
		typ:       func(c Column) string { return typeName(c, "INTEGER", "TEXT", "", "") },
		length:    func(col string) (string, int) { return "length(" + col + ")", 1 },
		text:      quote(""),
		strict:    " STRICT",
		maxLength: true,
	},
	"sqlserver": {
		typ: func(c Column) string {
			return typeName(c, "bigint", "nvarchar(%d) COLLATE Latin1_General_100_BIN2", "bit", "decimal(%d,%d)")
		},
		// DATALENGTH counts bytes, two per UTF-16 unit; LEN would leave
		// out trailing spaces.
		length:  func(col string) (string, int) { return "DATALENGTH(" + col + ")", 2 },
		text:    quote("N"),
		boolean: bit,
	},
}

// mysqlStyle writes text in a binary collation, and booleans as
// TINYINT(1) annotated bool (§8.7).
var mysqlStyle = style{
	typ: func(c Column) string {
		return typeName(c, "bigint", "varchar(%d) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin", "tinyint(1)", "decimal(%d,%d)")
	},
	length:   func(col string) (string, int) { return "char_length(" + col + ")", 1 },
	text:     quote(""),
	boolean:  bit,
	boolNote: " COMMENT '@bool'",
}

// typeName writes c's type from the dialect's names of each kind.
func typeName(c Column, integer, text, boolean, decimal string) string {
	switch c.Kind {
	case Integer:
		return integer
	case Text:
		if strings.Contains(text, "%d") {
			return fmt.Sprintf(text, c.Max)
		}
		return text
	case Bool:
		return boolean
	}
	return fmt.Sprintf(decimal, c.P, c.S)
}

// quote writes text in single quotes after prefix. Generated text holds no
// quote to double.
func quote(prefix string) func(string) string {
	return func(s string) string { return prefix + "'" + s + "'" }
}

// bit writes a boolean as 1 or 0.
func bit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// Script returns the DDL creating the catalog in dialect, then the
// statements inserting its rows; it errs for a dialect it is not written
// for. Statements end with semicolons, and a SQL Server script is one
// batch.
func (c *Catalog) Script(dialect string) (string, error) {
	st, ok := styles[dialect]
	if !ok {
		return "", fmt.Errorf("fuzz: no dialect %s", dialect)
	}
	if ls := c.Lacks(dialect); len(ls) > 0 {
		return "", fmt.Errorf("fuzz: %s lacks %s", dialect, strings.Join(ls, " and "))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "-- The catalog of seed %d.\n", c.Seed)
	for _, t := range c.Tables {
		b.WriteString(st.table(t))
	}
	for _, t := range c.Tables {
		b.WriteString(st.rows(t))
	}
	return b.String(), nil
}

// table writes the definition of t.
func (st style) table(t *Table) string {
	var defs []string
	for _, c := range t.Columns {
		d := c.Name + " " + st.typ(c)
		if !c.Null {
			d += " NOT NULL"
		}
		if c.Kind == Bool {
			d += st.boolNote
		}
		defs = append(defs, d)
	}
	defs = append(defs, fmt.Sprintf("CONSTRAINT %s_pkey PRIMARY KEY (%s)", t.Name, strings.Join(t.Key, ", ")))
	for _, u := range t.Uniques {
		defs = append(defs, fmt.Sprintf("CONSTRAINT %s_%s_key UNIQUE (%s)", t.Name, u, u))
	}
	for _, c := range t.Columns {
		if st.maxLength && c.Kind == Text {
			l, _ := st.length(c.Name)
			defs = append(defs, fmt.Sprintf("CONSTRAINT %s_%s_length CHECK (%s <= %d)", t.Name, c.Name, l, c.Max))
		}
	}
	for _, k := range t.Checks {
		defs = append(defs, fmt.Sprintf("CONSTRAINT %s_%s_check CHECK (%s)", t.Name, k.Column, st.check(k)))
	}
	for _, f := range t.Foreign {
		defs = append(defs, fmt.Sprintf("CONSTRAINT %s_%s_fkey FOREIGN KEY (%s) REFERENCES %s (id)", t.Name, f.Column, f.Column, f.Table))
	}
	return fmt.Sprintf("\nCREATE TABLE %s (\n  %s\n)%s;\n", t.Name, strings.Join(defs, ",\n  "), st.strict)
}

// check writes the condition of k in the first form each dialect's source
// recognizes.
func (st style) check(k Check) string {
	switch k.Kind {
	case NotEmpty:
		return k.Column + " <> " + st.text("")
	case MinLength:
		l, units := st.length(k.Column)
		return fmt.Sprintf("%s >= %d", l, k.N*units)
	case Membership:
		var ls []string
		for _, l := range k.Labels {
			ls = append(ls, st.text(l))
		}
		return k.Column + " IN (" + strings.Join(ls, ", ") + ")"
	}
	return fmt.Sprintf("%s BETWEEN %d AND %d", k.Column, k.Lo, k.Hi)
}

// rows writes one statement inserting t's rows, if it has any.
func (st style) rows(t *Table) string {
	if len(t.Rows) == 0 {
		return ""
	}
	var names, rows []string
	for _, c := range t.Columns {
		names = append(names, c.Name)
	}
	for _, r := range t.Rows {
		var vs []string
		for _, v := range r {
			vs = append(vs, st.value(v))
		}
		rows = append(rows, "("+strings.Join(vs, ", ")+")")
	}
	return fmt.Sprintf("\nINSERT INTO %s (%s) VALUES\n  %s;\n", t.Name, strings.Join(names, ", "), strings.Join(rows, ",\n  "))
}

// value writes v as a literal.
func (st style) value(v any) string {
	switch v := v.(type) {
	case nil:
		return "NULL"
	case string:
		return st.text(v)
	case bool:
		return st.boolean(v)
	}
	return fmt.Sprint(v)
}
