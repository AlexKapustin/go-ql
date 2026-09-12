// Package dialect abstracts the SQL-generation details that differ between
// database engines: identifier quoting, JSON path extraction, date
// arithmetic, and built-in function rendering. Placeholder style is
// deliberately NOT part of this interface — the walker emits GORM-style
// named placeholders (@name), which GORM's own statement builder translates
// to the correct underlying syntax at execution time, so the walker never
// needs dialect-specific positional-parameter numbering.
package dialect

import "fmt"

// FuncRenderer renders a call to a built-in or custom function given its
// already-rendered SQL argument expressions.
type FuncRenderer func(args []string) (string, error)

// Dialect owns everything SQL generation needs that varies by database
// engine.
type Dialect interface {
	// QuoteIdent quotes a single identifier (table or column name).
	QuoteIdent(name string) string

	// QuoteQualified quotes a dotted identifier chain, e.g. table.column.
	QuoteQualified(parts ...string) string

	// JSONExtract renders an expression that extracts, as text, the value
	// at path within the JSON/JSONB column expression.
	JSONExtract(column string, path []string) (string, error)

	// DateAdd renders `expr + n unit` as a date/timestamp addition.
	// unit has already been validated against the fixed whitelist
	// (second, minute, hour, day, week, month, year).
	DateAdd(expr, n, unit string) (string, error)

	// DateSub renders `expr - n unit` as a date/timestamp subtraction.
	DateSub(expr, n, unit string) (string, error)

	// Functions returns the table of built-in function renderers, keyed by
	// lower-cased function name.
	Functions() map[string]FuncRenderer
}

// ValidDateUnits is the whitelist of units DATE_ADD/DATE_SUB accept
var ValidDateUnits = map[string]bool{
	"second": true,
	"minute": true,
	"hour":   true,
	"day":    true,
	"week":   true,
	"month":  true,
	"year":   true,
}

// ErrInvalidDateUnit reports a DATE_ADD/DATE_SUB unit outside ValidDateUnits.
func ErrInvalidDateUnit(fn, unit string) error {
	return fmt.Errorf(
		"%s() only supports units of type second, minute, hour, day, week, month and year, got %q",
		fn, unit,
	)
}
