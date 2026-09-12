// Package token defines the lexical tokens of the query language, mirroring
// itself modeled on Doctrine's DQL lexer).
package token

import "strings"

// Kind identifies the lexical class of a Token.
//
// expected" parser relies on:
//   - Kind < 100:  punctuation / literals that are never valid identifiers
//   - 100 <= Kind < 200: identifier-like tokens
//   - Kind >= 200: reserved keywords (which are also valid identifiers)
type Kind int

const (
	NONE              Kind = 1
	INTEGER           Kind = 2
	STRING            Kind = 3
	INPUT_PARAMETER   Kind = 4
	FLOAT             Kind = 5
	CLOSE_PARENTHESIS Kind = 6
	OPEN_PARENTHESIS  Kind = 7
	COMMA             Kind = 8
	DIVIDE            Kind = 9
	DOT               Kind = 10
	EQUALS            Kind = 11
	GREATER_THAN      Kind = 12
	LOWER_THAN        Kind = 13
	MINUS             Kind = 14
	MULTIPLY          Kind = 15
	NEGATE            Kind = 16
	PLUS              Kind = 17
	DATE              Kind = 20

	IDENTIFIER Kind = 102

	AND      Kind = 201
	BETWEEN  Kind = 206
	BOTH     Kind = 207
	CASE     Kind = 209
	COALESCE Kind = 210
	ELSE     Kind = 215
	END      Kind = 217
	FALSE    Kind = 220
	FROM     Kind = 221
	IN       Kind = 225
	IS       Kind = 229
	ESCAPE   Kind = 230
	LEADING  Kind = 231
	LIKE     Kind = 233
	NOT      Kind = 238
	NULL     Kind = 239
	NULLIF   Kind = 240
	OR       Kind = 242
	THEN     Kind = 250
	TRAILING Kind = 251
	TRUE     Kind = 252
	WHEN     Kind = 254
	WHERE    Kind = 255
)

// Keywords maps the upper-cased spelling of a keyword to its Kind. An
// identifier lexeme is looked up here (case-insensitively) to decide whether
// it is a plain identifier or a reserved word
var Keywords = map[string]Kind{
	"AND":      AND,
	"BETWEEN":  BETWEEN,
	"BOTH":     BOTH,
	"CASE":     CASE,
	"COALESCE": COALESCE,
	"ELSE":     ELSE,
	"END":      END,
	"FALSE":    FALSE,
	"FROM":     FROM,
	"IN":       IN,
	"IS":       IS,
	"ESCAPE":   ESCAPE,
	"LEADING":  LEADING,
	"LIKE":     LIKE,
	"NOT":      NOT,
	"NULL":     NULL,
	"NULLIF":   NULLIF,
	"OR":       OR,
	"THEN":     THEN,
	"TRAILING": TRAILING,
	"TRUE":     TRUE,
	"WHEN":     WHEN,
	"WHERE":    WHERE,
}

// LookupKeyword returns the Kind for value if it names a reserved keyword
// (case-insensitively), and false otherwise.
func LookupKeyword(value string) (Kind, bool) {
	k, ok := Keywords[strings.ToUpper(value)]
	return k, ok
}

// Token is a single lexical token produced by the lexer.
type Token struct {
	Kind     Kind
	Value    string // for STRING: the unquoted, un-escaped text; for DATE: the unquoted text; otherwise the raw lexeme
	Position int    // byte offset into the source where this token starts
}

// names gives a human-readable label for a Kind, used in error messages.
var names = map[Kind]string{
	NONE:              "T_NONE",
	INTEGER:           "integer",
	STRING:            "string",
	INPUT_PARAMETER:   "input parameter",
	FLOAT:             "float",
	CLOSE_PARENTHESIS: "')'",
	OPEN_PARENTHESIS:  "'('",
	COMMA:             "','",
	DIVIDE:            "'/'",
	DOT:               "'.'",
	EQUALS:            "'='",
	GREATER_THAN:      "'>'",
	LOWER_THAN:        "'<'",
	MINUS:             "'-'",
	MULTIPLY:          "'*'",
	NEGATE:            "'!'",
	PLUS:              "'+'",
	DATE:              "date",
	IDENTIFIER:        "identifier",
}

// Name returns a human-readable label for k, falling back to its keyword
// spelling (or a numeric placeholder) when no explicit label is registered.
func Name(k Kind) string {
	if n, ok := names[k]; ok {
		return n
	}
	for word, kind := range Keywords {
		if kind == k {
			return word
		}
	}
	return "token"
}
