// Package diag carries diagnostics produced during parse, type-check, and SSA
// construction. It is intentionally small: a single Diagnostic record plus a
// Severity enum, with no dependencies on frontend or interp internals.
package diag

import (
	"fmt"
	"go/token"
)

// Severity classifies a diagnostic.
type Severity int

const (
	// SeverityError is a fatal diagnostic: the unit cannot be executed.
	SeverityError Severity = iota
	// SeverityWarning is non-fatal: the unit is still executable.
	SeverityWarning
	// SeverityInfo is informational only.
	SeverityInfo
)

// String returns a short label for the severity.
func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	default:
		return "unknown"
	}
}

// Diagnostic is a single problem reported by the frontend or interpreter.
// Pos is optional (use token.NoPos when unavailable).
//
//nolint:errname // Diagnostic is the domain term; Error formats it for aggregation.
type Diagnostic struct {
	Severity Severity
	Pos      token.Position
	Message  string
}

// Error returns the canonical "<file>:<line>:<col>: <severity>: <message>"
// representation, matching go/types diagnostic style.
func (d Diagnostic) Error() string {
	if d.Pos.IsValid() {
		return fmt.Sprintf("%s: %s: %s", d.Pos, d.Severity, d.Message)
	}
	return fmt.Sprintf("%s: %s", d.Severity, d.Message)
}
