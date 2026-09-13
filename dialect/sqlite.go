package dialect

import (
	"fmt"
	"strings"
)

// SQLite is a Dialect implementation targeting SQLite 3, using the built-in
// json_extract() (JSON1) function, date()/datetime() modifiers for date
// arithmetic, and SQLite's own TRIM/LTRIM/RTRIM signature.
//
// sqrt() and mod() render to SQLite's SQRT()/MOD() functions, which require
// SQLite to have been built with SQLITE_ENABLE_MATH_FUNCTIONS. That's the
// default in most modern SQLite builds and Go drivers (e.g.
// modernc.org/sqlite, and mattn/go-sqlite3 with its default build tags) —
// if yours doesn't have it, register a replacement via
// ql.WithCustomFunc("sqrt", ...) / ql.WithCustomFunc("mod", ...), or use the
// "%" operator directly in place of mod(a, b).
type SQLite struct{}

// NewSQLite returns a ready-to-use SQLite dialect.
func NewSQLite() *SQLite {
	return &SQLite{}
}

func (SQLite) QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (s SQLite) QuoteQualified(parts ...string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = s.QuoteIdent(part)
	}
	return strings.Join(quoted, ".")
}

// JSONExtract renders `json_extract(column, '$.seg1.seg2')`, which returns
// the value at that path as a native SQLite value (TEXT/INTEGER/REAL/NULL
// for a scalar, or a JSON string for an object/array) — the SQLite1
// equivalent of PostgreSQL's `#>>`.
func (SQLite) JSONExtract(column string, path []string) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("ql/dialect: JSONExtract requires a non-empty path")
	}
	for _, seg := range path {
		if strings.ContainsAny(seg, `$."'\`) {
			return "", fmt.Errorf("ql/dialect: unsupported character in JSON path segment %q", seg)
		}
	}
	return fmt.Sprintf("json_extract(%s, '$.%s')", column, strings.Join(path, ".")), nil
}

// dateUnitWord maps a validated date unit to the plural keyword SQLite's
// date/datetime modifiers expect. SQLite has no "weeks" modifier, so DateAdd
// and DateSub convert week counts to 7x the day count before calling this.
func dateUnitWord(unit string) string {
	return strings.ToLower(unit) + "s"
}

// dateShift renders `datetime(expr, printf('%+d <unit>', amount))`, an
// explicitly-signed modifier so the shift direction is correct regardless
// of whether the bound amount is itself positive or negative. negate flips
// the sign for DateSub.
func (SQLite) dateShift(expr, n, unit string, negate bool) (string, error) {
	u := strings.ToLower(unit)
	amount := n
	if negate {
		amount = fmt.Sprintf("-(%s)", n)
	}
	word := dateUnitWord(u)
	if u == "week" {
		amount = fmt.Sprintf("(%s) * 7", amount)
		word = "days"
	}
	return fmt.Sprintf("datetime(%s, printf('%%+d %s', %s))", expr, word, amount), nil
}

// JSONComparableBool renders the boolean's normal keyword form unchanged:
// SQLite's json_extract already returns a JSON boolean as the native
// integer 0/1, which compares correctly against TRUE/FALSE (SQLite's own
// aliases for 1/0) with no special handling needed.
func (SQLite) JSONComparableBool(v bool) string {
	if v {
		return "TRUE"
	}
	return "FALSE"
}

// JSONComparableNumber renders n unchanged, for the same reason as
// JSONComparableBool: json_extract already returns a JSON number as a
// native, properly-typed SQLite value.
func (SQLite) JSONComparableNumber(n string) string {
	return n
}

func (s SQLite) DateAdd(expr, n, unit string) (string, error) {
	if !ValidDateUnits[strings.ToLower(unit)] {
		return "", ErrInvalidDateUnit("DATE_ADD", unit)
	}
	return s.dateShift(expr, n, unit, false)
}

func (s SQLite) DateSub(expr, n, unit string) (string, error) {
	if !ValidDateUnits[strings.ToLower(unit)] {
		return "", ErrInvalidDateUnit("DATE_SUB", unit)
	}
	return s.dateShift(expr, n, unit, true)
}

// Trim renders SQLite's TRIM/LTRIM/RTRIM(target[, chars]) form. SQLite has
// no ANSI "TRIM(LEADING x FROM y)" syntax; LEADING/TRAILING map to
// LTRIM/RTRIM, and BOTH (or no mode at all) maps to TRIM.
func (SQLite) Trim(mode, char, target string) (string, error) {
	fn := "TRIM"
	switch strings.ToUpper(mode) {
	case "LEADING":
		fn = "LTRIM"
	case "TRAILING":
		fn = "RTRIM"
	}
	if char != "" {
		return fmt.Sprintf("%s(%s, %s)", fn, target, char), nil
	}
	return fmt.Sprintf("%s(%s)", fn, target), nil
}

func (s SQLite) Functions() map[string]FuncRenderer {
	return map[string]FuncRenderer{
		"concat": func(args []string) (string, error) {
			// SQLite has no CONCAT() builtin; "||" is its string
			// concatenation operator.
			if len(args) < 2 {
				return "", fmt.Errorf("ql/dialect: concat() expects at least 2 arguments, got %d", len(args))
			}
			return "(" + strings.Join(args, " || ") + ")", nil
		},
		"substring": func(args []string) (string, error) {
			if len(args) < 2 || len(args) > 3 {
				return "", fmt.Errorf("ql/dialect: substring() expects 2 or 3 arguments, got %d", len(args))
			}
			return "SUBSTR(" + strings.Join(args, ", ") + ")", nil
		},
		"lower": func(args []string) (string, error) {
			if err := exactly(1, "lower", args); err != nil {
				return "", err
			}
			return "LOWER(" + args[0] + ")", nil
		},
		"upper": func(args []string) (string, error) {
			if err := exactly(1, "upper", args); err != nil {
				return "", err
			}
			return "UPPER(" + args[0] + ")", nil
		},
		"length": func(args []string) (string, error) {
			if err := exactly(1, "length", args); err != nil {
				return "", err
			}
			return "LENGTH(" + args[0] + ")", nil
		},
		"locate": func(args []string) (string, error) {
			// ZQL: locate(needle, haystack [, startPos]). SQLite has no
			// LOCATE builtin; INSTR(haystack, needle) is the equivalent
			// without a start-position argument. When a start position is
			// given, search the substring of haystack from that position
			// and add the offset back in.
			if len(args) < 2 || len(args) > 3 {
				return "", fmt.Errorf("ql/dialect: locate() expects 2 or 3 arguments, got %d", len(args))
			}
			needle, haystack := args[0], args[1]
			if len(args) == 2 {
				return fmt.Sprintf("INSTR(%s, %s)", haystack, needle), nil
			}
			pos := args[2]
			return fmt.Sprintf(
				"(CASE WHEN INSTR(SUBSTR(%s, (%s)+1), %s) = 0 THEN 0 ELSE INSTR(SUBSTR(%s, (%s)+1), %s) + (%s) END)",
				haystack, pos, needle, haystack, pos, needle, pos,
			), nil
		},
		"abs": func(args []string) (string, error) {
			if err := exactly(1, "abs", args); err != nil {
				return "", err
			}
			return "ABS(" + args[0] + ")", nil
		},
		"sqrt": func(args []string) (string, error) {
			if err := exactly(1, "sqrt", args); err != nil {
				return "", err
			}
			return "SQRT(" + args[0] + ")", nil
		},
		"mod": func(args []string) (string, error) {
			// SQLite's "%" is a core operator, unlike MOD()/sqrt(), so this
			// one needs no optional extension.
			if err := exactly(2, "mod", args); err != nil {
				return "", err
			}
			return fmt.Sprintf("(%s %% %s)", args[0], args[1]), nil
		},
		"date_diff": func(args []string) (string, error) {
			// ZQL: date_diff(date1, date2). SQLite has no DATEDIFF builtin;
			// the difference in Julian day numbers gives the difference in
			// days between the two date-ish expressions.
			if err := exactly(2, "date_diff", args); err != nil {
				return "", err
			}
			return fmt.Sprintf("CAST(julianday(%s) - julianday(%s) AS INTEGER)", args[0], args[1]), nil
		},
		"bit_and": func(args []string) (string, error) {
			if err := exactly(2, "bit_and", args); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s & %s", args[0], args[1]), nil
		},
		"bit_or": func(args []string) (string, error) {
			if err := exactly(2, "bit_or", args); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s | %s", args[0], args[1]), nil
		},
		"current_date": func(args []string) (string, error) {
			if err := exactly(0, "current_date", args); err != nil {
				return "", err
			}
			return "CURRENT_DATE", nil
		},
		"current_time": func(args []string) (string, error) {
			if err := exactly(0, "current_time", args); err != nil {
				return "", err
			}
			return "CURRENT_TIME", nil
		},
		"current_timestamp": func(args []string) (string, error) {
			if err := exactly(0, "current_timestamp", args); err != nil {
				return "", err
			}
			return "CURRENT_TIMESTAMP", nil
		},
	}
}
