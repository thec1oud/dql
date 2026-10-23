// Package fidelity names what a Postgres source captures less completely than
// pg-live, which reads the live catalog, and normalizes those differences
// away, so two catalogs of one schema can be compared (implementation guide
// M1).
//
// A source whose provenance is SOURCE_FIDELITY_PARTIAL lists the limits that
// apply to it in provenance.fidelity_limits, sorted. The names are stable
// kebab-case strings. Normalize applies each limit's documented
// normalization to a catalog: Normalize(live, limits) and
// Normalize(snapshot, limits), with the snapshot catalog's limits, differ
// only where no documented limit explains the difference.
package fidelity

// The limits of the dpg-snapshot source: what a dpg snapshot does not record
// about the database it describes.
const (
	// BodyHashedObjects: aggregates, casts, collations, operators, extended
	// statistics, base types, text search objects and the other kinds dpg
	// keeps by a hash of their definition have no definition in the
	// snapshot; they travel as dpg records only. Normalized by dropping the
	// Statistics, Collation and BaseType extensions and the Routine
	// extensions of aggregates.
	BodyHashedObjects = "body-hashed-objects"
	// CollationFacts: only the built-in text types are known to be
	// collatable, so a column of any other collatable type, such as an
	// extension's, has no collation; and a collation outside pg_catalog has
	// comparison facts only when the snapshot defines it with its properties.
	// Normalized by dropping the collation of every column, attribute and
	// domain whose type is not a built-in text type, and clearing the facts
	// of collations outside pg_catalog.
	CollationFacts = "collation-facts"
	// DatabaseSettings: the snapshot holds no database encoding or default
	// locale. Normalized by dropping the Database extension.
	DatabaseSettings = "database-settings"
	// DerivedNames: constraints written without a name, and the sequences
	// behind identity and serial columns, are recorded without names; they
	// are named by Postgres's own rule (users_pkey, posts_title_check,
	// users_id_seq), which is exact unless Postgres had to number a name to
	// avoid a clash. Not normalized.
	DerivedNames = "derived-names"
	// ExpressionText: defaults, generated columns, check and exclusion
	// constraints, index keys and predicates, policies, partition keys and
	// view queries are the text the dpg source wrote, not Postgres's deparse
	// ('draft' where pg-live reads 'draft'::app.post_status, §10.4, §11.3);
	// the definitions only the server prints, of indexes and triggers, are
	// empty. Normalized by clearing that text.
	ExpressionText = "expression-text"
	// Grants: the snapshot holds the GRANT and REVOKE statements dpg applies,
	// not the objects' ACLs, so grants travel as dpg records. Normalized by
	// dropping Grant extensions and the grants of other extensions.
	Grants = "grants"
	// MaterializedViews: a view record does not say whether the view is
	// materialized. A view is read as materialized when it has indexes, WITH
	// NO DATA or a tablespace, which a plain view cannot have, and as a plain
	// view otherwise. Normalized by reading every materialized view as a view.
	MaterializedViews = "materialized-views"
	// Owners: an owner is recorded only where dpg declares one. Normalized by
	// dropping Owner extensions and the owners of other extensions.
	Owners = "owners"
	// Partitions: a partition is recorded inside its parent with its name,
	// bound and own constraints only. Normalized by dropping Partition
	// extensions.
	Partitions = "partitions"
	// RangeSubtypes: a range type's record has no subtype, so a column of a
	// range type outside pg_catalog is opaque (§8.3). Normalized by reading
	// every such range or multirange as opaque and dropping RangeType
	// extensions.
	RangeSubtypes = "range-subtypes"
	// RoutineArguments: a routine is recorded by its input argument types
	// only, with no names, modes, defaults or OUT arguments (§6.5).
	// Normalized by removing OUT arguments, clearing argument names, modes,
	// defaults and identity arguments, and ordering overloads by their
	// argument types.
	RoutineArguments = "routine-arguments"
	// RoutineBodies: a routine's body is recorded as a hash only, and a
	// procedure's language not at all. Normalized by clearing bodies, and the
	// language of procedures.
	RoutineBodies = "routine-bodies"
	// SearchPath: names written without a schema are resolved in the schema
	// of the object that uses them, then in public, not by the search path
	// the object was created under. Not normalized.
	SearchPath = "search-path"
	// UnmanagedObjects: only what dpg manages is recorded. Objects created
	// outside dpg, extension members, extensions dpg does not declare (such
	// as plpgsql) and drift since the last recorded migration are absent.
	// Normalized by dropping InstalledExtension extensions and every object
	// that belongs to an installed extension.
	UnmanagedObjects = "unmanaged-objects"
	// ViewColumnComments: view columns carry no comments, so their
	// annotations, such as nonnull (§6.4), must come from config. Normalized
	// by dropping view columns' comments and the annotations read from them.
	ViewColumnComments = "view-column-comments"
	// ViewColumnTypes: a view records its query, not its columns. Columns are
	// read from the query when every output column is a plain reference to a
	// column of a table or view of the snapshot, with that column's type and
	// collation, nullable as Postgres reports every view column (§6.4);
	// otherwise the view has no columns and a warning says so. Not
	// normalized.
	ViewColumnTypes = "view-column-types"
)

// DPGSnapshot lists the dpg-snapshot source's limits, sorted.
var DPGSnapshot = []string{
	BodyHashedObjects,
	CollationFacts,
	DatabaseSettings,
	DerivedNames,
	ExpressionText,
	Grants,
	MaterializedViews,
	Owners,
	Partitions,
	RangeSubtypes,
	RoutineArguments,
	RoutineBodies,
	SearchPath,
	UnmanagedObjects,
	ViewColumnComments,
	ViewColumnTypes,
}
