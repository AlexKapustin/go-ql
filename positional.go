package ql

import (
	"fmt"
	"regexp"
)

var placeholderRe = regexp.MustCompile(`@[A-Za-z_][A-Za-z0-9_]*`)

// rewriteToPositional replaces each @name placeholder in sql with $1, $2, ...
// in first-occurrence order, returning the rewritten SQL and the matching
// positional argument slice drawn from args.
func rewriteToPositional(sql string, args map[string]any) (string, []any) {
	order := make(map[string]int)
	var positional []any

	rewritten := placeholderRe.ReplaceAllStringFunc(sql, func(match string) string {
		name := match[1:]
		idx, ok := order[name]
		if !ok {
			positional = append(positional, args[name])
			idx = len(positional)
			order[name] = idx
		}
		return fmt.Sprintf("$%d", idx)
	})

	return rewritten, positional
}
