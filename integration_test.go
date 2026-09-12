package ql

import (
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/AlexKapustin/go-ql/schema"
)

// TestPostgresIntegration executes a representative compiled query against
// a real PostgreSQL instance to confirm the generated SQL is actually valid
// Postgres, not just plausible-looking. It requires QL_TEST_POSTGRES_DSN
// (a standard postgres connection string) and is skipped otherwise — there
// is no live Postgres available in the environment this port was built in.
func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("QL_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("QL_TEST_POSTGRES_DSN not set; skipping live Postgres integration test")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("connecting to postgres: %v", err)
	}

	if err := db.Exec(`
		CREATE TEMP TABLE products (
			id SERIAL PRIMARY KEY,
			sku TEXT NOT NULL,
			price NUMERIC NOT NULL,
			metadata JSONB
		)
	`).Error; err != nil {
		t.Fatalf("creating temp table: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO products (sku, price, metadata) VALUES
			('sku-1', 10, '{"brand": "Samsung", "custom": {"hfss": true}}'),
			('sku-2', 200, '{"brand": "Apple"}')
	`).Error; err != nil {
		t.Fatalf("seeding rows: %v", err)
	}

	components := schema.Components{
		"product":        productEntity{},
		"product_custom": productCustomEntity{fields: []string{"hfss"}},
	}

	type row struct {
		ID    int
		Sku   string
		Price float64
	}
	var rows []row

	err = db.Table("products").Scopes(Where(
		`product.brand = :brand and product.price < 100 and product_custom.hfss = true`,
		components,
		WithParams(map[string]any{"brand": "Samsung"}),
	)).Find(&rows).Error
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if len(rows) != 1 || rows[0].Sku != "sku-1" {
		t.Fatalf("rows = %+v, want exactly sku-1", rows)
	}
}
