package walker

import "fmt"

// reservedParamPrefix must match parser.reservedParamPrefix: user-supplied
// named parameters starting with it are rejected at parse time, so every
// name this builder synthesizes is guaranteed collision-free.
const reservedParamPrefix = "ql_"

// argBuilder synthesizes unique, GORM-compatible named placeholders (@name)
// for every value referenced while walking a query, and collects the
// corresponding values into a map suitable for gorm.DB.Where(sql, args).
type argBuilder struct {
	args   map[string]any
	litSeq int
}

func newArgBuilder() *argBuilder {
	return &argBuilder{args: map[string]any{}}
}

// Named records a user-supplied named parameter (ZQL ":foo") under its own
// name, so the generated SQL stays readable.
func (b *argBuilder) Named(name string, v any) string {
	b.args[name] = v
	return "@" + name
}

// Positional records a user-supplied positional parameter (ZQL "?3") under
// a synthesized name derived from its digits.
func (b *argBuilder) Positional(digits string, v any) string {
	key := fmt.Sprintf("%spos_%s", reservedParamPrefix, digits)
	b.args[key] = v
	return "@" + key
}

// Literal records an inline literal value from the query text itself (e.g.
// the 100 in "price > 100") under a synthesized, sequential name.
func (b *argBuilder) Literal(v any) string {
	b.litSeq++
	key := fmt.Sprintf("%slit_%d", reservedParamPrefix, b.litSeq)
	b.args[key] = v
	return "@" + key
}
