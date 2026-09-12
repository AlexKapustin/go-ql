package parser

import "fmt"

// SyntaxError reports a token that didn't match what the grammar expected.
type SyntaxError struct {
	Position int
	Expected string
	Got      string
}

func (e *SyntaxError) Error() string {
	if e.Expected == "" {
		return fmt.Sprintf("[Syntax Error] col %d: unexpected %s", e.Position, e.Got)
	}
	return fmt.Sprintf("[Syntax Error] col %d: expected %s, got %s", e.Position, e.Expected, e.Got)
}

// SemanticError reports a grammatically valid query that is invalid for
// other reasons: an unknown alias, an unwhitelisted field, an undeclared
// parameter, a reserved parameter name, or an invalid function argument.
type SemanticError struct {
	Position int
	Message  string
}

func (e *SemanticError) Error() string {
	if e.Position < 0 {
		return fmt.Sprintf("[Semantic Error] %s", e.Message)
	}
	return fmt.Sprintf("[Semantic Error] col %d: %s", e.Position, e.Message)
}
