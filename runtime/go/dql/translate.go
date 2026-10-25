package dql

import (
	"context"
	"errors"
)

// ErrorMap is the generated mapping from a dialect's errors into the
// taxonomy (§23): its native codes, and how it reports a violation of each
// rule of the catalogs (§23.2).
type ErrorMap struct {
	// The class of each native code that maps into the taxonomy.
	Codes map[string]Class
	// Each rule's constraint, by where the database reports its violation.
	Violations map[Reported]Constraint
}

// Reported is how a database reports a violation: the catalog the
// statement ran on, the schema and the table or domain, the class, and the
// name the error carries, such as a constraint or, for a not-null
// violation, a column.
type Reported struct {
	Catalog string
	Schema  string
	Object  string
	Class   Class
	Name    string
}

// translate maps err, from a statement of query q on catalog of the given
// source, into the taxonomy. A violation of a rule the map does not know
// names what the database reported and drops its detail.
func (m *ErrorMap) translate(d Driver, dialect string, err error, catalog, q, source string) error {
	o := Origin{Dialect: dialect, Query: q}
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return &Canceled{Origin: o, Err: err}
	case errors.Is(err, context.DeadlineExceeded):
		return &Timeout{Origin: o, Err: err}
	}
	if ClassOf(err) != "" {
		return err
	}
	n, ok := d.Native(err)
	if !ok {
		if d.Broken(err) {
			return &ConnectionError{Origin: o, Source: source, Err: err}
		}
		return &DatabaseError{Origin: o, Message: err.Error()}
	}
	o.Code = n.Code
	class := m.Codes[n.Code]
	switch class {
	case ClassUniqueViolation, ClassForeignKeyViolation, ClassCheckViolation, ClassNotNullViolation, ClassExclusionViolation:
		return m.violation(n, class, o, catalog)
	case ClassSerializationFailure:
		return &SerializationFailure{Origin: o}
	case ClassDeadlock:
		return &Deadlock{Origin: o}
	case ClassLockTimeout:
		return &LockTimeout{Origin: o}
	case ClassTimeout:
		return &Timeout{Origin: o, Err: err}
	case ClassConnectionError:
		return &ConnectionError{Origin: o, Source: source, Err: err}
	}
	return &DatabaseError{Origin: o, Message: n.Message, Detail: n.Detail}
}

func (m *ErrorMap) violation(n NativeError, class Class, o Origin, catalog string) error {
	object := n.Table
	if object == "" {
		object = n.DataType
	}
	name := n.Constraint
	if class == ClassNotNullViolation && n.Constraint == "" {
		name = n.Column
	}
	c, known := m.Violations[Reported{Catalog: catalog, Schema: n.Schema, Object: object, Class: class, Name: name}]
	if !known {
		c = Constraint{Name: join(join(join(catalog, n.Schema), object), name), Sensitive: true}
	}
	v := Violation{Origin: o, Constraint: c}
	if !c.Sensitive {
		v.Detail = n.Detail
	}
	switch class {
	case ClassUniqueViolation:
		return &UniqueViolation{v}
	case ClassForeignKeyViolation:
		return &ForeignKeyViolation{v}
	case ClassCheckViolation:
		return &CheckViolation{v}
	case ClassExclusionViolation:
		return &ExclusionViolation{v}
	}
	e := &NotNullViolation{Violation: v, Column: join(join(join(catalog, n.Schema), object), n.Column)}
	if n.Constraint == "" {
		// The database names the column, not a constraint.
		e.Constraint.Name = ""
	}
	return e
}
