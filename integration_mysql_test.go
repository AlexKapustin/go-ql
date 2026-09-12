package ql

import (
	"os"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/AlexKapustin/go-ql/dialect"
	"github.com/AlexKapustin/go-ql/schema"
)

// TestMySQLIntegration executes a representative compiled query against a
// real MySQL 8.0+ instance to confirm the generated SQL is actually valid
// MySQL, not just plausible-looking. It requires QL_TEST_MYSQL_DSN (a
// standard go-sql-driver/mysql DSN) and is skipped otherwise.
func TestMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("QL_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("QL_TEST_MYSQL_DSN not set; skipping live MySQL integration test")
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("connecting to mysql: %v", err)
	}

	if err := db.Exec(`DROP TABLE IF EXISTS products`).Error; err != nil {
		t.Fatalf("dropping table: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE products (
			id INT AUTO_INCREMENT PRIMARY KEY,
			sku VARCHAR(255) NOT NULL,
			price DECIMAL(10,2) NOT NULL,
			metadata JSON
		)
	`).Error; err != nil {
		t.Fatalf("creating table: %v", err)
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
		`product.brand = :brand and product.price < 100 and product_custom.hfss is not null`,
		components,
		WithParams(map[string]any{"brand": "Samsung"}),
		WithDialect(dialect.NewMySQL()),
	)).Find(&rows).Error
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if len(rows) != 1 || rows[0].Sku != "sku-1" {
		t.Fatalf("rows = %+v, want exactly sku-1", rows)
	}
}
