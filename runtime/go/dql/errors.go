package dql

import (
	"errors"
	"strings"
)

// Class names an error's place in the taxonomy of §23.1. Its values are the
// taxonomy's names, as hooks report them and conformance scenarios match them.
type Class string

// The classes of §23.1.
const (
	// Cardinality
	ClassNotFound    Class = "NotFound"
	ClassTooManyRows Class = "TooManyRows"

	// Constraints
	ClassUniqueViolation     Class = "UniqueViolation"
	ClassForeignKeyViolation Class = "ForeignKeyViolation"
	ClassCheckViolation      Class = "CheckViolation"
	ClassNotNullViolation    Class = "NotNullViolation"
	ClassExclusionViolation  Class = "ExclusionViolation"

	// Concurrency
	ClassSerializationFailure   Class = "SerializationFailure"
	ClassDeadlock               Class = "Deadlock"
	ClassLockTimeout            Class = "LockTimeout"
	ClassConcurrentModification Class = "ConcurrentModification"

	// Client-side
	ClassValidationErrors Class = "ValidationErrors"
	ClassDecodeError      Class = "DecodeError"

	// Infrastructure
	ClassConnectionError    Class = "ConnectionError"
	ClassPoolExhausted      Class = "PoolExhausted"
	ClassTimeout            Class = "Timeout"
	ClassCanceled           Class = "Canceled"
	ClassReplicaUnavailable Class = "ReplicaUnavailable"

	// Fallback
	ClassDatabaseError Class = "DatabaseError"
)

// Error is implemented by every error of the taxonomy.
type Error interface {
	error
	// Class returns the error's class.
	Class() Class
}

// ClassOf returns the class of the first error in err's tree that is an
// Error, or "" when there is none.
func ClassOf(err error) Class {
	var e Error
	if errors.As(err, &e) {
		return e.Class()
	}
	return ""
}

// Origin says where an error arose (§23.1): the dialect, the database's native
// code where there is one, such as a SQLSTATE, and the name of the query or
// operation.
type Origin struct {
	Dialect string
	Code    string
	Query   string
}

// suffix formats the origin for an error message.
func (o Origin) suffix() string {
	var b strings.Builder
	if o.Query != "" {
		b.WriteString(" in ")
		b.WriteString(o.Query)
	}
	if o.Dialect != "" || o.Code != "" {
		b.WriteString(" (")
		b.WriteString(strings.TrimSpace(o.Dialect + " " + o.Code))
		b.WriteString(")")
	}
	return b.String()
}

func message(c Class, what string, o Origin) string {
	if what != "" {
		what = " " + what
	}
	return "dql: " + string(c) + what + o.suffix()
}

// NotFound reports that an operation that needs a row found none (§19).
type NotFound struct{ Origin }

// TooManyRows reports that an operation that allows at most one row found
// more (§19).
type TooManyRows struct{ Origin }

// SerializationFailure reports that the database aborted a transaction to
// keep it serializable.
type SerializationFailure struct{ Origin }

// Deadlock reports that the database aborted a transaction to break a
// deadlock.
type Deadlock struct{ Origin }

// LockTimeout reports that a lock was not granted in time.
type LockTimeout struct{ Origin }

// ConcurrentModification reports that a versioned write found its row at
// another version (§20.4).
type ConcurrentModification struct{ Origin }

// PoolExhausted reports that no connection of a source's pool became free in
// time.
type PoolExhausted struct {
	Origin
	Source string
}

// ReplicaUnavailable reports that no replica could serve a read at the
// consistency level it asked for (§22.5).
type ReplicaUnavailable struct {
	Origin
	Source string
}

// ConnectionError reports that a connection to a source failed. Err is the
// driver's error.
type ConnectionError struct {
	Origin
	Source string
	Err    error
}

// Timeout reports that an operation ran out of time. Err is the cause, such
// as context.DeadlineExceeded.
type Timeout struct {
	Origin
	Err error
}

// Canceled reports that an operation was canceled. Err is the cause, such as
// context.Canceled.
type Canceled struct {
	Origin
	Err error
}

// DatabaseError is any database error no other class covers. Message is the
// database's own message and Detail its detail: either may quote values, so
// neither is part of Error(), and the executor clears both when a sensitive
// column is involved (§23.3).
type DatabaseError struct {
	Origin
	Message string
	Detail  string
}

func (e *NotFound) Class() Class               { return ClassNotFound }
func (e *TooManyRows) Class() Class            { return ClassTooManyRows }
func (e *SerializationFailure) Class() Class   { return ClassSerializationFailure }
func (e *Deadlock) Class() Class               { return ClassDeadlock }
func (e *LockTimeout) Class() Class            { return ClassLockTimeout }
func (e *ConcurrentModification) Class() Class { return ClassConcurrentModification }
func (e *PoolExhausted) Class() Class          { return ClassPoolExhausted }
func (e *ReplicaUnavailable) Class() Class     { return ClassReplicaUnavailable }
func (e *ConnectionError) Class() Class        { return ClassConnectionError }
func (e *Timeout) Class() Class                { return ClassTimeout }
func (e *Canceled) Class() Class               { return ClassCanceled }
func (e *DatabaseError) Class() Class          { return ClassDatabaseError }

func (e *NotFound) Error() string             { return message(e.Class(), "", e.Origin) }
func (e *TooManyRows) Error() string          { return message(e.Class(), "", e.Origin) }
func (e *SerializationFailure) Error() string { return message(e.Class(), "", e.Origin) }
func (e *Deadlock) Error() string             { return message(e.Class(), "", e.Origin) }
func (e *LockTimeout) Error() string          { return message(e.Class(), "", e.Origin) }
func (e *ConcurrentModification) Error() string {
	return message(e.Class(), "", e.Origin)
}
func (e *PoolExhausted) Error() string { return message(e.Class(), source(e.Source), e.Origin) }
func (e *ReplicaUnavailable) Error() string {
	return message(e.Class(), source(e.Source), e.Origin)
}
func (e *ConnectionError) Error() string {
	return message(e.Class(), source(e.Source), e.Origin) + cause(e.Err)
}
func (e *Timeout) Error() string       { return message(e.Class(), "", e.Origin) + cause(e.Err) }
func (e *Canceled) Error() string      { return message(e.Class(), "", e.Origin) + cause(e.Err) }
func (e *DatabaseError) Error() string { return message(e.Class(), "", e.Origin) }

// Unwrap returns the driver's error.
func (e *ConnectionError) Unwrap() error { return e.Err }

// Unwrap returns the cause.
func (e *Timeout) Unwrap() error { return e.Err }

// Unwrap returns the cause.
func (e *Canceled) Unwrap() error { return e.Err }

func source(s string) string {
	if s == "" {
		return ""
	}
	return "on source " + s
}

func cause(err error) string {
	if err == nil {
		return ""
	}
	return ": " + err.Error()
}

// ConstraintKind is the kind of a catalog constraint.
type ConstraintKind string

// The constraint kinds whose violations the taxonomy names.
const (
	ConstraintPrimaryKey ConstraintKind = "primary_key"
	ConstraintUnique     ConstraintKind = "unique"
	ConstraintForeignKey ConstraintKind = "foreign_key"
	ConstraintCheck      ConstraintKind = "check"
	ConstraintNotNull    ConstraintKind = "not_null"
	ConstraintExclusion  ConstraintKind = "exclusion"
)

// Constraint is a catalog constraint as a generated value (§23.2), so code
// branches on which rule failed without matching strings. Name is its
// qualified name, such as shop.app.users.users_tenant_email_key. Sensitive is
// true when any column it involves is sensitive, so the database's detail is
// dropped (§23.3).
type Constraint struct {
	Name      string
	Kind      ConstraintKind
	Sensitive bool
}

// Violation is what every constraint violation carries: the constraint, and
// the database's own detail, such as Postgres's "Key (email)=(a@x.com)
// already exists". Detail may quote values, so it is never part of Error(),
// and it is empty when the constraint is sensitive (§23.3).
type Violation struct {
	Origin
	Constraint Constraint
	Detail     string
}

// Violated returns the violated constraint.
func (v *Violation) Violated() Constraint { return v.Constraint }

func (v *Violation) message(c Class) string {
	return message(c, "of "+v.Constraint.Name, v.Origin)
}

// UniqueViolation reports a violated primary key or unique constraint.
type UniqueViolation struct{ Violation }

// ForeignKeyViolation reports a violated foreign key.
type ForeignKeyViolation struct{ Violation }

// CheckViolation reports a violated check constraint.
type CheckViolation struct{ Violation }

// NotNullViolation reports a NULL in a NOT NULL column. Column is the
// column's qualified name; Constraint is set where the dialect names
// not-null constraints.
type NotNullViolation struct {
	Violation
	Column string
}

// ExclusionViolation reports a violated exclusion constraint (Postgres).
type ExclusionViolation struct{ Violation }

func (e *UniqueViolation) Class() Class     { return ClassUniqueViolation }
func (e *ForeignKeyViolation) Class() Class { return ClassForeignKeyViolation }
func (e *CheckViolation) Class() Class      { return ClassCheckViolation }
func (e *NotNullViolation) Class() Class    { return ClassNotNullViolation }
func (e *ExclusionViolation) Class() Class  { return ClassExclusionViolation }

func (e *UniqueViolation) Error() string     { return e.message(e.Class()) }
func (e *ForeignKeyViolation) Error() string { return e.message(e.Class()) }
func (e *CheckViolation) Error() string      { return e.message(e.Class()) }
func (e *ExclusionViolation) Error() string  { return e.message(e.Class()) }
func (e *NotNullViolation) Error() string {
	what := "of " + e.Constraint.Name
	if e.Constraint.Name == "" {
		what = "in " + e.Column
	}
	return message(e.Class(), what, e.Origin)
}

// IsViolation reports whether err's tree holds a violation of c (§23.2).
func IsViolation(err error, c Constraint) bool {
	var v interface{ Violated() Constraint }
	return errors.As(err, &v) && v.Violated().Name == c.Name
}

// Ensure the taxonomy's types implement Error.
var _ = []Error{
	(*NotFound)(nil), (*TooManyRows)(nil),
	(*UniqueViolation)(nil), (*ForeignKeyViolation)(nil), (*CheckViolation)(nil),
	(*NotNullViolation)(nil), (*ExclusionViolation)(nil),
	(*SerializationFailure)(nil), (*Deadlock)(nil), (*LockTimeout)(nil),
	(*ConcurrentModification)(nil),
	(ValidationErrors)(nil), (*DecodeError)(nil),
	(*ConnectionError)(nil), (*PoolExhausted)(nil), (*Timeout)(nil), (*Canceled)(nil),
	(*ReplicaUnavailable)(nil),
	(*DatabaseError)(nil),
}
