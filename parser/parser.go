// Package parser implements a recursive-descent parser for the query
//
// Precedence is baked into the call hierarchy
// version: conditionalExpression(OR) -> conditionalTerm(AND) ->
// conditionalFactor(NOT) -> conditionalPrimary -> simpleConditionalExpression
// -> comparisonExpression -> arithmetic chain. Each level applies the same
// "Phase 1" AST-collapsing optimization: a wrapper node (e.g.
// ConditionalExpression) is only constructed when it actually has more than
// one child: a single child is returned directly.
//
// Field-path and input-parameter validation is deferred: Parse collects
// every path expression and parameter reference it encounters, and only
// validates them against the supplied schema.Components / parameter map
// after a full, syntactically valid parse succeeds deferred-validation pattern.
package parser

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AlexKapustin/go-ql/ast"
	"github.com/AlexKapustin/go-ql/lexer"
	"github.com/AlexKapustin/go-ql/schema"
	"github.com/AlexKapustin/go-ql/token"
)

// reservedParamPrefix is disallowed as the start of a user-supplied named
// parameter, so it can never collide with the walker's synthesized
// placeholder names (which all use this same prefix).
const reservedParamPrefix = "ql_"

type deferredPath struct {
	expr *ast.PathExpression
	tok  *token.Token
}

type deferredParam struct {
	key string // ":name" for named parameters, or the digits for positional ones
	tok *token.Token
}

// Parser parses a single query-language expression string into an AST,
// validating field paths against components and parameter references
// against params.
type Parser struct {
	lx         *lexer.Lexer
	components schema.Components
	params     map[string]any
	tz         *time.Location

	deferredPaths  []deferredPath
	deferredParams []deferredParam
}

// New returns a Parser ready to parse input. components is the alias ->
// table-schema registry used to validate field paths; params is the set of
// externally supplied parameter values used to validate :name/?N
// references; tz is the timezone date literals are interpreted in before
// being normalized to UTC (defaults to UTC if nil).
func New(input string, components schema.Components, params map[string]any, tz *time.Location) (*Parser, error) {
	lx, err := lexer.New(input)
	if err != nil {
		return nil, err
	}
	if tz == nil {
		tz = time.UTC
	}
	return &Parser{lx: lx, components: components, params: params, tz: tz}, nil
}

// Parse parses the whole input as a WHERE-clause expression, validates all
// deferred field-path and parameter references, and returns the resulting
// AST.
func (p *Parser) Parse() (*ast.WhereClause, error) {
	p.lx.MoveNext()

	expr, err := p.conditionalExpression()
	if err != nil {
		return nil, err
	}
	if p.lx.Lookahead != nil {
		return nil, p.syntaxError("end of string")
	}
	if err := p.validateDeferred(); err != nil {
		return nil, err
	}
	return &ast.WhereClause{Expr: expr}, nil
}

func (p *Parser) validateDeferred() error {
	for _, d := range p.deferredPaths {
		meta, ok := p.components[d.expr.Alias]
		if !ok {
			return p.semanticErrorAt(fmt.Sprintf("'%s' is not defined.", d.expr.Alias), d.tok)
		}
		if _, ok := meta.Fields()[d.expr.Field]; !ok {
			return p.semanticErrorAt(
				fmt.Sprintf("Class %s has no field or association named %s", d.expr.Alias, d.expr.Field),
				d.tok,
			)
		}
	}
	for _, d := range p.deferredParams {
		if _, ok := p.params[d.key]; !ok {
			return p.semanticErrorAt(fmt.Sprintf("'%s' is not defined.", d.key), d.tok)
		}
	}
	return nil
}

// --- low-level helpers -----------------------------------------------------

func kindOf(t *token.Token) token.Kind {
	if t == nil {
		return 0
	}
	return t.Kind
}

func valueOf(t *token.Token) string {
	if t == nil {
		return ""
	}
	return t.Value
}

func isMathOperator(t *token.Token) bool {
	switch kindOf(t) {
	case token.PLUS, token.MINUS, token.DIVIDE, token.MULTIPLY:
		return true
	}
	return false
}

func isComparisonOpStart(t *token.Token) bool {
	switch kindOf(t) {
	case token.EQUALS, token.LOWER_THAN, token.GREATER_THAN, token.NEGATE:
		return true
	}
	return false
}

// match consumes Lookahead if it has kind k, or if k is IDENTIFIER and
// Lookahead is any keyword (keywords double as identifiers), and otherwise
// returns a syntax error.
func (p *Parser) match(k token.Kind) error {
	la := p.lx.Lookahead
	if la != nil && la.Kind == k {
		p.lx.MoveNext()
		return nil
	}
	if k == token.IDENTIFIER && la != nil && la.Kind > token.IDENTIFIER {
		p.lx.MoveNext()
		return nil
	}
	return p.syntaxError(token.Name(k))
}

func (p *Parser) syntaxError(expected string) error {
	la := p.lx.Lookahead
	if la == nil {
		return &SyntaxError{Position: -1, Expected: expected, Got: "end of string"}
	}
	return &SyntaxError{Position: la.Position, Expected: expected, Got: fmt.Sprintf("%q", la.Value)}
}

func (p *Parser) semanticErrorAt(msg string, tok *token.Token) error {
	pos := -1
	if tok != nil {
		pos = tok.Position
	}
	return &SemanticError{Position: pos, Message: msg}
}

// peekBeyondClosingParenthesis assumes the peek cursor is positioned just
// inside one level of parenthesis nesting (i.e. right after an already-peeked
// "(") and returns the first token after the matching ")". When resetPeek is
// true, the peek cursor is rewound back to just after Lookahead afterward.
func (p *Parser) peekBeyondClosingParenthesis(resetPeek bool) *token.Token {
	t := p.lx.Peek()
	unmatched := 1
	for unmatched > 0 && t != nil {
		switch t.Kind {
		case token.OPEN_PARENTHESIS:
			unmatched++
		case token.CLOSE_PARENTHESIS:
			unmatched--
		}
		t = p.lx.Peek()
	}
	if resetPeek {
		p.lx.ResetPeek()
	}
	return t
}

// isFunction reports whether Lookahead starts a function call: an
// identifier-or-keyword immediately followed by "(".
func (p *Parser) isFunction() bool {
	la := p.lx.Lookahead
	if la == nil || la.Kind < token.IDENTIFIER {
		return false
	}
	peek := p.lx.Peek()
	p.lx.ResetPeek()
	return peek != nil && peek.Kind == token.OPEN_PARENTHESIS
}

func (p *Parser) normalizeDate(v string) (string, error) {
	layouts := []string{"2006-01-02 15:04:05", "2006-01-02"}
	var t time.Time
	var err error
	for _, layout := range layouts {
		t, err = time.ParseInLocation(layout, v, p.tz)
		if err == nil {
			break
		}
	}
	if err != nil {
		return "", err
	}
	return t.UTC().Format("2006-01-02 15:04:05"), nil
}

// --- identifiers & path expressions -----------------------------------------

func (p *Parser) identificationVariable() (string, *token.Token, error) {
	if err := p.match(token.IDENTIFIER); err != nil {
		return "", nil, err
	}
	tok := p.lx.Token
	return tok.Value, tok, nil
}

// pathExpression ::= IdentificationVariable {"." identifier}*
func (p *Parser) pathExpression() (*ast.PathExpression, error) {
	alias, aliasTok, err := p.identificationVariable()
	if err != nil {
		return nil, err
	}

	var field string
	if p.lx.IsNextToken(token.DOT) {
		if err := p.match(token.DOT); err != nil {
			return nil, err
		}
		if err := p.match(token.IDENTIFIER); err != nil {
			return nil, err
		}
		field = p.lx.Token.Value

		for p.lx.IsNextToken(token.DOT) {
			if err := p.match(token.DOT); err != nil {
				return nil, err
			}
			if err := p.match(token.IDENTIFIER); err != nil {
				return nil, err
			}
			field += "." + p.lx.Token.Value
		}
	}

	pe := &ast.PathExpression{Alias: alias, Field: field}
	p.deferredPaths = append(p.deferredPaths, deferredPath{expr: pe, tok: aliasTok})
	return pe, nil
}

// --- literals & parameters ---------------------------------------------------

func (p *Parser) literal() (*ast.Literal, error) {
	la := p.lx.Lookahead
	switch kindOf(la) {
	case token.STRING:
		v := la.Value
		if err := p.match(token.STRING); err != nil {
			return nil, err
		}
		return &ast.Literal{Type: ast.LiteralString, Value: v}, nil

	case token.INTEGER:
		v := la.Value
		if err := p.match(token.INTEGER); err != nil {
			return nil, err
		}
		return &ast.Literal{Type: ast.LiteralNumeric, Value: v}, nil

	case token.FLOAT:
		v := la.Value
		if err := p.match(token.FLOAT); err != nil {
			return nil, err
		}
		return &ast.Literal{Type: ast.LiteralNumeric, Value: v}, nil

	case token.TRUE:
		if err := p.match(token.TRUE); err != nil {
			return nil, err
		}
		return &ast.Literal{Type: ast.LiteralBoolean, Value: "true"}, nil

	case token.FALSE:
		if err := p.match(token.FALSE); err != nil {
			return nil, err
		}
		return &ast.Literal{Type: ast.LiteralBoolean, Value: "false"}, nil

	case token.DATE:
		v := la.Value
		if err := p.match(token.DATE); err != nil {
			return nil, err
		}
		normalized, err := p.normalizeDate(v)
		if err != nil {
			// fall back to the raw value on a parse failure
			// rather than erroring the whole query.
			return &ast.Literal{Type: ast.LiteralDate, Value: v}, nil
		}
		return &ast.Literal{Type: ast.LiteralDate, Value: normalized}, nil

	default:
		return nil, p.syntaxError("Literal")
	}
}

// inParameter ::= Literal | InputParameter
func (p *Parser) inParameter() (ast.Node, error) {
	if p.lx.IsNextToken(token.INPUT_PARAMETER) {
		return p.inputParameter()
	}
	return p.literal()
}

// paramKey is the key a value for ip must be supplied under in the params
// map, for both named (":foo" -> "foo") and positional ("?2" -> "2")
// parameters — deliberately without the ":" prefix, so WithParams' map
// keys read naturally as plain Go identifiers/numbers.
func paramKey(ip *ast.InputParameter) string {
	return ip.Name
}

// inputParameter ::= ":" identifier | "?" digit+
func (p *Parser) inputParameter() (*ast.InputParameter, error) {
	if err := p.match(token.INPUT_PARAMETER); err != nil {
		return nil, err
	}
	tok := p.lx.Token
	raw := tok.Value

	if len(raw) <= 1 {
		return nil, p.semanticErrorAt(
			fmt.Sprintf("Invalid parameter format, %s given, but :<name> or ?<num> expected.", raw),
			tok,
		)
	}

	body := raw[1:]
	ip := &ast.InputParameter{}

	if raw[0] == ':' {
		ip.IsNamed = true
		ip.Name = body
		if strings.HasPrefix(strings.ToLower(body), reservedParamPrefix) {
			return nil, p.semanticErrorAt(
				fmt.Sprintf("parameter name %q uses the reserved prefix %q", body, reservedParamPrefix),
				tok,
			)
		}
	} else {
		n, _ := strconv.Atoi(body)
		ip.IsNamed = false
		ip.Position = n
		ip.Name = body
	}

	p.deferredParams = append(p.deferredParams, deferredParam{key: paramKey(ip), tok: tok})
	return ip, nil
}

// --- arithmetic ---------------------------------------------------------------

func (p *Parser) arithmeticExpression() (ast.Node, error) {
	return p.simpleArithmeticExpression()
}

// simpleArithmeticExpression ::= ArithmeticTerm {("+" | "-") ArithmeticTerm}*
func (p *Parser) simpleArithmeticExpression() (ast.Node, error) {
	var terms []ast.Node
	var ops []string

	t, err := p.arithmeticTerm()
	if err != nil {
		return nil, err
	}
	terms = append(terms, t)

	for p.lx.IsNextToken(token.PLUS) || p.lx.IsNextToken(token.MINUS) {
		op := "+"
		if p.lx.IsNextToken(token.PLUS) {
			if err := p.match(token.PLUS); err != nil {
				return nil, err
			}
		} else {
			if err := p.match(token.MINUS); err != nil {
				return nil, err
			}
			op = "-"
		}
		ops = append(ops, op)

		t, err := p.arithmeticTerm()
		if err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}

	if len(terms) == 1 {
		return terms[0], nil
	}
	return &ast.SimpleArithmeticExpression{Terms: terms, Ops: ops}, nil
}

// arithmeticTerm ::= ArithmeticFactor {("*" | "/") ArithmeticFactor}*
func (p *Parser) arithmeticTerm() (ast.Node, error) {
	var factors []ast.Node
	var ops []string

	f, err := p.arithmeticFactor()
	if err != nil {
		return nil, err
	}
	factors = append(factors, f)

	for p.lx.IsNextToken(token.MULTIPLY) || p.lx.IsNextToken(token.DIVIDE) {
		op := "*"
		if p.lx.IsNextToken(token.MULTIPLY) {
			if err := p.match(token.MULTIPLY); err != nil {
				return nil, err
			}
		} else {
			if err := p.match(token.DIVIDE); err != nil {
				return nil, err
			}
			op = "/"
		}
		ops = append(ops, op)

		f, err := p.arithmeticFactor()
		if err != nil {
			return nil, err
		}
		factors = append(factors, f)
	}

	if len(factors) == 1 {
		return factors[0], nil
	}
	return &ast.ArithmeticTerm{Factors: factors, Ops: ops}, nil
}

// arithmeticFactor ::= [("+" | "-")] ArithmeticPrimary
func (p *Parser) arithmeticFactor() (ast.Node, error) {
	sign := ""
	if p.lx.IsNextToken(token.PLUS) || p.lx.IsNextToken(token.MINUS) {
		if p.lx.IsNextToken(token.PLUS) {
			if err := p.match(token.PLUS); err != nil {
				return nil, err
			}
			sign = "+"
		} else {
			if err := p.match(token.MINUS); err != nil {
				return nil, err
			}
			sign = "-"
		}
	}

	primary, err := p.arithmeticPrimary()
	if err != nil {
		return nil, err
	}
	if sign == "" {
		return primary, nil
	}
	return &ast.ArithmeticFactor{Sign: sign, Primary: primary}, nil
}

// arithmeticPrimary ::= "(" SimpleArithmeticExpression ")" | Literal |
//
//	PathExpression | FunctionDeclaration | InputParameter | CaseExpression
func (p *Parser) arithmeticPrimary() (ast.Node, error) {
	if p.lx.IsNextToken(token.OPEN_PARENTHESIS) {
		if err := p.match(token.OPEN_PARENTHESIS); err != nil {
			return nil, err
		}
		expr, err := p.simpleArithmeticExpression()
		if err != nil {
			return nil, err
		}
		if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
			return nil, err
		}
		return &ast.ParenthesisExpression{Expr: expr}, nil
	}

	la := p.lx.Lookahead
	switch kindOf(la) {
	case token.COALESCE, token.NULLIF, token.CASE:
		return p.caseExpression()

	case token.IDENTIFIER:
		peek := p.lx.Glimpse()
		if valueOf(peek) == "(" {
			return p.functionDeclaration()
		}
		return p.pathExpression()

	case token.INPUT_PARAMETER:
		return p.inputParameter()

	default:
		peek := p.lx.Glimpse()
		if valueOf(peek) == "(" {
			return p.functionDeclaration()
		}
		return p.literal()
	}
}

// --- strings --------------------------------------------------------------

func (p *Parser) stringExpression() (ast.Node, error) {
	return p.stringPrimary()
}

// stringPrimary ::= StateFieldPathExpression | string | InputParameter |
//
//	FunctionsReturningStrings | CaseExpression
func (p *Parser) stringPrimary() (ast.Node, error) {
	la := p.lx.Lookahead
	switch kindOf(la) {
	case token.IDENTIFIER:
		peek := p.lx.Glimpse()
		if valueOf(peek) == "." {
			return p.pathExpression()
		}
		if valueOf(peek) == "(" {
			return p.functionDeclaration()
		}
		return nil, p.syntaxError("'.' or '('")

	case token.STRING:
		v := la.Value
		if err := p.match(token.STRING); err != nil {
			return nil, err
		}
		return &ast.Literal{Type: ast.LiteralString, Value: v}, nil

	case token.INPUT_PARAMETER:
		return p.inputParameter()

	case token.CASE, token.COALESCE, token.NULLIF:
		return p.caseExpression()
	}

	return nil, p.syntaxError("StateFieldPathExpression | string | InputParameter | FunctionsReturningStrings")
}

// --- scalar / case expressions ---------------------------------------------

func (p *Parser) scalarExpression() (ast.Node, error) {
	la := p.lx.Lookahead
	peek := p.lx.Glimpse()

	switch kindOf(la) {
	case token.INTEGER, token.FLOAT, token.MINUS, token.PLUS, token.OPEN_PARENTHESIS:
		return p.simpleArithmeticExpression()

	case token.STRING:
		return p.stringPrimary()

	case token.TRUE:
		if err := p.match(token.TRUE); err != nil {
			return nil, err
		}
		return &ast.Literal{Type: ast.LiteralBoolean, Value: "true"}, nil

	case token.FALSE:
		if err := p.match(token.FALSE); err != nil {
			return nil, err
		}
		return &ast.Literal{Type: ast.LiteralBoolean, Value: "false"}, nil

	case token.INPUT_PARAMETER:
		if isMathOperator(peek) {
			return p.simpleArithmeticExpression()
		}
		return p.inputParameter()

	case token.CASE, token.COALESCE, token.NULLIF:
		return p.caseExpression()
	}

	if p.isFunction() {
		p.lx.Peek() // "("
		beyond := p.peekBeyondClosingParenthesis(true)
		if isMathOperator(beyond) {
			return p.simpleArithmeticExpression()
		}
		return p.functionDeclaration()
	}

	if kindOf(la) == token.IDENTIFIER {
		p.lx.Peek() // '.'
		p.lx.Peek() // token after '.'
		peek2 := p.lx.Peek()
		p.lx.ResetPeek()
		if isMathOperator(peek2) {
			return p.simpleArithmeticExpression()
		}
		return p.pathExpression()
	}

	return nil, p.syntaxError("")
}

func (p *Parser) caseExpression() (ast.Node, error) {
	la := p.lx.Lookahead
	switch kindOf(la) {
	case token.NULLIF:
		return p.nullIfExpression()
	case token.COALESCE:
		return p.coalesceExpression()
	case token.CASE:
		p.lx.ResetPeek()
		peek := p.lx.Peek()
		p.lx.ResetPeek()
		if kindOf(peek) == token.WHEN {
			return p.generalCaseExpression()
		}
		return p.simpleCaseExpression()
	}
	return nil, p.syntaxError("")
}

func (p *Parser) coalesceExpression() (*ast.CoalesceExpression, error) {
	if err := p.match(token.COALESCE); err != nil {
		return nil, err
	}
	if err := p.match(token.OPEN_PARENTHESIS); err != nil {
		return nil, err
	}

	var exprs []ast.Node
	e, err := p.scalarExpression()
	if err != nil {
		return nil, err
	}
	exprs = append(exprs, e)

	for p.lx.IsNextToken(token.COMMA) {
		if err := p.match(token.COMMA); err != nil {
			return nil, err
		}
		e, err := p.scalarExpression()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, e)
	}

	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return &ast.CoalesceExpression{Exprs: exprs}, nil
}

func (p *Parser) nullIfExpression() (*ast.NullIfExpression, error) {
	if err := p.match(token.NULLIF); err != nil {
		return nil, err
	}
	if err := p.match(token.OPEN_PARENTHESIS); err != nil {
		return nil, err
	}
	first, err := p.scalarExpression()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.COMMA); err != nil {
		return nil, err
	}
	second, err := p.scalarExpression()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return &ast.NullIfExpression{First: first, Second: second}, nil
}

func (p *Parser) generalCaseExpression() (*ast.GeneralCaseExpression, error) {
	if err := p.match(token.CASE); err != nil {
		return nil, err
	}

	var whens []*ast.WhenClause
	for {
		w, err := p.whenClause()
		if err != nil {
			return nil, err
		}
		whens = append(whens, w)
		if !p.lx.IsNextToken(token.WHEN) {
			break
		}
	}

	if err := p.match(token.ELSE); err != nil {
		return nil, err
	}
	elseExpr, err := p.scalarExpression()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.END); err != nil {
		return nil, err
	}
	return &ast.GeneralCaseExpression{Whens: whens, Else: elseExpr}, nil
}

func (p *Parser) whenClause() (*ast.WhenClause, error) {
	if err := p.match(token.WHEN); err != nil {
		return nil, err
	}
	cond, err := p.conditionalExpression()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.THEN); err != nil {
		return nil, err
	}
	then, err := p.scalarExpression()
	if err != nil {
		return nil, err
	}
	return &ast.WhenClause{Cond: cond, Then: then}, nil
}

func (p *Parser) simpleCaseExpression() (*ast.SimpleCaseExpression, error) {
	if err := p.match(token.CASE); err != nil {
		return nil, err
	}
	operand, err := p.pathExpression()
	if err != nil {
		return nil, err
	}

	var whens []*ast.SimpleWhenClause
	for {
		w, err := p.simpleWhenClause()
		if err != nil {
			return nil, err
		}
		whens = append(whens, w)
		if !p.lx.IsNextToken(token.WHEN) {
			break
		}
	}

	if err := p.match(token.ELSE); err != nil {
		return nil, err
	}
	elseExpr, err := p.scalarExpression()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.END); err != nil {
		return nil, err
	}
	return &ast.SimpleCaseExpression{Operand: operand, Whens: whens, Else: elseExpr}, nil
}

func (p *Parser) simpleWhenClause() (*ast.SimpleWhenClause, error) {
	if err := p.match(token.WHEN); err != nil {
		return nil, err
	}
	when, err := p.scalarExpression()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.THEN); err != nil {
		return nil, err
	}
	then, err := p.scalarExpression()
	if err != nil {
		return nil, err
	}
	return &ast.SimpleWhenClause{When: when, Then: then}, nil
}

// --- conditional grammar ----------------------------------------------------

// conditionalExpression ::= ConditionalTerm {"OR" ConditionalTerm}*
func (p *Parser) conditionalExpression() (ast.Node, error) {
	var terms []ast.Node
	t, err := p.conditionalTerm()
	if err != nil {
		return nil, err
	}
	terms = append(terms, t)

	for p.lx.IsNextToken(token.OR) {
		if err := p.match(token.OR); err != nil {
			return nil, err
		}
		t, err := p.conditionalTerm()
		if err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}

	if len(terms) == 1 {
		return terms[0], nil
	}
	return &ast.ConditionalExpression{Terms: terms}, nil
}

// conditionalTerm ::= ConditionalFactor {"AND" ConditionalFactor}*
func (p *Parser) conditionalTerm() (ast.Node, error) {
	var factors []ast.Node
	f, err := p.conditionalFactor()
	if err != nil {
		return nil, err
	}
	factors = append(factors, f)

	for p.lx.IsNextToken(token.AND) {
		if err := p.match(token.AND); err != nil {
			return nil, err
		}
		f, err := p.conditionalFactor()
		if err != nil {
			return nil, err
		}
		factors = append(factors, f)
	}

	if len(factors) == 1 {
		return factors[0], nil
	}
	return &ast.ConditionalTerm{Factors: factors}, nil
}

// conditionalFactor ::= ["NOT"] ConditionalPrimary
func (p *Parser) conditionalFactor() (ast.Node, error) {
	not := false
	if p.lx.IsNextToken(token.NOT) {
		if err := p.match(token.NOT); err != nil {
			return nil, err
		}
		not = true
	}

	primary, err := p.conditionalPrimary()
	if err != nil {
		return nil, err
	}
	if !not {
		return primary, nil
	}
	return &ast.ConditionalFactor{Not: true, Primary: primary}, nil
}

// conditionalPrimary ::= SimpleConditionalExpression | "(" ConditionalExpression ")"
func (p *Parser) conditionalPrimary() (*ast.ConditionalPrimary, error) {
	if !p.lx.IsNextToken(token.OPEN_PARENTHESIS) {
		simple, err := p.simpleConditionalExpression()
		if err != nil {
			return nil, err
		}
		return &ast.ConditionalPrimary{Simple: simple}, nil
	}

	peek := p.peekBeyondClosingParenthesis(true)
	if peek != nil && (isComparisonOpStart(peek) ||
		peek.Kind == token.NOT || peek.Kind == token.BETWEEN || peek.Kind == token.LIKE ||
		peek.Kind == token.IN || peek.Kind == token.IS || isMathOperator(peek)) {
		simple, err := p.simpleConditionalExpression()
		if err != nil {
			return nil, err
		}
		return &ast.ConditionalPrimary{Simple: simple}, nil
	}

	if err := p.match(token.OPEN_PARENTHESIS); err != nil {
		return nil, err
	}
	expr, err := p.conditionalExpression()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return &ast.ConditionalPrimary{Paren: expr}, nil
}

// simpleConditionalExpression ::= ComparisonExpression | BetweenExpression |
//
//	LikeExpression | InExpression | NullComparisonExpression
func (p *Parser) simpleConditionalExpression() (ast.Node, error) {
	tok := p.lx.Lookahead
	peek := p.lx.Glimpse()
	lookahead := tok

	if p.lx.IsNextToken(token.NOT) {
		tok = p.lx.Glimpse()
	}

	if kindOf(tok) == token.IDENTIFIER || kindOf(tok) == token.INPUT_PARAMETER || p.isFunction() {
		beyond := p.lx.Peek()

		if valueOf(peek) == "(" {
			tok = p.peekBeyondClosingParenthesis(false)
			if kindOf(tok) == token.NOT {
				tok = p.lx.Peek()
			}
			if kindOf(tok) == token.IS {
				lookahead = p.lx.Peek()
			}
		} else {
			tok = beyond
			for valueOf(tok) == "." {
				p.lx.Peek()
				tok = p.lx.Peek()
			}
			if kindOf(tok) == token.NOT {
				tok = p.lx.Peek()
			}
			lookahead = p.lx.Peek()
		}

		if kindOf(lookahead) == token.NOT {
			lookahead = p.lx.Peek()
		}

		p.lx.ResetPeek()
	}

	switch kindOf(tok) {
	case token.BETWEEN:
		return p.betweenExpression()
	case token.LIKE:
		return p.likeExpression()
	case token.IN:
		return p.inExpression()
	}
	if kindOf(tok) == token.IS && kindOf(lookahead) == token.NULL {
		return p.nullComparisonExpression()
	}
	return p.comparisonExpression()
}

// --- comparison / between / in / like / is null -----------------------------

func (p *Parser) betweenExpression() (*ast.BetweenExpression, error) {
	expr, err := p.arithmeticExpression()
	if err != nil {
		return nil, err
	}
	not := false
	if p.lx.IsNextToken(token.NOT) {
		if err := p.match(token.NOT); err != nil {
			return nil, err
		}
		not = true
	}
	if err := p.match(token.BETWEEN); err != nil {
		return nil, err
	}
	left, err := p.arithmeticExpression()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.AND); err != nil {
		return nil, err
	}
	right, err := p.arithmeticExpression()
	if err != nil {
		return nil, err
	}
	return &ast.BetweenExpression{Expr: expr, Left: left, Right: right, Not: not}, nil
}

func (p *Parser) comparisonExpression() (*ast.ComparisonExpression, error) {
	left, err := p.arithmeticExpression()
	if err != nil {
		return nil, err
	}
	op, err := p.comparisonOperator()
	if err != nil {
		return nil, err
	}
	right, err := p.arithmeticExpression()
	if err != nil {
		return nil, err
	}
	return &ast.ComparisonExpression{Left: left, Operator: op, Right: right}, nil
}

// comparisonOperator ::= "=" | "<" | "<=" | "<>" | ">" | ">=" | "!="
func (p *Parser) comparisonOperator() (string, error) {
	la := p.lx.Lookahead
	switch valueOf(la) {
	case "=":
		if err := p.match(token.EQUALS); err != nil {
			return "", err
		}
		return "=", nil

	case "<":
		if err := p.match(token.LOWER_THAN); err != nil {
			return "", err
		}
		op := "<"
		if p.lx.IsNextToken(token.EQUALS) {
			if err := p.match(token.EQUALS); err != nil {
				return "", err
			}
			op += "="
		} else if p.lx.IsNextToken(token.GREATER_THAN) {
			if err := p.match(token.GREATER_THAN); err != nil {
				return "", err
			}
			op += ">"
		}
		return op, nil

	case ">":
		if err := p.match(token.GREATER_THAN); err != nil {
			return "", err
		}
		op := ">"
		if p.lx.IsNextToken(token.EQUALS) {
			if err := p.match(token.EQUALS); err != nil {
				return "", err
			}
			op += "="
		}
		return op, nil

	case "!":
		if err := p.match(token.NEGATE); err != nil {
			return "", err
		}
		if err := p.match(token.EQUALS); err != nil {
			return "", err
		}
		return "<>", nil

	default:
		return "", p.syntaxError("=, <, <=, <>, >, >=, !=")
	}
}

func (p *Parser) inExpression() (*ast.InExpression, error) {
	expr, err := p.arithmeticExpression()
	if err != nil {
		return nil, err
	}
	in := &ast.InExpression{Expr: expr}

	if p.lx.IsNextToken(token.NOT) {
		if err := p.match(token.NOT); err != nil {
			return nil, err
		}
		in.Not = true
	}
	if err := p.match(token.IN); err != nil {
		return nil, err
	}
	if err := p.match(token.OPEN_PARENTHESIS); err != nil {
		return nil, err
	}

	item, err := p.inParameter()
	if err != nil {
		return nil, err
	}
	in.Items = append(in.Items, item)

	for p.lx.IsNextToken(token.COMMA) {
		if err := p.match(token.COMMA); err != nil {
			return nil, err
		}
		item, err := p.inParameter()
		if err != nil {
			return nil, err
		}
		in.Items = append(in.Items, item)
	}

	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return in, nil
}

func (p *Parser) likeExpression() (*ast.LikeExpression, error) {
	str, err := p.stringExpression()
	if err != nil {
		return nil, err
	}

	not := false
	if p.lx.IsNextToken(token.NOT) {
		if err := p.match(token.NOT); err != nil {
			return nil, err
		}
		not = true
	}
	if err := p.match(token.LIKE); err != nil {
		return nil, err
	}

	var pattern ast.Node
	if p.lx.IsNextToken(token.INPUT_PARAMETER) {
		pattern, err = p.inputParameter()
	} else {
		pattern, err = p.stringPrimary()
	}
	if err != nil {
		return nil, err
	}

	var escape *ast.Literal
	if p.lx.Lookahead != nil && p.lx.Lookahead.Kind == token.ESCAPE {
		if err := p.match(token.ESCAPE); err != nil {
			return nil, err
		}
		v := p.lx.Lookahead.Value
		if err := p.match(token.STRING); err != nil {
			return nil, err
		}
		escape = &ast.Literal{Type: ast.LiteralString, Value: v}
	}

	return &ast.LikeExpression{String: str, Pattern: pattern, Escape: escape, Not: not}, nil
}

func (p *Parser) nullComparisonExpression() (*ast.NullComparisonExpression, error) {
	var expr ast.Node
	var err error

	switch {
	case p.lx.IsNextToken(token.INPUT_PARAMETER):
		expr, err = p.inputParameter()
	case p.lx.IsNextToken(token.NULLIF):
		expr, err = p.nullIfExpression()
	case p.lx.IsNextToken(token.COALESCE):
		expr, err = p.coalesceExpression()
	case p.isFunction():
		expr, err = p.functionDeclaration()
	default:
		expr, err = p.pathExpression()
	}
	if err != nil {
		return nil, err
	}

	nc := &ast.NullComparisonExpression{Expr: expr}
	if err := p.match(token.IS); err != nil {
		return nil, err
	}
	if p.lx.IsNextToken(token.NOT) {
		if err := p.match(token.NOT); err != nil {
			return nil, err
		}
		nc.Not = true
	}
	if err := p.match(token.NULL); err != nil {
		return nil, err
	}
	return nc, nil
}
