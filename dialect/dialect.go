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

	// JSONComparableBool renders a boolean value the way it must be written
	// to compare correctly against a JSONExtract result holding a JSON
	// boolean. Some engines' text-returning JSON extraction (e.g.
	// PostgreSQL's #>>, MySQL's ->>) can't be compared against that
	// engine's native boolean keyword — PostgreSQL rejects text = boolean
	// outright, and MySQL's numeric string coercion maps both "true" and
	// "false" to 0 — so those dialects render this as quoted text instead.
	// A dialect whose JSON extraction already yields a typed value (e.g.
	// SQLite's json_extract, which returns 0/1) returns its normal boolean
	// rendering unchanged.
	JSONComparableBool(v bool) string

	// JSONComparableNumber renders n (already-validated, well-formed
	// digit/./e/+/- text) the way it must be written to compare correctly
	// against a JSONExtract result holding a JSON number, for the same
	// reason as JSONComparableBool. Most engines coerce a numeric string
	// back to a number for comparison and so need no change here — this
	// exists for a dialect that, like JSONComparableBool's PostgreSQL case,
	// needs the number quoted as text instead.
	JSONComparableNumber(n string) string

	// JSONIsNull renders `<field> IS [NOT] NULL` for a JSON-mapped field,
	// given the same (column, path) inputs as JSONExtract rather than an
	// already-extracted expression. This exists because some engines'
	// extraction can't tell an explicit JSON null value apart from a
	// missing key using a plain "extracted IS NULL" check — e.g. MySQL's
	// ->>/JSON_EXTRACT return SQL NULL for a missing key, but the text
	// "null" (not SQL NULL) for a key explicitly set to JSON null — so
	// that dialect must render a different expression than the extraction
	// used everywhere else. A dialect whose extraction already unifies
	// both cases as SQL NULL (PostgreSQL, SQLite) can just extract and
	// append IS [NOT] NULL as normal.
	JSONIsNull(column string, path []string, not bool) (string, error)

	// DateAdd renders `expr + n unit` as a date/timestamp addition.
	// unit has already been validated against the fixed whitelist
	// (second, minute, hour, day, week, month, year).
	DateAdd(expr, n, unit string) (string, error)

	// DateSub renders `expr - n unit` as a date/timestamp subtraction.
	DateSub(expr, n, unit string) (string, error)

	// Trim renders a TRIM([LEADING|TRAILING|BOTH] [char] FROM target)
	// expression. mode is one of "", "LEADING", "TRAILING", "BOTH"; char is
	// "" when the query gave no trim-character expression (trim whitespace).
	// This is part of Dialect, not rendered generically by the walker,
	// because engines disagree on TRIM's syntax — e.g. SQLite has no
	// FROM-clause form and instead uses TRIM/LTRIM/RTRIM(target[, chars]).
	Trim(mode, char, target string) (string, error)

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
