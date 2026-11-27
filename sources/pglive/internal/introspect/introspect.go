// Package introspect reads a live Postgres catalog into plain Go structs: the
// raw layer of the pg-live source plugin, underneath the builder that turns a
// Snapshot into catalog IR. It reads everything the IR needs as first-class
// objects (design record §6.1) and everything it carries as extensions (§6.2),
// so the plugin can honor the lossless rule (§6.3), plus the comments that
// annotations are read from (§7.1).
package introspect

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Conn is what Read needs from a connection; *pgx.Conn and *pgxpool.Pool
// both satisfy it.
type Conn interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// Read reads the given schemas. Schemas that do not exist are absent from the
// Snapshot; extensions and the database's encoding and locale are read
// whatever the schemas.
//
// All queries run in one read-only repeatable-read transaction, so they see
// one consistent catalog. The transaction's search_path is empty: every
// deparsed expression, definition and type spelling then qualifies everything
// outside pg_catalog, as in 'draft'::app.post_status (§10.4, §11.3), and the
// text does not depend on the caller's session.
func Read(ctx context.Context, conn Conn, namespaces []string) (*Snapshot, error) {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}
	// The transaction is read-only, so ending it by rollback loses nothing.
	defer tx.Rollback(ctx)

	s := &Snapshot{}
	steps := []func() error{
		func() (err error) {
			_, err = tx.Exec(ctx, `SELECT pg_catalog.set_config('search_path', '', true)`)
			return err
		},
		func() (err error) { s.Database, err = single(collect[Database](ctx, tx, databaseSQL)); return err },
		func() (err error) { s.Extensions, err = collect[Extension](ctx, tx, extensionsSQL); return err },
		func() (err error) { s.Schemas, err = collect[Schema](ctx, tx, schemasSQL, namespaces); return err },
		func() (err error) {
			s.Collations, err = collect[Collation](ctx, tx, collationsSQL, namespaces)
			return err
		},
		func() (err error) {
			s.Relations, err = collect[Relation](ctx, tx, relationsSQL, namespaces)
			return err
		},
		func() (err error) { s.Enums, err = collect[Enum](ctx, tx, enumsSQL, namespaces); return err },
		func() (err error) { s.Domains, err = collect[Domain](ctx, tx, domainsSQL, namespaces); return err },
		func() (err error) {
			s.Composites, err = collect[Composite](ctx, tx, compositesSQL, namespaces)
			return err
		},
		func() (err error) { s.Ranges, err = collect[Range](ctx, tx, rangesSQL, namespaces); return err },
		func() (err error) {
			s.BaseTypes, err = collect[BaseType](ctx, tx, baseTypesSQL, namespaces)
			return err
		},
		func() (err error) {
			s.Sequences, err = collect[Sequence](ctx, tx, sequencesSQL, namespaces)
			return err
		},
		func() (err error) { s.Routines, err = collect[Routine](ctx, tx, routinesSQL, namespaces); return err },
		func() (err error) {
			s.Statistics, err = collect[Statistics](ctx, tx, statisticsSQL, namespaces)
			return err
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, fmt.Errorf("introspect: %w", err)
		}
	}
	return s, nil
}

// collect runs a query whose rows are single JSON documents and decodes each
// into a T.
func collect[T any](ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]T, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	docs, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		return nil, err
	}
	var out []T
	for _, doc := range docs {
		var v T
		if err := json.Unmarshal(doc, &v); err != nil {
			return nil, fmt.Errorf("decoding %T: %w", v, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// single returns the one row of a query that always returns exactly one.
func single[T any](rows []T, err error) (T, error) {
	var zero T
	if len(rows) == 0 {
		return zero, err
	}
	return rows[0], nil
}
