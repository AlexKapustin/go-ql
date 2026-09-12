// Package lexer tokenizes query-language source strings using
// github.com/timtadh/lexmachine as the scanning engine, reproducing the
//
// Unlike lexmachine's forward-only Scanner, Lexer tokenizes its whole input
// eagerly up front (queries are short, single-expression strings) and then
// exposes a Doctrine-AbstractLexer-style cursor API — MoveNext/Token/
// Lookahead/Peek/Glimpse/ResetPeek/IsNextToken — so the parser can be a
// faithful, recursive-descent grammar, which
// depends heavily on that exact lookahead API.
package lexer

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/timtadh/lexmachine"
	"github.com/timtadh/lexmachine/machines"

	"github.com/AlexKapustin/go-ql/token"
)

var dateRe = regexp.MustCompile(`^\d{4}-\d{1,2}-\d{1,2}( \d{1,2}:\d{1,2}:\d{1,2})?$`)

// rawToken is what our lexmachine actions produce; classification into a
// tokenize-then-getType(&$value) design.
type rawToken struct {
	kind  token.Kind // pre-classified when unambiguous (symbols); NONE placeholder for identifier/number/string/param
	value string
	pos   int
}

const (
	ruleIdentifier = iota
	ruleNumber
	ruleString
	ruleParam
	ruleSymbol
	ruleWhitespace
	ruleCatchAll
)

var machine *lexmachine.Lexer
var machineOnce sync.Once
var machineErr error

func symbolAction(k token.Kind) lexmachine.Action {
	return func(s *lexmachine.Scanner, m *machines.Match) (interface{}, error) {
		return rawToken{kind: k, value: string(m.Bytes), pos: m.TC}, nil
	}
}

func buildMachine() (*lexmachine.Lexer, error) {
	lx := lexmachine.NewLexer()

	lx.Add([]byte(`[A-Za-z_][A-Za-z0-9_]*`), func(s *lexmachine.Scanner, m *machines.Match) (interface{}, error) {
		return rawToken{kind: token.NONE, value: string(m.Bytes), pos: m.TC}, nil
	})
	lx.Add([]byte(`[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?`), func(s *lexmachine.Scanner, m *machines.Match) (interface{}, error) {
		return rawToken{kind: token.NONE, value: string(m.Bytes), pos: m.TC}, nil
	})
	lx.Add([]byte(`"([^"]|"")*"`), func(s *lexmachine.Scanner, m *machines.Match) (interface{}, error) {
		return rawToken{kind: token.STRING, value: string(m.Bytes), pos: m.TC}, nil
	})
	lx.Add([]byte(`\?[0-9]*`), func(s *lexmachine.Scanner, m *machines.Match) (interface{}, error) {
		return rawToken{kind: token.INPUT_PARAMETER, value: string(m.Bytes), pos: m.TC}, nil
	})
	lx.Add([]byte(`:[A-Za-z_][A-Za-z0-9_]*`), func(s *lexmachine.Scanner, m *machines.Match) (interface{}, error) {
		return rawToken{kind: token.INPUT_PARAMETER, value: string(m.Bytes), pos: m.TC}, nil
	})

	symbols := map[string]token.Kind{
		`\.`: token.DOT,
		`,`:  token.COMMA,
		`\(`: token.OPEN_PARENTHESIS,
		`\)`: token.CLOSE_PARENTHESIS,
		`=`:  token.EQUALS,
		`>`:  token.GREATER_THAN,
		`<`:  token.LOWER_THAN,
		`\+`: token.PLUS,
		`-`:  token.MINUS,
		`\*`: token.MULTIPLY,
		`/`:  token.DIVIDE,
		`!`:  token.NEGATE,
	}
	for pattern, kind := range symbols {
		lx.Add([]byte(pattern), symbolAction(kind))
	}

	lx.Add([]byte(`[ \t\n\r]+`), func(s *lexmachine.Scanner, m *machines.Match) (interface{}, error) {
		return nil, nil
	})

	// Catch-all: anything not matched by the rules above becomes a single
	// NONE token non-catchable pattern + T_NONE
	// default in getType(). This lets unrecognized characters surface as a
	// syntax error at parse time instead of a lexer error.
	lx.Add([]byte(`.`), func(s *lexmachine.Scanner, m *machines.Match) (interface{}, error) {
		return rawToken{kind: token.NONE, value: string(m.Bytes), pos: m.TC}, nil
	})

	if err := lx.Compile(); err != nil {
		return nil, err
	}
	return lx, nil
}

func getMachine() (*lexmachine.Lexer, error) {
	machineOnce.Do(func() {
		machine, machineErr = buildMachine()
	})
	return machine, machineErr
}

// classify turns a raw scanned token into its final token.Kind, replicating
func classify(rt rawToken) token.Token {
	switch rt.kind {
	case token.STRING:
		inner := rt.value[1 : len(rt.value)-1]
		inner = strings.ReplaceAll(inner, `""`, `"`)
		if dateRe.MatchString(inner) {
			return token.Token{Kind: token.DATE, Value: inner, Position: rt.pos}
		}
		return token.Token{Kind: token.STRING, Value: inner, Position: rt.pos}

	case token.INPUT_PARAMETER:
		return token.Token{Kind: token.INPUT_PARAMETER, Value: rt.value, Position: rt.pos}

	default:
		// still-unclassified: symbols already carry their final kind via
		// rt.kind set in symbolAction, so only NONE-tagged raw tokens
		// (identifier/number/catch-all) reach here.
	}

	if rt.kind != token.NONE {
		return token.Token{Kind: rt.kind, Value: rt.value, Position: rt.pos}
	}

	v := rt.value

	if len(v) > 0 && (isDigit(v[0])) {
		if strings.ContainsAny(v, ".eE") {
			return token.Token{Kind: token.FLOAT, Value: v, Position: rt.pos}
		}
		return token.Token{Kind: token.INTEGER, Value: v, Position: rt.pos}
	}

	if len(v) > 0 && (isAlpha(v[0]) || v[0] == '_') {
		if kw, ok := token.LookupKeyword(v); ok {
			return token.Token{Kind: kw, Value: v, Position: rt.pos}
		}
		return token.Token{Kind: token.IDENTIFIER, Value: v, Position: rt.pos}
	}

	return token.Token{Kind: token.NONE, Value: v, Position: rt.pos}
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
func isAlpha(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// Lexer tokenizes an entire input string up front and exposes a
// Doctrine-AbstractLexer-style navigation API over the resulting token
// stream: Token/Lookahead track the current position, MoveNext advances,
// and Peek/Glimpse/ResetPeek support lookahead without consuming.
type Lexer struct {
	tokens  []token.Token
	pos     int // index of the token MoveNext will expose as the new Lookahead
	peekPos int // index of the token the next Peek() call will return

	Token     *token.Token
	Lookahead *token.Token
}

// New tokenizes input and returns a Lexer positioned before the first
// token; call MoveNext to load the first Lookahead.
func New(input string) (*Lexer, error) {
	m, err := getMachine()
	if err != nil {
		return nil, fmt.Errorf("ql/lexer: building lexer machine: %w", err)
	}

	scanner, err := m.Scanner([]byte(input))
	if err != nil {
		return nil, fmt.Errorf("ql/lexer: creating scanner: %w", err)
	}

	var toks []token.Token
	for tk, err, eof := scanner.Next(); !eof; tk, err, eof = scanner.Next() {
		if err != nil {
			return nil, fmt.Errorf("ql/lexer: %w", err)
		}
		if tk == nil {
			continue // whitespace, skipped
		}
		toks = append(toks, classify(tk.(rawToken)))
	}

	return &Lexer{tokens: toks}, nil
}

// MoveNext advances Token/Lookahead by one position and reports whether a
// new Lookahead is available.
func (l *Lexer) MoveNext() bool {
	l.Token = l.Lookahead
	if l.pos < len(l.tokens) {
		t := l.tokens[l.pos]
		l.Lookahead = &t
		l.pos++
	} else {
		l.Lookahead = nil
	}
	l.peekPos = l.pos
	return l.Lookahead != nil
}

// Peek returns the token after the current peek cursor and advances that
// cursor by one, without touching Token/Lookahead. Consecutive calls walk
// further ahead; call ResetPeek to rewind to just after Lookahead.
func (l *Lexer) Peek() *token.Token {
	if l.peekPos < len(l.tokens) {
		t := l.tokens[l.peekPos]
		l.peekPos++
		return &t
	}
	l.peekPos++
	return nil
}

// ResetPeek rewinds the peek cursor back to just after Lookahead.
func (l *Lexer) ResetPeek() {
	l.peekPos = l.pos
}

// Glimpse returns the token immediately after Lookahead without moving the
// peek cursor (Peek followed by ResetPeek).
func (l *Lexer) Glimpse() *token.Token {
	t := l.Peek()
	l.ResetPeek()
	return t
}

// IsNextToken reports whether Lookahead is non-nil and has the given Kind.
func (l *Lexer) IsNextToken(k token.Kind) bool {
	return l.Lookahead != nil && l.Lookahead.Kind == k
}
