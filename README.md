# ql

`ql` is a small, WHERE-clause-only expression language compiler. It parses a single
boolean expression string against a whitelisted schema and compiles it to a
parameterized SQL fragment, ready to hand to GORM's `Where`.

It is **not** a full query language: no `SELECT`, `JOIN`, `ORDER BY`, or
subqueries. It only compiles the condition that goes inside a `WHERE`.

## Why

Letting a caller (an API client, a saved-filter feature, etc.) supply a raw
filter expression is convenient but dangerous if that expression reaches SQL
unchecked. `ql` gives you a small, safe expression grammar instead: every
field referenced in a query string must be explicitly whitelisted by a
`schema.TableMetadata` you write, every value is bound as a named parameter
(never string-concatenated into the SQL), and constructs that could smuggle
arbitrary SQL — like a subquery inside `IN (...)` — are rejected as syntax
errors.

## Install

```sh
go get github.com/AlexKapustin/go-ql
```

## Quick start

Declare which tables and fields a query string is allowed to reference:

```go
package main

import (
	ql "github.com/AlexKapustin/go-ql"
	"github.com/AlexKapustin/go-ql/schema"
)

type productMetadata struct{}

func (productMetadata) TableName() string { return "products" }

func (productMetadata) Fields() map[string]schema.FieldMapping {
	return map[string]schema.FieldMapping{
		"sku":   {Column: "sku"},
		"price": {Column: "price"},
		// "brand" is stored inside a JSONB "metadata" column, at key "brand".
		// ql treats it as an ordinary field for querying purposes.
		"brand": {Column: "metadata", JSONPath: "brand"},
	}
}
```

Compile a query string against that schema and pass the result straight to
GORM:

```go
components := schema.Components{
	"product": productMetadata{},
}

res, err := ql.Compile(
	`product.sku = :sku and product.price > 100 and product.brand = "Samsung"`,
	components,
	ql.WithParams(map[string]any{"sku": "abc-123"}),
)
if err != nil {
	return err
}

var products []Product
err = db.Where(res.SQL, res.Args).Find(&products).Error
```

Or use the GORM scope adapter, which folds compile errors into
`db.Error` instead of requiring a separate error check:

```go
err := db.Scopes(ql.Where(
	`product.sku = :sku`,
	components,
	ql.WithParams(map[string]any{"sku": "abc-123"}),
)).Find(&products).Error
```

`res.SQL` is a parenthesized fragment like
`("products"."sku" = @sku AND "products"."price" > 100 AND "products"."metadata" #>> '{brand}' = @ql_lit_1)`,
and `res.Args` is the `map[string]any` GORM resolves those `@name`
placeholders against (`map[ql_lit_1:Samsung sku:abc-123]` here) — you never
need to think about placeholder numbering or ordering. Numeric and boolean
literals are inlined directly (they can't carry a SQL-injection risk once
the parser has validated them as a number/bool token); string and date
literals are always placeholder-bound.

### Using it outside GORM

If you're going through `database/sql` or a driver that doesn't understand
GORM's `@name` argument syntax, rewrite to positional placeholders first:

```go
sqlText, args := res.Positional() // "... WHERE ... $1 ... $2 ..."
rows, err := db.QueryContext(ctx, sqlText, args...)
```

## GORM integration

`ql.Where(...)` returns a `func(*gorm.DB) *gorm.DB` — a plain [GORM
scope](https://gorm.io/docs/advanced_query.html#Scopes) — so it composes
with the rest of GORM's chainable API and with your own scopes. A compile
error (bad syntax, an unwhitelisted field, a missing parameter, ...) is
recorded via `db.AddError` rather than panicking, so it surfaces through the
normal `.Error` check at the end of the chain like any other GORM error.
`ql.Compile`/`ql.Where` default to the Postgres dialect — pass
`ql.WithDialect(dialect.NewSQLite())` (see [Dialects](#dialects)) if your
`*gorm.DB` is actually a SQLite connection.

### Filtering a list endpoint

```go
type Product struct {
	ID    uint
	Sku   string
	Price float64
}

var productMetadataComponents = schema.Components{"product": productMetadata{}}

func ListProducts(db *gorm.DB, filter string, params map[string]any) ([]Product, error) {
	var products []Product
	err := db.Scopes(ql.Where(filter, productMetadataComponents, ql.WithParams(params))).
		Order("price desc").
		Limit(50).
		Find(&products).Error
	return products, err
}
```

`filter` here is exactly the kind of string an API client might send
(`?filter=product.price > 100 and product.brand = :brand`) — `ql` is what
makes accepting that from the outside world safe, since only whitelisted
fields and parameter-bound values can reach the query.

### Combining with other scopes

`Scopes` takes any number of scope functions, so a `ql` filter composes with
your existing ones (soft-delete filtering, tenant scoping, etc.) in one
chain:

```go
func notDeleted(db *gorm.DB) *gorm.DB {
	return db.Where("deleted_at IS NULL")
}

func ListActiveProducts(db *gorm.DB, filter string, params map[string]any) ([]Product, error) {
	var products []Product
	err := db.Scopes(
		notDeleted,
		ql.Where(filter, productMetadataComponents, ql.WithParams(params)),
	).Find(&products).Error
	return products, err
}
```

### Count, Update, Delete

Since it's an ordinary scope producing a `WHERE`, it works with any GORM
verb, not just `Find`:

```go
func CountProducts(db *gorm.DB, filter string, params map[string]any) (int64, error) {
	var count int64
	err := db.Model(&Product{}).
		Scopes(ql.Where(filter, productMetadataComponents, ql.WithParams(params))).
		Count(&count).Error
	return count, err
}

func DiscountMatching(db *gorm.DB, filter string, params map[string]any, pct float64) error {
	return db.Model(&Product{}).
		Scopes(ql.Where(filter, productMetadataComponents, ql.WithParams(params))).
		Update("price", gorm.Expr("price * ?", 1-pct)).Error
}
```

### Inside a transaction

```go
func ArchiveMatching(db *gorm.DB, filter string, params map[string]any) error {
	return db.Transaction(func(tx *gorm.DB) error {
		return tx.Model(&Product{}).
			Scopes(ql.Where(filter, productMetadataComponents, ql.WithParams(params))).
			Update("archived", true).Error
	})
}
```

### Compiling explicitly instead of using the scope adapter

If you'd rather see the compile error at the call site instead of via
`db.Error`, call `ql.Compile` directly and pass the result to `db.Where`
yourself — this is exactly what `ql.Where` does internally:

```go
func FindProducts(db *gorm.DB, filter string, params map[string]any) ([]Product, error) {
	res, err := ql.Compile(filter, productMetadataComponents, ql.WithParams(params))
	if err != nil {
		return nil, err
	}
	var products []Product
	err = db.Where(res.SQL, res.Args).Find(&products).Error
	return products, err
}
```

## Query language

A query is a single boolean expression. Field references are always
`alias.field`, where `alias` is a key in the `schema.Components` map you
compiled against.

```
product.sku = "abc-123"
product.price >= 10 and product.price <= 100
not (product.sku = "x" or product.sku = "y")
product.sku in ("a", "b", "c")
product.sku not in (:excluded1, :excluded2)
product.name like "50!% off" escape "!"
product.deleted_at is null
product.price between 10 and 100
product.refreshed_at > "2024-01-01 00:00:00"
```

**Comparisons:** `= < <= <> > >= !=` (`!=` is accepted and normalized to `<>`)

**Logic:** `and` / `or` / `not`, parentheses for grouping. Precedence is
`or` (loosest) > `and` > `not`, same as SQL.

**Arithmetic:** `+ - * /`, unary `+`/`-`, standard precedence, e.g.
`product.price > (product.sale_price + 5) * 2`.

**Literals:** double-quoted strings (`""` inside a string is a literal `"`),
integers, floats (including scientific notation `1e10`), `true`/`false`, and
dates/datetimes — a quoted string matching `YYYY-MM-DD` or
`YYYY-MM-DD HH:MM:SS` is automatically treated as a date literal, interpreted
in the timezone from `ql.WithTimezone` (UTC by default) and normalized to
UTC.

**Parameters:** `:name` (bound via `ql.WithParams(map[string]any{"name": ...})`)
or positional `?1`, `?2`, ... (bound via the same map, keyed as `"1"`, `"2"`,
...). Referencing a parameter you didn't supply is a compile error.

**CASE / COALESCE / NULLIF** are scalar expressions, usable anywhere a value
is expected (they're not booleans on their own, so they appear on one side
of a comparison):
```
(case product.sku when "a" then 1 when "b" then 2 else 0 end) = 1
(case when product.price > 100 then "expensive" else "cheap" end) = "expensive"
product.price = coalesce(product.sale_price, product.price, 0)
nullif(product.price, 0) is null
```

**Built-in functions:** `concat`, `substring`, `trim([leading|trailing|both]
char from expr)`, `lower`, `upper`, `length`, `locate(needle, haystack)`,
`abs`, `sqrt`, `mod`, `date_diff`, `bit_and`, `bit_or`, `current_date()`,
`current_time()`, `current_timestamp()`, `date_add(expr, n, "unit")`,
`date_sub(expr, n, "unit")`, where `"unit"` is a quoted string, one of
`"second"`, `"minute"`, `"hour"`, `"day"`, `"week"`, `"month"`, `"year"`.

Register your own with `ql.WithCustomFunc(name, fn)` — it can't be named the
same as a built-in, since built-in names are parsed into their own dedicated
grammar rather than falling through to the custom-function table.

### `WithCustomFunc` example

A `dialect.FuncRenderer` receives its arguments already rendered to SQL
(quoted identifiers, placeholders, nested expressions — whatever they are,
you just splice them in), and returns the SQL for the call, or an error to
reject the call (e.g. a bad argument count):

```go
ageYears := func(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("age_years() takes exactly 1 argument, got %d", len(args))
	}
	return fmt.Sprintf("DATE_PART('year', AGE(%s))", args[0]), nil
}

res, err := ql.Compile(
	`age_years(product.refreshed_at) < 1`,
	components,
	ql.WithCustomFunc("age_years", ageYears),
)
// res.SQL == `(DATE_PART('year', AGE("products"."refreshed_at")) < 1)`
```

A `WithCustomFunc` error is returned from `Compile` like any other compile
error — it doesn't panic mid-query.

## Errors

`Compile` returns `*parser.SyntaxError` for a malformed query string (bad
token, unexpected end of input, ...) and `*parser.SemanticError` for a
grammatically valid one that isn't allowed — an unknown alias, a field not
in that table's whitelist, an undeclared parameter, or an invalid function
argument (e.g. a bad `date_add` unit).

## Dialects

SQL generation lives entirely behind the `dialect.Dialect` interface
(identifier quoting, JSON path extraction, date arithmetic, `TRIM` syntax,
and the built-in function table) — the lexer, parser, and walker know
nothing dialect-specific. Three implementations ship today:

- `dialect.NewPostgres()` — the default. JSONB `#>>` path extraction, ANSI
  `TRIM(... FROM ...)`, `+/- INTERVAL` date arithmetic.
- `dialect.NewMySQL()` — pass via `ql.WithDialect(dialect.NewMySQL())`.
  Targets **MySQL 8.0 and above**. Backtick identifiers, `->>'$."path"'`
  JSON path extraction, native `DATE_ADD`/`DATE_SUB(expr, INTERVAL n UNIT)`,
  `DATEDIFF`, and `LOCATE(needle, haystack[, pos])` (MySQL's native 3-argument
  form takes arguments in exactly the order this library's `locate()` uses,
  so no rewriting is needed, unlike the other two dialects).
- `dialect.NewSQLite()` — pass via `ql.WithDialect(dialect.NewSQLite())`.
  Uses `json_extract(col, '$.path')` for JSON columns, `TRIM`/`LTRIM`/`RTRIM`
  (SQLite has no `FROM`-clause `TRIM` form), and `datetime(expr, printf(...))`
  modifiers for date arithmetic (SQLite has no `DATE_ADD`/`DATEDIFF`
  builtins — `date_diff` uses `julianday()` subtraction instead). `sqrt()`
  and `mod()` render to `SQRT()`/`%`; `SQRT()` needs SQLite built with
  `SQLITE_ENABLE_MATH_FUNCTIONS`, the default in most modern builds and Go
  drivers (`modernc.org/sqlite`, `mattn/go-sqlite3`'s default tags) — if
  yours lacks it, override with `ql.WithCustomFunc("sqrt", ...)`.

```go
res, err := ql.Compile(
	`product.brand = :brand and product.price < 100`,
	components,
	ql.WithParams(map[string]any{"brand": "Samsung"}),
	ql.WithDialect(dialect.NewMySQL()),
)
```

Another engine can be added the same way, without touching the lexer,
parser, or walker.

## Package layout

```
token/    token kinds
lexer/    lexmachine-backed tokenizer
ast/      parsed expression tree
parser/   recursive-descent parser + error types
schema/   TableMetadata / Components (the field whitelist)
dialect/  Dialect interface + Postgres implementation
walker/   AST -> (SQL, named args) renderer
ql.go     public Compile/Result/Option API
gorm.go   GORM Where(...) scope adapter
```

## Testing

`go test ./...` runs the full unit and fixture-ported test suite for all
three dialects. Two of the three also have a live-database integration
test:

- `integration_test.go` (Postgres) — skipped unless `QL_TEST_POSTGRES_DSN`
  is set to a Postgres connection string.
- `integration_mysql_test.go` (MySQL) — skipped unless `QL_TEST_MYSQL_DSN`
  is set to a `go-sql-driver/mysql` DSN, e.g.
  `user:pass@tcp(127.0.0.1:3306)/dbname?parseTime=true`.
- `integration_sqlite_test.go` (SQLite) — always runs; SQLite needs no
  external service, so this one executes against a real in-memory database
  every time.
