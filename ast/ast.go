// Package ast defines the abstract syntax tree produced by the parser,
// nodes are plain data — there
// is no double-dispatch/visitor method on Node. Walking (SQL generation) is
// done externally via a type switch, which also lets function/CASE/COALESCE
// nodes stay pure grammar without owning any codegen logic.
package ast

// Node is implemented by every AST node type. The unexported method seals
// the interface to this package.
type Node interface {
	node()
}

// WhereClause is the root of a parsed query: WhereClause ::= ConditionalExpression
type WhereClause struct {
	Expr Node // nil for an empty where-clause
}

func (*WhereClause) node() {}

// ConditionalExpression ::= ConditionalTerm {"OR" ConditionalTerm}*
//
// Only constructed when there is more than one term (the parser's "Phase 1"
// optimization collapses a single term to that term directly).
type ConditionalExpression struct {
	Terms []Node
}

func (*ConditionalExpression) node() {}

// ConditionalTerm ::= ConditionalFactor {"AND" ConditionalFactor}*
type ConditionalTerm struct {
	Factors []Node
}

func (*ConditionalTerm) node() {}

// ConditionalFactor ::= ["NOT"] ConditionalPrimary
//
// Only constructed when NOT is present (otherwise the primary is returned
// directly).
type ConditionalFactor struct {
	Not     bool
	Primary Node
}

func (*ConditionalFactor) node() {}

// ConditionalPrimary ::= SimpleConditionalExpression | "(" ConditionalExpression ")"
type ConditionalPrimary struct {
	Simple Node // set when this wraps a simple conditional expression
	Paren  Node // set when this wraps a parenthesized conditional expression
}

func (*ConditionalPrimary) node() {}

// ComparisonExpression ::= ArithmeticExpression ComparisonOperator ArithmeticExpression
type ComparisonExpression struct {
	Left, Right Node
	Operator    string // one of: = < <= <> > >=  (!= is normalized to <>)
}

func (*ComparisonExpression) node() {}

// BetweenExpression ::= ArithmeticExpression ["NOT"] "BETWEEN" ArithmeticExpression "AND" ArithmeticExpression
type BetweenExpression struct {
	Expr, Left, Right Node
	Not               bool
}

func (*BetweenExpression) node() {}

// InExpression ::= ArithmeticExpression ["NOT"] "IN" "(" (InParameter {"," InParameter}*) ")"
//
// Items only ever hold *Literal or *InputParameter (never a sub-expression
// or subquery) — this is a deliberate, preserved injection-safety property
// of the source language.
type InExpression struct {
	Expr  Node
	Items []Node
	Not   bool
}

func (*InExpression) node() {}

// LikeExpression ::= StringExpression ["NOT"] "LIKE" StringPrimary ["ESCAPE" char]
type LikeExpression struct {
	String, Pattern Node
	Escape          *Literal
	Not             bool
}

func (*LikeExpression) node() {}

// NullComparisonExpression ::= Expression "IS" ["NOT"] "NULL"
type NullComparisonExpression struct {
	Expr Node
	Not  bool
}

func (*NullComparisonExpression) node() {}

// Field mapping kinds a PathExpression may resolve to; v1 only supports
// plain state fields (no associations/joins).
const (
	TypeStateField = 8
)

// PathExpression ::= IdentificationVariable "." identifier
type PathExpression struct {
	Alias string // the identification variable, e.g. "product"
	Field string
}

func (*PathExpression) node() {}

// LiteralType identifies the kind of value a Literal holds.
type LiteralType int

const (
	LiteralString LiteralType = iota + 1
	LiteralBoolean
	LiteralNumeric
	LiteralDate
)

// Literal ::= string | integer | float | boolean | date
type Literal struct {
	Type  LiteralType
	Value string // raw textual form; NUMERIC keeps the source spelling, BOOLEAN is "true"/"false", DATE is "YYYY-MM-DD HH:MM:SS" in UTC
}

func (*Literal) node() {}

// InputParameter ::= ":" identifier | "?" [integer]
type InputParameter struct {
	Name     string // parameter name without the leading ':', or the digits after '?' (empty for a bare '?')
	IsNamed  bool
	Position int // for positional parameters: the explicit number after '?', or 0 for a bare '?'
}

func (*InputParameter) node() {}

// ParenthesisExpression ::= "(" SimpleArithmeticExpression ")"
type ParenthesisExpression struct {
	Expr Node
}

func (*ParenthesisExpression) node() {}

// SimpleArithmeticExpression ::= ArithmeticTerm {("+" | "-") ArithmeticTerm}*
//
// Only constructed when there is more than one term. Ops[i] is the operator
// between Terms[i] and Terms[i+1] (len(Ops) == len(Terms)-1).
type SimpleArithmeticExpression struct {
	Terms []Node
	Ops   []string
}

func (*SimpleArithmeticExpression) node() {}

// ArithmeticTerm ::= ArithmeticFactor {("*" | "/") ArithmeticFactor}*
type ArithmeticTerm struct {
	Factors []Node
	Ops     []string
}

func (*ArithmeticTerm) node() {}

// ArithmeticFactor ::= [("+" | "-")] ArithmeticPrimary
//
// Only constructed when a sign is present.
type ArithmeticFactor struct {
	Sign    string // "+" or "-"
	Primary Node
}

func (*ArithmeticFactor) node() {}

// CoalesceExpression ::= "COALESCE" "(" ScalarExpression {"," ScalarExpression}* ")"
type CoalesceExpression struct {
	Exprs []Node
}

func (*CoalesceExpression) node() {}

// NullIfExpression ::= "NULLIF" "(" ScalarExpression "," ScalarExpression ")"
type NullIfExpression struct {
	First, Second Node
}

func (*NullIfExpression) node() {}

// WhenClause ::= "WHEN" ConditionalExpression "THEN" ScalarExpression
type WhenClause struct {
	Cond, Then Node
}

func (*WhenClause) node() {}

// SimpleWhenClause ::= "WHEN" ScalarExpression "THEN" ScalarExpression
type SimpleWhenClause struct {
	When, Then Node
}

func (*SimpleWhenClause) node() {}

// GeneralCaseExpression ::= "CASE" WhenClause {WhenClause}* "ELSE" ScalarExpression "END"
type GeneralCaseExpression struct {
	Whens []*WhenClause
	Else  Node
}

func (*GeneralCaseExpression) node() {}

// SimpleCaseExpression ::= "CASE" PathExpression SimpleWhenClause {SimpleWhenClause}* "ELSE" ScalarExpression "END"
type SimpleCaseExpression struct {
	Operand *PathExpression
	Whens   []*SimpleWhenClause
	Else    Node
}

func (*SimpleCaseExpression) node() {}

// FuncCall is a call to a built-in or custom function whose arguments are a
// simple comma-separated list. It is deliberately plain data — grammar
// (argument parsing) lives entirely in the parser, and codegen lives
// entirely in a dialect's function renderer table
type FuncCall struct {
	Name string // lower-cased function name, e.g. "concat", "lower", "bit_and"
	Args []Node
}

func (*FuncCall) node() {}

// TrimExpression ::= "TRIM" "(" [["LEADING"|"TRAILING"|"BOTH"] [char] "FROM"] StringPrimary ")"
//
// Split out from FuncCall because its argument grammar (an optional
// direction keyword and trim character before FROM) doesn't fit a plain
// comma-separated argument list.
type TrimExpression struct {
	Mode   string // "", "LEADING", "TRAILING", or "BOTH"
	Char   *Literal
	Target Node
}

func (*TrimExpression) node() {}

// DateAddExpression ::= ("DATE_ADD"|"DATE_SUB") "(" ArithmeticPrimary "," ArithmeticPrimary "," StringPrimary ")"
//
// Split out from FuncCall because its unit argument is validated against a
// fixed whitelist and rendered as a dialect-specific INTERVAL expression
// rather than a plain function argument.
type DateAddExpression struct {
	Sub      bool // false = DATE_ADD, true = DATE_SUB
	Date     Node
	Interval Node
	Unit     string // lower-cased: second, minute, hour, day, week, month, year
}

func (*DateAddExpression) node() {}
