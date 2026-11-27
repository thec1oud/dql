package mapping

import (
	"strconv"
	"unicode/utf8"

	"github.com/thec1oud/dql/sources/dpgsnapshot/internal/format"
)

// namer gives names to what a snapshot records without one: constraints
// written without a name, and the sequences behind identity and serial
// columns. It follows Postgres's own rule (ChooseRelationName,
// ChooseConstraintName and makeObjectName in Postgres's source), so the
// names are the ones the database holds, unless Postgres had to number a
// name to avoid a clash with an object created in another order
// (fidelity.DerivedNames).
type namer struct {
	// relations holds the schema's relation names: tables, views, sequences,
	// indexes and composite types. constraints holds its constraint names,
	// of tables and domains alike.
	relations, constraints map[string]bool
}

// namer returns the namer of schema, which knows every name the snapshot
// gives there, so a derived name never takes a name the snapshot uses.
func (m *mapper) namer(schema string) *namer {
	if n, ok := m.names[schema]; ok {
		return n
	}
	n := &namer{relations: map[string]bool{}, constraints: map[string]bool{}}
	m.names[schema] = n
	for q, t := range m.tables {
		if q.schema != schema {
			continue
		}
		n.relations[t.Name] = true
		for _, p := range t.Partitions {
			n.partition(schema, p)
		}
		n.indexes(t.Indexes)
		for _, c := range t.Constraints {
			n.constraint(c)
		}
	}
	for q, v := range m.views {
		if q.schema == schema {
			n.relations[v.Name] = true
			n.indexes(v.Indexes)
		}
	}
	for q, t := range m.typeDefs {
		if q.schema != schema {
			continue
		}
		if t.Variant == "COMPOSITE" {
			n.relations[t.Name] = true
		}
		for _, c := range t.DomainConstraints {
			n.constraint(c)
		}
	}
	for q := range m.sequences {
		if q.schema == schema {
			n.relations[q.name] = true
		}
	}
	return n
}

func (n *namer) partition(schema string, p format.Partition) {
	if p.Schema == "" || p.Schema == schema {
		n.relations[p.Name] = true
	}
	for _, c := range p.Partitions {
		n.partition(schema, c)
	}
}

func (n *namer) indexes(list []format.Index) {
	for _, i := range list {
		n.relations[i.Name] = true
	}
}

// constraint records a named constraint. A primary key, unique or exclusion
// constraint is also an index, so its name is a relation name too.
func (n *namer) constraint(c format.Constraint) {
	if c.Name == "" {
		return
	}
	n.constraints[c.Name] = true
	if indexBacked[c.Type] {
		n.relations[c.Name] = true
	}
}

var indexBacked = map[string]bool{"PRIMARY KEY": true, "UNIQUE": true, "EXCLUDE": true}

// relation names a new relation, as Postgres's ChooseRelationName does: an
// index behind a constraint, or a sequence behind a column.
func (n *namer) relation(name1, name2, label string) string {
	name := choose(n.relations, name1, name2, label)
	n.relations[name] = true
	return name
}

// indexConstraint names a constraint backed by an index, which takes a
// relation name and a constraint name at once.
func (n *namer) indexConstraint(name1, name2, label string) string {
	name := n.relation(name1, name2, label)
	n.constraints[name] = true
	return name
}

// check names a check or foreign key constraint, as Postgres's
// ChooseConstraintName does.
func (n *namer) check(name1, name2, label string) string {
	name := choose(n.constraints, name1, name2, label)
	n.constraints[name] = true
	return name
}

// choose returns makeObjectName(name1, name2, label) or, when that is used,
// the first unused name with label followed by 1, 2, and so on.
func choose(used map[string]bool, name1, name2, label string) string {
	name := makeObjectName(name1, name2, label)
	for pass := 1; used[name]; pass++ {
		name = makeObjectName(name1, name2, label+strconv.Itoa(pass))
	}
	return name
}

// nameDataLen is Postgres's NAMEDATALEN: names hold at most 63 bytes.
const nameDataLen = 64

// makeObjectName joins name1, name2 (which may be empty) and label with
// underscores. When the result would exceed 63 bytes, the longer of name1
// and name2 is shortened, one byte at a time, then cut back to a whole
// UTF-8 character, as Postgres's makeObjectName does.
func makeObjectName(name1, name2, label string) string {
	overhead := len(label) + 1
	if name2 != "" {
		overhead++
	}
	n1, n2 := len(name1), len(name2)
	for n1+n2 > nameDataLen-1-overhead {
		if n1 > n2 {
			n1--
		} else {
			n2--
		}
	}
	name := clip(name1, n1)
	if name2 != "" {
		name += "_" + clip(name2, n2)
	}
	return name + "_" + label
}

// clip returns the longest prefix of s of at most n bytes that ends on a
// character boundary.
func clip(s string, n int) string {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
