package ql

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/AlexKapustin/go-ql/dialect"
	"github.com/AlexKapustin/go-ql/schema"
)

// TestSQLiteIntegration executes a representative compiled query against a
// real, in-memory SQLite database to confirm the generated SQL is actually
// valid SQLite, not just plausible-looking. Unlike the Postgres integration
// test, this needs no external service — modernc.org/sqlite is a pure-Go,
// cgo-free driver — so it always runs.
func TestSQLiteIntegration(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE products (
			id INTEGER PRIMARY KEY,
			sku TEXT NOT NULL,
			price REAL NOT NULL,
			metadata TEXT
		)
	`); err != nil {
		t.Fatalf("creating table: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO products (sku, price, metadata) VALUES
			('sku-1', 10, '{"brand": "Samsung", "custom": {"hfss": true}}'),
			('sku-2', 200, '{"brand": "Apple"}')
	`); err != nil {
		t.Fatalf("seeding rows: %v", err)
	}

	components := schema.Components{
		"product":        productEntity{},
		"product_custom": productCustomEntity{fields: []string{"hfss"}},
	}

	res, err := Compile(
		`product.brand = :brand and product.price < 100 and product_custom.hfss = true`,
		components,
		WithParams(map[string]any{"brand": "Samsung"}),
		WithDialect(dialect.NewSQLite()),
	)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	sqlText, args := res.Positional()
	rows, err := db.Query(`SELECT sku FROM products WHERE `+sqlText, args...)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var skus []string
	for rows.Next() {
		var sku string
		if err := rows.Scan(&sku); err != nil {
			t.Fatalf("scan: %v", err)
		}
		skus = append(skus, sku)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if len(skus) != 1 || skus[0] != "sku-1" {
		t.Fatalf("skus = %v, want exactly [sku-1]", skus)
	}
}
