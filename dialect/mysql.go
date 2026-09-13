package dialect

import (
	"fmt"
	"strings"
)

// MySQL is a Dialect implementation targeting MySQL 8.0 and above. It uses
// backtick-quoted identifiers, the `->>` JSON path operator (available
// since MySQL 5.7.13, unaffected by the 8.0 floor here), native
// DATE_ADD/DATE_SUB/DATEDIFF, and ANSI TRIM syntax.
//
// The MySQL 8.0-and-above floor doesn't currently change any rendering
// choice below — everything used here has been present since 5.7 — but is
// documented as the supported floor for clarity, and to leave room for a
// version-gated feature (e.g. a construct only valid from 8.0) to be added
// without a breaking API change.
type MySQL struct{}

// NewMySQL returns a ready-to-use MySQL dialect.
func NewMySQL() *MySQL {
	return &MySQL{}
}

func (MySQL) QuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func (m MySQL) QuoteQualified(parts ...string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = m.QuoteIdent(part)
	}
	return strings.Join(quoted, ".")
}

// JSONExtract renders `column->>'$."seg1"."seg2"'`, MySQL's operator for
// extracting a nested JSON value as unquoted text in one step. Each path
// segment is double-quoted within the JSON path expression so a segment
// containing characters that would otherwise be path syntax (e.g. a literal
// ".") is still addressed as a single key, matching the reference PHP
// implementation this library ports.
func (MySQL) JSONExtract(column string, path []string) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("ql/dialect: JSONExtract requires a non-empty path")
	}
	quoted := make([]string, len(path))
	for i, seg := range path {
		if strings.ContainsAny(seg, `$."'\`) {
			return "", fmt.Errorf("ql/dialect: unsupported character in JSON path segment %q", seg)
		}
		quoted[i] = `"` + seg + `"`
	}
	return fmt.Sprintf(`%s->>'$.%s'`, column, strings.Join(quoted, ".")), nil
}

// JSONComparableBool quotes the boolean as text: MySQL's ->> always returns
// text, and while MySQL coerces a numeric-looking string to a number for
// comparison, both "true" and "false" coerce to 0, silently equating them.
func (MySQL) JSONComparableBool(v bool) string {
	if v {
		return "'true'"
	}
	return "'false'"
}

// JSONComparableNumber renders n unchanged: MySQL coerces the text ->>
// returns back to a number for comparison against a numeric literal, so no
// special handling is needed here (unlike JSONComparableBool).
func (MySQL) JSONComparableNumber(n string) string {
	return n
}

func (MySQL) DateAdd(expr, n, unit string) (string, error) {
	if !ValidDateUnits[strings.ToLower(unit)] {
		return "", ErrInvalidDateUnit("DATE_ADD", unit)
	}
	return fmt.Sprintf("DATE_ADD(%s, INTERVAL %s %s)", expr, n, strings.ToUpper(unit)), nil
}

func (MySQL) DateSub(expr, n, unit string) (string, error) {
	if !ValidDateUnits[strings.ToLower(unit)] {
		return "", ErrInvalidDateUnit("DATE_SUB", unit)
	}
	return fmt.Sprintf("DATE_SUB(%s, INTERVAL %s %s)", expr, n, strings.ToUpper(unit)), nil
}

// Trim renders the ANSI-standard TRIM([LEADING|TRAILING|BOTH] [char] FROM
// target) form, which MySQL supports natively.
func (MySQL) Trim(mode, char, target string) (string, error) {
	switch {
	case mode != "" && char != "":
		return fmt.Sprintf("TRIM(%s %s FROM %s)", mode, char, target), nil
	case mode != "":
		return fmt.Sprintf("TRIM(%s FROM %s)", mode, target), nil
	case char != "":
		return fmt.Sprintf("TRIM(%s FROM %s)", char, target), nil
	default:
		return "TRIM(" + target + ")", nil
	}
}

func (m MySQL) Functions() map[string]FuncRenderer {
	return map[string]FuncRenderer{
		"concat": func(args []string) (string, error) {
			if len(args) < 2 {
				return "", fmt.Errorf("ql/dialect: concat() expects at least 2 arguments, got %d", len(args))
			}
			return "CONCAT(" + strings.Join(args, ", ") + ")", nil
		},
		"substring": func(args []string) (string, error) {
			if len(args) < 2 || len(args) > 3 {
				return "", fmt.Errorf("ql/dialect: substring() expects 2 or 3 arguments, got %d", len(args))
			}
			return "SUBSTRING(" + strings.Join(args, ", ") + ")", nil
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
			// ZQL: locate(needle, haystack [, startPos]). MySQL's native
			// LOCATE(substr, str[, pos]) takes its arguments in this exact
			// order, so no rewriting is needed here (unlike the Postgres
			// and SQLite dialects, which have no 3-argument equivalent).
			if len(args) < 2 || len(args) > 3 {
				return "", fmt.Errorf("ql/dialect: locate() expects 2 or 3 arguments, got %d", len(args))
			}
			return "LOCATE(" + strings.Join(args, ", ") + ")", nil
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
			if err := exactly(2, "mod", args); err != nil {
				return "", err
			}
			return fmt.Sprintf("MOD(%s, %s)", args[0], args[1]), nil
		},
		"date_diff": func(args []string) (string, error) {
			// ZQL: date_diff(date1, date2). MySQL's native DATEDIFF(a, b)
			// returns a - b in whole days.
			if err := exactly(2, "date_diff", args); err != nil {
				return "", err
			}
			return fmt.Sprintf("DATEDIFF(%s, %s)", args[0], args[1]), nil
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
			return "CURRENT_DATE()", nil
		},
		"current_time": func(args []string) (string, error) {
			if err := exactly(0, "current_time", args); err != nil {
				return "", err
			}
			return "CURRENT_TIME()", nil
		},
		"current_timestamp": func(args []string) (string, error) {
			if err := exactly(0, "current_timestamp", args); err != nil {
				return "", err
			}
			return "CURRENT_TIMESTAMP()", nil
		},
	}
}
