package lexer

import (
	"testing"

	"github.com/AlexKapustin/go-ql/token"
)

func tokenize(t *testing.T, input string) []token.Token {
	t.Helper()
	l, err := New(input)
	if err != nil {
		t.Fatalf("New(%q): %v", input, err)
	}
	var got []token.Token
	for l.MoveNext() {
		got = append(got, *l.Lookahead)
	}
	return got
}

func TestBasicTokens(t *testing.T) {
	toks := tokenize(t, `product.sku = "test-product-0001"`)
	want := []token.Kind{token.IDENTIFIER, token.DOT, token.IDENTIFIER, token.EQUALS, token.STRING}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d: %+v", len(toks), len(want), toks)
	}
	for i, k := range want {
		if toks[i].Kind != k {
			t.Errorf("token %d: got kind %v, want %v (%+v)", i, toks[i].Kind, k, toks[i])
		}
	}
	if toks[4].Value != "test-product-0001" {
		t.Errorf("string value = %q, want %q", toks[4].Value, "test-product-0001")
	}
}

func TestKeywords(t *testing.T) {
	toks := tokenize(t, `product.price between 100 and 200`)
	kinds := make([]token.Kind, len(toks))
	for i, tk := range toks {
		kinds[i] = tk.Kind
	}
	want := []token.Kind{token.IDENTIFIER, token.DOT, token.IDENTIFIER, token.BETWEEN, token.INTEGER, token.AND, token.INTEGER}
	if len(kinds) != len(want) {
		t.Fatalf("got %d tokens, want %d: %+v", len(kinds), len(want), toks)
	}
	for i, k := range want {
		if kinds[i] != k {
			t.Errorf("token %d: got kind %v, want %v", i, kinds[i], k)
		}
	}
}

func TestDateDetection(t *testing.T) {
	toks := tokenize(t, `product.refreshed_at > "2024-09-02 10:00:00"`)
	last := toks[len(toks)-1]
	if last.Kind != token.DATE {
		t.Fatalf("got kind %v, want DATE (%+v)", last.Kind, last)
	}
	if last.Value != "2024-09-02 10:00:00" {
		t.Errorf("value = %q", last.Value)
	}
}

func TestPlainStringNotDate(t *testing.T) {
	toks := tokenize(t, `product.sku = "test-product-0001"`)
	last := toks[len(toks)-1]
	if last.Kind != token.STRING {
		t.Fatalf("got kind %v, want STRING", last.Kind)
	}
}

func TestEscapedQuote(t *testing.T) {
	toks := tokenize(t, `product.sku = "a""b"`)
	last := toks[len(toks)-1]
	if last.Kind != token.STRING {
		t.Fatalf("got kind %v, want STRING", last.Kind)
	}
	if last.Value != `a"b` {
		t.Errorf("value = %q, want %q", last.Value, `a"b`)
	}
}

func TestParameters(t *testing.T) {
	toks := tokenize(t, `product.brand = :testBrand`)
	last := toks[len(toks)-1]
	if last.Kind != token.INPUT_PARAMETER || last.Value != ":testBrand" {
		t.Fatalf("got %+v", last)
	}

	toks = tokenize(t, `product.brand = ?1`)
	last = toks[len(toks)-1]
	if last.Kind != token.INPUT_PARAMETER || last.Value != "?1" {
		t.Fatalf("got %+v", last)
	}
}

func TestComparisonSymbolsAreSingleChar(t *testing.T) {
	toks := tokenize(t, `product.price <= 100`)
	// "<=" should lex as two separate single-char tokens; the parser
	if len(toks) != 6 {
		t.Fatalf("got %d tokens, want 6: %+v", len(toks), toks)
	}
	if toks[3].Kind != token.LOWER_THAN {
		t.Errorf("token 3 kind = %v, want LOWER_THAN", toks[3].Kind)
	}
	if toks[4].Kind != token.EQUALS {
		t.Errorf("token 4 kind = %v, want EQUALS", toks[4].Kind)
	}
}

func TestPeekGlimpseResetPeek(t *testing.T) {
	l, err := New(`a . b`)
	if err != nil {
		t.Fatal(err)
	}
	l.MoveNext() // lookahead = a
	if l.Lookahead.Kind != token.IDENTIFIER {
		t.Fatalf("lookahead = %+v", l.Lookahead)
	}
	g := l.Glimpse()
	if g == nil || g.Kind != token.DOT {
		t.Fatalf("glimpse = %+v, want DOT", g)
	}
	// Glimpse must not have moved the peek cursor permanently.
	p1 := l.Peek()
	if p1 == nil || p1.Kind != token.DOT {
		t.Fatalf("peek after glimpse = %+v, want DOT", p1)
	}
	p2 := l.Peek()
	if p2 == nil || p2.Kind != token.IDENTIFIER {
		t.Fatalf("second peek = %+v, want IDENTIFIER", p2)
	}
	l.ResetPeek()
	p3 := l.Peek()
	if p3 == nil || p3.Kind != token.DOT {
		t.Fatalf("peek after resetPeek = %+v, want DOT", p3)
	}
}

func TestUnrecognizedCharacterProducesNoneToken(t *testing.T) {
	toks := tokenize(t, `1 = ї`)
	var sawNone bool
	for _, tk := range toks {
		if tk.Kind == token.NONE {
			sawNone = true
		}
	}
	if !sawNone {
		t.Fatalf("expected a NONE token for unrecognized character, got %+v", toks)
	}
}
