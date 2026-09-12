package parser

import (
	"strings"

	"github.com/AlexKapustin/go-ql/ast"
	"github.com/AlexKapustin/go-ql/token"
)

// functionDeclaration parses a call to a built-in string/numeric/datetime
// function (matching one of the argument grammars below) or, for any other
// name, a generic comma-separated-argument custom function call resolved
// against a caller-supplied renderer at walk time.
func (p *Parser) functionDeclaration() (ast.Node, error) {
	name := strings.ToLower(p.lx.Lookahead.Value)

	switch name {
	case "concat":
		return p.parseConcat()
	case "substring":
		return p.parseSubstring()
	case "trim":
		return p.parseTrim()
	case "lower", "upper", "length":
		return p.parseUnaryStringFunc(name)
	case "locate":
		return p.parseLocate()
	case "abs", "sqrt":
		return p.parseUnaryArithFunc(name)
	case "mod":
		return p.parseBinaryFunc(name, p.simpleArithmeticExpression)
	case "date_diff", "bit_and", "bit_or":
		return p.parseBinaryFunc(name, p.arithmeticPrimary)
	case "current_date", "current_time", "current_timestamp":
		return p.parseZeroArgFunc(name)
	case "date_add":
		return p.parseDateAddSub(false)
	case "date_sub":
		return p.parseDateAddSub(true)
	default:
		return p.parseCustomFunction(name)
	}
}

func (p *Parser) matchFuncOpen() error {
	if err := p.match(token.IDENTIFIER); err != nil {
		return err
	}
	return p.match(token.OPEN_PARENTHESIS)
}

// concat ::= "CONCAT" "(" StringPrimary "," StringPrimary {"," StringPrimary}* ")"
func (p *Parser) parseConcat() (ast.Node, error) {
	if err := p.matchFuncOpen(); err != nil {
		return nil, err
	}
	a, err := p.stringPrimary()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.COMMA); err != nil {
		return nil, err
	}
	b, err := p.stringPrimary()
	if err != nil {
		return nil, err
	}
	args := []ast.Node{a, b}

	for p.lx.IsNextToken(token.COMMA) {
		if err := p.match(token.COMMA); err != nil {
			return nil, err
		}
		n, err := p.stringPrimary()
		if err != nil {
			return nil, err
		}
		args = append(args, n)
	}

	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return &ast.FuncCall{Name: "concat", Args: args}, nil
}

// substring ::= "SUBSTRING" "(" StringPrimary "," SimpleArithmeticExpression ["," SimpleArithmeticExpression] ")"
func (p *Parser) parseSubstring() (ast.Node, error) {
	if err := p.matchFuncOpen(); err != nil {
		return nil, err
	}
	str, err := p.stringPrimary()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.COMMA); err != nil {
		return nil, err
	}
	start, err := p.simpleArithmeticExpression()
	if err != nil {
		return nil, err
	}
	args := []ast.Node{str, start}

	if p.lx.IsNextToken(token.COMMA) {
		if err := p.match(token.COMMA); err != nil {
			return nil, err
		}
		length, err := p.simpleArithmeticExpression()
		if err != nil {
			return nil, err
		}
		args = append(args, length)
	}

	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return &ast.FuncCall{Name: "substring", Args: args}, nil
}

// trim ::= "TRIM" "(" [["LEADING"|"TRAILING"|"BOTH"] [char] "FROM"] StringPrimary ")"
func (p *Parser) parseTrim() (ast.Node, error) {
	if err := p.matchFuncOpen(); err != nil {
		return nil, err
	}

	trim := &ast.TrimExpression{}
	if la := p.lx.Lookahead; la != nil {
		switch la.Kind {
		case token.LEADING:
			if err := p.match(token.LEADING); err != nil {
				return nil, err
			}
			trim.Mode = "LEADING"
		case token.TRAILING:
			if err := p.match(token.TRAILING); err != nil {
				return nil, err
			}
			trim.Mode = "TRAILING"
		case token.BOTH:
			if err := p.match(token.BOTH); err != nil {
				return nil, err
			}
			trim.Mode = "BOTH"
		}
	}

	if p.lx.IsNextToken(token.STRING) {
		ch := p.lx.Lookahead.Value
		if err := p.match(token.STRING); err != nil {
			return nil, err
		}
		trim.Char = &ast.Literal{Type: ast.LiteralString, Value: ch}
	}

	if trim.Mode != "" || trim.Char != nil {
		if err := p.match(token.FROM); err != nil {
			return nil, err
		}
	}

	target, err := p.stringPrimary()
	if err != nil {
		return nil, err
	}
	trim.Target = target

	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return trim, nil
}

func (p *Parser) parseUnaryStringFunc(name string) (ast.Node, error) {
	if err := p.matchFuncOpen(); err != nil {
		return nil, err
	}
	arg, err := p.stringPrimary()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return &ast.FuncCall{Name: name, Args: []ast.Node{arg}}, nil
}

func (p *Parser) parseUnaryArithFunc(name string) (ast.Node, error) {
	if err := p.matchFuncOpen(); err != nil {
		return nil, err
	}
	arg, err := p.simpleArithmeticExpression()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return &ast.FuncCall{Name: name, Args: []ast.Node{arg}}, nil
}

// locate ::= "LOCATE" "(" StringPrimary "," StringPrimary ["," SimpleArithmeticExpression] ")"
//
// ZQL argument order is (needle, haystack [, startPos]); see
// dialect.Postgres.Functions()["locate"] for the rendering.
func (p *Parser) parseLocate() (ast.Node, error) {
	if err := p.matchFuncOpen(); err != nil {
		return nil, err
	}
	needle, err := p.stringPrimary()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.COMMA); err != nil {
		return nil, err
	}
	haystack, err := p.stringPrimary()
	if err != nil {
		return nil, err
	}
	args := []ast.Node{needle, haystack}

	if p.lx.IsNextToken(token.COMMA) {
		if err := p.match(token.COMMA); err != nil {
			return nil, err
		}
		pos, err := p.simpleArithmeticExpression()
		if err != nil {
			return nil, err
		}
		args = append(args, pos)
	}

	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return &ast.FuncCall{Name: "locate", Args: args}, nil
}

func (p *Parser) parseBinaryFunc(name string, argFn func() (ast.Node, error)) (ast.Node, error) {
	if err := p.matchFuncOpen(); err != nil {
		return nil, err
	}
	a, err := argFn()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.COMMA); err != nil {
		return nil, err
	}
	b, err := argFn()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return &ast.FuncCall{Name: name, Args: []ast.Node{a, b}}, nil
}

func (p *Parser) parseZeroArgFunc(name string) (ast.Node, error) {
	if err := p.matchFuncOpen(); err != nil {
		return nil, err
	}
	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return &ast.FuncCall{Name: name, Args: nil}, nil
}

// dateAddSub ::= ("DATE_ADD"|"DATE_SUB") "(" ArithmeticPrimary "," ArithmeticPrimary "," string ")"
func (p *Parser) parseDateAddSub(sub bool) (ast.Node, error) {
	if err := p.matchFuncOpen(); err != nil {
		return nil, err
	}
	date, err := p.arithmeticPrimary()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.COMMA); err != nil {
		return nil, err
	}
	interval, err := p.arithmeticPrimary()
	if err != nil {
		return nil, err
	}
	if err := p.match(token.COMMA); err != nil {
		return nil, err
	}
	if !p.lx.IsNextToken(token.STRING) {
		return nil, p.syntaxError("string unit")
	}
	unit := p.lx.Lookahead.Value
	if err := p.match(token.STRING); err != nil {
		return nil, err
	}
	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return &ast.DateAddExpression{Sub: sub, Date: date, Interval: interval, Unit: strings.ToLower(unit)}, nil
}

// parseCustomFunction parses a generic FuncName "(" [ScalarExpression {"," ScalarExpression}*] ")"
// call for any function name not recognized as a built-in. It is resolved
// against a caller-supplied renderer at walk time (see the WithCustomFunc
// facade option); an unresolved name is a walk-time error, not a parse
// error, since the parser has no registry of custom function names.
func (p *Parser) parseCustomFunction(name string) (ast.Node, error) {
	if err := p.matchFuncOpen(); err != nil {
		return nil, err
	}

	var args []ast.Node
	if !p.lx.IsNextToken(token.CLOSE_PARENTHESIS) {
		a, err := p.scalarExpression()
		if err != nil {
			return nil, err
		}
		args = append(args, a)

		for p.lx.IsNextToken(token.COMMA) {
			if err := p.match(token.COMMA); err != nil {
				return nil, err
			}
			n, err := p.scalarExpression()
			if err != nil {
				return nil, err
			}
			args = append(args, n)
		}
	}

	if err := p.match(token.CLOSE_PARENTHESIS); err != nil {
		return nil, err
	}
	return &ast.FuncCall{Name: name, Args: args}, nil
}
