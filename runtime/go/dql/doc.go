// Package dql is the runtime library generated Go code calls (design record
// §8.1, §13, §22, §23): the executor, its handles and transactions, the
// error taxonomy, validation and decode errors, the generic helpers for
// collections of shapes, and the host types of the logical types bit,
// range<T> and interval (Bits, Range and Interval).
//
// The executor holds no driver: it runs on a Driver, which an adapter
// package such as dqlpgx provides, and a fake implements in tests. Reads
// outside write transactions leave the primary when their consistency
// level allows: a tracker polls each replica's lag, and a Session records
// a request's commit positions for read-your-writes, with the SQL for both
// from the dialect's Replication.
//
// Builder queries reach the executor as plans over the dialect's generated
// fragment table (§16.2): Fragments.Assemble fills holes, joins fragments
// and numbers placeholders, and never writes SQL of its own.
//
// No error of this package carries a parameter or row value (§23, §23.3): a
// message names classes, shapes, fields, constraints and queries, and a
// database's own message or detail, which may quote values, is kept in a
// separate field that the executor clears for sensitive columns.
package dql
