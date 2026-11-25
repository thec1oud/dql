package dql

import (
	"errors"
	"fmt"
	"slices"
)

// StatementMode is how a source sends statements (§26.4). Fragments give
// each query variant a stable SQL text, so prepared statements can be
// cached by text.
type StatementMode int

const (
	// ModePrepare prepares and caches named statements, for direct
	// connections. It is the zero value, so the default.
	ModePrepare StatementMode = iota
	// ModeDescribe uses unnamed statements, for poolers in transaction mode
	// such as PgBouncer.
	ModeDescribe
	// ModeSimple prepares nothing, for proxies that support nothing else.
	ModeSimple
)

func (m StatementMode) String() string {
	switch m {
	case ModePrepare:
		return "prepare"
	case ModeDescribe:
		return "describe"
	case ModeSimple:
		return "simple"
	}
	return fmt.Sprintf("StatementMode(%d)", int(m))
}

// ErrStatementMode is the error of a source whose statement mode the
// dialect's drivers do not support (§26.4).
var ErrStatementMode = errors.New("dql: the dialect does not support the statement mode")

// checkMode fails when the dialect declares its modes and m is not one.
func (d *Dialect) checkMode(source string, m StatementMode) error {
	if d.StatementModes == nil || slices.Contains(d.StatementModes, m) {
		return nil
	}
	return fmt.Errorf("dql: source %s: %w: %s", source, ErrStatementMode, m)
}
