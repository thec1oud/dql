package mssqlsql

// Predicate is what a filtered index's WHERE clause requires (§21.3).
type Predicate struct {
	// NullColumns are the columns it requires to be NULL, by a top-level
	// conjunct c IS NULL.
	NullColumns []string
	// NotTrueColumns are the bit columns it requires to be false, by a
	// top-level conjunct c = 0, which SQL Server prints as ([c]=(0)).
	NotTrueColumns []string
}

// RecognizePredicate reads a filtered index's predicate; bits holds the
// table's bit columns. An error means the predicate does not parse.
func RecognizePredicate(text string, bits map[string]bool) (Predicate, error) {
	e, err := ParseCondition(text)
	if err != nil {
		return Predicate{}, err
	}
	var p Predicate
	var conjuncts func(e Expr)
	conjuncts = func(e Expr) {
		switch e := e.(type) {
		case Bin:
			if e.Op == "AND" {
				conjuncts(e.L)
				conjuncts(e.R)
				return
			}
			c, ok := e.L.(Col)
			if l, isLit := e.R.(Lit); ok && isLit && e.Op == "=" && bits[c.Name] && l.Kind == LitNumber && l.Text == "0" {
				p.NotTrueColumns = append(p.NotTrueColumns, c.Name)
			}
		case Is:
			if c, ok := e.X.(Col); ok && !e.Not {
				p.NullColumns = append(p.NullColumns, c.Name)
			}
		}
	}
	conjuncts(e)
	return p, nil
}
