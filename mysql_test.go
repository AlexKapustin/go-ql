package ql

import (
	"testing"
	"time"

	"github.com/AlexKapustin/go-ql/dialect"
)

// TestMySQLSimpleFixtures mirrors TestSimpleFixtures, translating expected
// SQL from Postgres syntax to MySQL syntax (backtick identifiers, `->>` JSON
// extraction, native DATE_ADD/DATE_SUB/DATEDIFF/LOCATE).
func TestMySQLSimpleFixtures(t *testing.T) {
	components := testComponents()
	params := WithParams(map[string]any{"testBrand": "Samsung"})
	mysqlDialect := WithDialect(dialect.NewMySQL())

	cases := []struct {
		src  string
		want string
	}{
		{`product.sku = "test-product-0001"`, "(`products`.`sku` = 'test-product-0001')"},
		{
			`product.sku in ("test-product-0001", "test-product-0002", "test-product-0003")`,
			"(`products`.`sku` IN ('test-product-0001', 'test-product-0002', 'test-product-0003'))",
		},
		{
			`product.brand in ("Samsung", "Apple")`,
			"(`products`.`metadata`->>'$.\"brand\"' IN ('Samsung', 'Apple'))",
		},
		{
			`product.price > 100 and product.sale_price is null`,
			"(`products`.`price` > 100 AND `products`.`sale_price` IS NULL)",
		},
		{`product.price between 100 and 200`, "(`products`.`price` BETWEEN 100 AND 200)"},
		{`product.price not between 100 and 200`, "(`products`.`price` NOT BETWEEN 100 AND 200)"},
		{
			`product.price between abs(product.sale_price) and product.price`,
			"(`products`.`price` BETWEEN ABS(`products`.`sale_price`) AND `products`.`price`)",
		},
		{`product.price > 1 + 30 / 2 * 5`, "(`products`.`price` > 1 + 30 / 2 * 5)"},
		{
			`case when product.price = 10 then "match" ELSE "not_match" end = "match"`,
			"(CASE WHEN `products`.`price` = 10 THEN 'match' ELSE 'not_match' END = 'match')",
		},
		{
			`case product.price when 20 then "match" else "not_match" end = "match"`,
			"(CASE `products`.`price` WHEN 20 THEN 'match' ELSE 'not_match' END = 'match')",
		},
		{
			`(product.brand = "Samsung" or product.brand = "Apple") and product.sale_price is not null`,
			"((`products`.`metadata`->>'$.\"brand\"' = 'Samsung' OR `products`.`metadata`->>'$.\"brand\"' = 'Apple') AND `products`.`sale_price` IS NOT NULL)",
		},
		{`abs(product.price) > 0`, "(ABS(`products`.`price`) > 0)"},
		{`trim(product.brand) = "Test"`, "(TRIM(`products`.`metadata`->>'$.\"brand\"') = 'Test')"},
		{
			`trim(both " " from product.brand) = "Test"`,
			"(TRIM(BOTH ' ' FROM `products`.`metadata`->>'$.\"brand\"') = 'Test')",
		},
		{
			`trim(leading " " from product.sku) = "Test"`,
			"(TRIM(LEADING ' ' FROM `products`.`sku`) = 'Test')",
		},
		{
			`trim(trailing " " from product.sku) = "Test"`,
			"(TRIM(TRAILING ' ' FROM `products`.`sku`) = 'Test')",
		},
		{
			`sqrt(product.price + 1) = length(product.brand)`,
			"(SQRT(`products`.`price` + 1) = LENGTH(`products`.`metadata`->>'$.\"brand\"'))",
		},
		{`product.brand = :testBrand`, "(`products`.`metadata`->>'$.\"brand\"' = 'Samsung')"},
		{`product.brand like "test%"`, "(`products`.`metadata`->>'$.\"brand\"' LIKE 'test%')"},
		{`product.brand like :testBrand`, "(`products`.`metadata`->>'$.\"brand\"' LIKE 'Samsung')"},
		{
			`product.sku like substring(product.brand, 2, 5)`,
			"(`products`.`sku` LIKE SUBSTRING(`products`.`metadata`->>'$.\"brand\"', 2, 5))",
		},
		{
			`product.sku like product.brand escape "#"`,
			"(`products`.`sku` LIKE `products`.`metadata`->>'$.\"brand\"' ESCAPE '#')",
		},
		{
			`coalesce(product.sale_price, product.price, 0) > 0`,
			"(COALESCE(`products`.`sale_price`, `products`.`price`, 0) > 0)",
		},
		{`nullif("test", product.price) is not null`, "(NULLIF('test', `products`.`price`) IS NOT NULL)"},
		{
			`concat(product.sku, "-", product.price) = "111"`,
			"(CONCAT(`products`.`sku`, '-', `products`.`price`) = '111')",
		},
		{`product_custom.hfss = true`, "(`products`.`metadata`->>'$.\"custom\".\"hfss\"' = TRUE)"},
		{
			`date_add(product.sale_price, 10, "year") > "2025-01-01"`,
			"(DATE_ADD(`products`.`sale_price`, INTERVAL 10 YEAR) > '2025-01-01 00:00:00')",
		},
		{
			`date_sub(current_timestamp(), 10, "year") > "2025-01-01"`,
			"(DATE_SUB(CURRENT_TIMESTAMP(), INTERVAL 10 YEAR) > '2025-01-01 00:00:00')",
		},
		{
			`date_add(product.sale_price, 2, "week") > "2025-01-01"`,
			"(DATE_ADD(`products`.`sale_price`, INTERVAL 2 WEEK) > '2025-01-01 00:00:00')",
		},
		{
			`date_diff(product.sale_price, current_date()) > 10`,
			"(DATEDIFF(`products`.`sale_price`, CURRENT_DATE()) > 10)",
		},
		{
			`locate("10:", current_time(), 0) = 0`,
			"(LOCATE('10:', CURRENT_TIME(), 0) = 0)",
		},
		{
			`lower(product.sku) = "test-100" and upper(product.sku) = "TEST-100"`,
			"(LOWER(`products`.`sku`) = 'test-100' AND UPPER(`products`.`sku`) = 'TEST-100')",
		},
		{
			`bit_and(product.price, 200) = 0 and bit_or(product.price, 300) = 1`,
			"(`products`.`price` & 200 = 0 AND `products`.`price` | 300 = 1)",
		},
		{`mod(product.price, 2) = 0`, "(MOD(`products`.`price`, 2) = 0)"},
		{`(2 + 2) * 2 = -6`, `((2 + 2) * 2 = -6)`},
		{
			`(product.price > 0) and not (product.sale_price > 0)`,
			"((`products`.`price` > 0) AND NOT (`products`.`sale_price` > 0))",
		},
	}

	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			res, err := Compile(c.src, components, params, mysqlDialect)
			if err != nil {
				t.Fatalf("Compile(%q): %v", c.src, err)
			}
			got := resolvedSQL(res)
			if got != c.want {
				t.Errorf("Compile(%q):\n got:  %s\n want: %s", c.src, got, c.want)
			}
		})
	}
}

func TestMySQLDatetimeFixture(t *testing.T) {
	loc, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	components := testComponents()

	res, err := Compile(`product.refreshed_at > "2024-09-02 10:00:00"`, components,
		WithTimezone(loc), WithDialect(dialect.NewMySQL()))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	want := "(`products`.`refreshed_at` > '2024-09-02 00:00:00')"
	if got := resolvedSQL(res); got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

func TestMySQLInvalidDateUnit(t *testing.T) {
	components := testComponents()
	mysqlDialect := WithDialect(dialect.NewMySQL())

	t.Run("date_add", func(t *testing.T) {
		if _, err := Compile(`date_add(product.sale_price, 10, "years") > "2025-01-01"`, components, mysqlDialect); err == nil {
			t.Fatal("expected an error for an invalid DATE_ADD unit")
		}
	})
	t.Run("date_sub", func(t *testing.T) {
		if _, err := Compile(`date_sub(product.sale_price, 10, "years") > "2025-01-01"`, components, mysqlDialect); err == nil {
			t.Fatal("expected an error for an invalid DATE_SUB unit")
		}
	})
}

func TestMySQLUsedFields(t *testing.T) {
	components := testComponents()
	src := `product.sku in ("test-001", "test-002") and product.price > 200 and product.brand = "Samsung" ` +
		`and product_custom.hfss = "yes"`

	res, err := Compile(src, components, WithDialect(dialect.NewMySQL()))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	usage, ok := res.UsedFields["products"]
	if !ok {
		t.Fatalf("UsedFields missing 'products': %v", res.UsedFields)
	}
	want := map[string]int{"sku": 1, "price": 1, "metadata": 2}
	for k, v := range want {
		if usage.Fields[k] != v {
			t.Errorf("usage.Fields[%q] = %d, want %d", k, usage.Fields[k], v)
		}
	}
}
