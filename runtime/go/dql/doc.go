// Package dql is the runtime library generated Go code calls (design record
// §8.1, §13, §23): the error taxonomy, validation and decode errors, the
// generic helpers for collections of shapes, and the host types of the
// logical types bit, range<T> and interval (Bits, Range and Interval).
//
// No error of this package carries a parameter or row value (§23, §23.3): a
// message names classes, shapes, fields, constraints and queries, and a
// database's own message or detail, which may quote values, is kept in a
// separate field that the executor clears for sensitive columns.
package dql
