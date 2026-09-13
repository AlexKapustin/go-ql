// a mall, WHERE-clause-only expression language compiled to a parameterized SQL
// fragment for use with GORM (see gorm.go), or standalone via Result.
//
// A caller declares, per alias a query string may reference, which table
// and which whitelisted fields are queryable (see the schema package), then
// compiles a query string against that schema:
//
//	components := schema.Components{
//		"product": myProductMetadata{},
//	}
//	res, err := ql.Compile(`product.sku = :sku and product.price > 100`, components,
//		ql.WithParams(map[string]any{"sku": "abc-123"}))
//	db.Where(res.SQL, res.GormArgs()...).Find(&products)
package ql

import (
	"time"

	"github.com/AlexKapustin/go-ql/dialect"
	"github.com/AlexKapustin/go-ql/parser"
	"github.com/AlexKapustin/go-ql/schema"
	"github.com/AlexKapustin/go-ql/walker"
)

// Result is the outcome of compiling a query string.
type Result struct {
	// SQL is a parenthesized SQL fragment suitable for gorm.DB.Where,
	// referencing Args via GORM-style named placeholders (@name).
	SQL string
	// Args maps every placeholder name in SQL to its value.
	Args map[string]any
	// UsedFields tallies how many times each column of each referenced
	// table was used, keyed by table name.
	UsedFields map[string]schema.FieldUsage
}

// Positional rewrites SQL's @name placeholders to $1, $2, ... in
// first-occurrence order and returns the corresponding positional argument
// slice, for callers using database/sql or a driver that doesn't understand
// GORM's named-argument syntax directly.
func (r *Result) Positional() (sql string, args []any) {
	return rewriteToPositional(r.SQL, r.Args)
}

// GormArgs returns the variadic argument(s) to pass to gorm.DB.Where (or
// Model, Or, ...) alongside SQL: db.Where(res.SQL, res.GormArgs()...). Use
// this instead of passing res.Args directly — when SQL has no @name
// placeholders at all (e.g. a bare "col IS NULL" with no literals or
// params), GORM mishandles a lone map[string]any argument it has nothing
// to resolve against, so GormArgs omits it entirely in that case.
func (r *Result) GormArgs() []any {
	if len(r.Args) == 0 {
		return nil
	}
	return []any{r.Args}
}

type options struct {
	params      map[string]any
	timezone    *time.Location
	customFuncs map[string]dialect.FuncRenderer
	dialect     dialect.Dialect
}

// Option configures a Compile call.
type Option func(*options)

// WithParams supplies the values referenced by named (:name) and positional
// (?N) parameters in the query string. Referencing a parameter not present
// here is a compile error.
func WithParams(params map[string]any) Option {
	return func(o *options) { o.params = params }
}

// WithTimezone sets the timezone quoted date/datetime literals in the query
// string are interpreted in before being normalized to UTC. Defaults to UTC.
func WithTimezone(loc *time.Location) Option {
	return func(o *options) { o.timezone = loc }
}

// WithCustomFunc registers a renderer for a function name not built into
// the language, resolved at compile time against any call to that name.
// It cannot override a built-in function name (concat, trim, date_add,
// etc.), since those are parsed into their own dedicated AST shapes.
func WithCustomFunc(name string, fn dialect.FuncRenderer) Option {
	return func(o *options) {
		if o.customFuncs == nil {
			o.customFuncs = make(map[string]dialect.FuncRenderer)
		}
		o.customFuncs[name] = fn
	}
}

// WithDialect overrides the SQL dialect (defaults to Postgres).
func WithDialect(d dialect.Dialect) Option {
	return func(o *options) { o.dialect = d }
}

// Compile parses src against components and renders it to a Result. src
// must be a single boolean expression (no leading "WHERE").
func Compile(src string, components schema.Components, opts ...Option) (*Result, error) {
	o := &options{dialect: dialect.NewPostgres()}
	for _, opt := range opts {
		opt(o)
	}

	p, err := parser.New(src, components, o.params, o.timezone)
	if err != nil {
		return nil, err
	}
	tree, err := p.Parse()
	if err != nil {
		return nil, err
	}

	w := walker.New(o.dialect, components, o.params, o.customFuncs)
	sql, args, usage, err := w.Walk(tree)
	if err != nil {
		return nil, err
	}

	return &Result{SQL: sql, Args: args, UsedFields: usage}, nil
}
