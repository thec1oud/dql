// Package roundtrip holds the introspection round trips of every live source
// (implementation guide §6.5): read a database, write its catalog as DDL with
// testkit/ddl, apply that to an empty database, read it again and compare.
package roundtrip
