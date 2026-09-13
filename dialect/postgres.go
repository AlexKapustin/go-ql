package dialect

import (
	"fmt"
	"strings"
)

// Postgres is a Dialect implementation targeting PostgreSQL / JSONB.
type Postgres struct{}

// NewPostgres returns a ready-to-use Postgres dialect.
func NewPostgres() *Postgres {
	return &Postgres{}
}

func (Postgres) QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (p Postgres) QuoteQualified(parts ...string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = p.QuoteIdent(part)
	}
	return strings.Join(quoted, ".")
}

// JSONExtract renders `column #>> '{seg1,seg2}'`, PostgreSQL's operator for
// extracting a nested JSONB value as text in one step regardless of depth.
func (Postgres) JSONExtract(column string, path []string) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("ql/dialect: JSONExtract requires a non-empty path")
	}
	for _, seg := range path {
		if strings.ContainsAny(seg, `{}",\`) {
			return "", fmt.Errorf("ql/dialect: unsupported character in JSON path segment %q", seg)
		}
	}
	return fmt.Sprintf("%s #>> '{%s}'", column, strings.Join(path, ",")), nil
}

// JSONComparableBool quotes the boolean as text: PostgreSQL's #>> always
// returns text, and there is no text = boolean operator.
func (Postgres) JSONComparableBool(v bool) string {
	if v {
		return "'true'"
	}
	return "'false'"
}

// JSONComparableNumber quotes the number as text, for the same reason as
// JSONComparableBool: #>> returns text, and there is no text = numeric
// operator either.
func (Postgres) JSONComparableNumber(n string) string {
	return "'" + n + "'"
}

func (p Postgres) DateAdd(expr, n, unit string) (string, error) {
	if !ValidDateUnits[strings.ToLower(unit)] {
		return "", ErrInvalidDateUnit("DATE_ADD", unit)
	}
	return fmt.Sprintf("(%s + (%s || ' %s')::interval)", expr, n, strings.ToUpper(unit)), nil
}

func (p Postgres) DateSub(expr, n, unit string) (string, error) {
	if !ValidDateUnits[strings.ToLower(unit)] {
		return "", ErrInvalidDateUnit("DATE_SUB", unit)
	}
	return fmt.Sprintf("(%s - (%s || ' %s')::interval)", expr, n, strings.ToUpper(unit)), nil
}

// Trim renders the ANSI-standard TRIM([LEADING|TRAILING|BOTH] [char] FROM
// target) form, which PostgreSQL supports natively.
func (Postgres) Trim(mode, char, target string) (string, error) {
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

func exactly(n int, name string, args []string) error {
	if len(args) != n {
		return fmt.Errorf("ql/dialect: %s() expects %d argument(s), got %d", name, n, len(args))
	}
	return nil
}

func (p Postgres) Functions() map[string]FuncRenderer {
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
			// ZQL: locate(needle, haystack [, startPos]). PostgreSQL has no
			// LOCATE builtin; STRPOS(haystack, needle) is the equivalent
			// without a start-position argument. When a start position is
			// given, search the substring of haystack from that position
			// and add the offset back in.
			if len(args) < 2 || len(args) > 3 {
				return "", fmt.Errorf("ql/dialect: locate() expects 2 or 3 arguments, got %d", len(args))
			}
			needle, haystack := args[0], args[1]
			if len(args) == 2 {
				return fmt.Sprintf("STRPOS(%s, %s)", haystack, needle), nil
			}
			pos := args[2]
			return fmt.Sprintf(
				"(CASE WHEN STRPOS(SUBSTRING(%s FROM (%s)+1), %s) = 0 THEN 0 ELSE STRPOS(SUBSTRING(%s FROM (%s)+1), %s) + (%s) END)",
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
			if err := exactly(2, "mod", args); err != nil {
				return "", err
			}
			return fmt.Sprintf("MOD(%s, %s)", args[0], args[1]), nil
		},
		"date_diff": func(args []string) (string, error) {
			// ZQL: date_diff(date1, date2). PostgreSQL has no DATEDIFF
			// builtin; date subtraction on two date-castable expressions
			// yields the difference in days.
			if err := exactly(2, "date_diff", args); err != nil {
				return "", err
			}
			return fmt.Sprintf("(%s::date - %s::date)", args[0], args[1]), nil
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
