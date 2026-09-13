package ql

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AlexKapustin/go-ql/schema"
)

type productEntity struct{}

func (productEntity) TableName() string { return "products" }
func (productEntity) Fields() map[string]schema.FieldMapping {
	return map[string]schema.FieldMapping{
		"name":         {Column: "name"},
		"target_id":    {Column: "target_id"},
		"price":        {Column: "price"},
		"sale_price":   {Column: "sale_price"},
		"sku":          {Column: "sku"},
		"refreshed_at": {Column: "refreshed_at"},
		"brand":        {Column: "metadata", JSONPath: "brand"},
	}
}

type productCustomEntity struct{ fields []string }

func (productCustomEntity) TableName() string { return "products" }
func (e productCustomEntity) Fields() map[string]schema.FieldMapping {
	out := map[string]schema.FieldMapping{}
	for _, f := range e.fields {
		out[f] = schema.FieldMapping{Column: "metadata", JSONPath: "custom." + f}
	}
	return out
}

func testComponents() schema.Components {
	return schema.Components{
		"product":        productEntity{},
		"product_custom": productCustomEntity{fields: []string{"test1", "test2", "someField", "hfss"}},
	}
}

// resolvedSQL reconstructs a fully value-inlined SQL string from a Result by
// substituting each @placeholder with its quoted argument value, so tests
// can compare against the fixtures' literal-inlined expected strings
// (adjusted for Postgres identifier/JSON syntax) regardless of exactly how
// the walker named its synthesized placeholders.
func resolvedSQL(res *Result) string {
	return placeholderRe.ReplaceAllStringFunc(res.SQL, func(m string) string {
		v, ok := res.Args[m[1:]]
		if !ok {
			return m
		}
		if s, ok := v.(string); ok {
			return "'" + strings.ReplaceAll(s, "'", "''") + "'"
		}
		return fmt.Sprintf("%v", v)
	})
}

// simpleProvider() cases, translating expected SQL from MySQL/literal-inlined
// form to PostgreSQL syntax (double-quoted identifiers, `#>>` JSONB
// extraction, dialect-translated date functions).
func TestSimpleFixtures(t *testing.T) {
	components := testComponents()
	params := WithParams(map[string]any{"testBrand": "Samsung"})

	cases := []struct {
		src  string
		want string
	}{
		{`product.sku = "test-product-0001"`, `("products"."sku" = 'test-product-0001')`},
		{
			`product.sku in ("test-product-0001", "test-product-0002", "test-product-0003")`,
			`("products"."sku" IN ('test-product-0001', 'test-product-0002', 'test-product-0003'))`,
		},
		{
			`product.brand in ("Samsung", "Apple")`,
			`("products"."metadata" #>> '{brand}' IN ('Samsung', 'Apple'))`,
		},
		{
			`product.price > 100 and product.sale_price is null`,
			`("products"."price" > 100 AND "products"."sale_price" IS NULL)`,
		},
		{`product.price between 100 and 200`, `("products"."price" BETWEEN 100 AND 200)`},
		{`product.price not between 100 and 200`, `("products"."price" NOT BETWEEN 100 AND 200)`},
		{
			`product.price between abs(product.sale_price) and product.price`,
			`("products"."price" BETWEEN ABS("products"."sale_price") AND "products"."price")`,
		},
		{`product.price > 1 + 30 / 2 * 5`, `("products"."price" > 1 + 30 / 2 * 5)`},
		{
			`case when product.price = 10 then "match" ELSE "not_match" end = "match"`,
			`(CASE WHEN "products"."price" = 10 THEN 'match' ELSE 'not_match' END = 'match')`,
		},
		{
			`case product.price when 20 then "match" else "not_match" end = "match"`,
			`(CASE "products"."price" WHEN 20 THEN 'match' ELSE 'not_match' END = 'match')`,
		},
		{
			`(product.brand = "Samsung" or product.brand = "Apple") and product.sale_price is not null`,
			`(("products"."metadata" #>> '{brand}' = 'Samsung' OR "products"."metadata" #>> '{brand}' = 'Apple') AND "products"."sale_price" IS NOT NULL)`,
		},
		{`abs(product.price) > 0`, `(ABS("products"."price") > 0)`},
		{`trim(product.brand) = "Test"`, `(TRIM("products"."metadata" #>> '{brand}') = 'Test')`},
		{
			`trim(both " " from product.brand) = "Test"`,
			`(TRIM(BOTH ' ' FROM "products"."metadata" #>> '{brand}') = 'Test')`,
		},
		{
			`trim(leading " " from product.sku) = "Test"`,
			`(TRIM(LEADING ' ' FROM "products"."sku") = 'Test')`,
		},
		{
			`trim(trailing " " from product.sku) = "Test"`,
			`(TRIM(TRAILING ' ' FROM "products"."sku") = 'Test')`,
		},
		{
			`sqrt(product.price + 1) = length(product.brand)`,
			`(SQRT("products"."price" + 1) = LENGTH("products"."metadata" #>> '{brand}'))`,
		},
		{`product.brand = :testBrand`, `("products"."metadata" #>> '{brand}' = 'Samsung')`},
		{`product.brand like "test%"`, `("products"."metadata" #>> '{brand}' LIKE 'test%')`},
		{`product.brand like :testBrand`, `("products"."metadata" #>> '{brand}' LIKE 'Samsung')`},
		{
			`product.sku like substring(product.brand, 2, 5)`,
			`("products"."sku" LIKE SUBSTRING("products"."metadata" #>> '{brand}', 2, 5))`,
		},
		{
			`product.sku like product.brand escape "#"`,
			`("products"."sku" LIKE "products"."metadata" #>> '{brand}' ESCAPE '#')`,
		},
		{
			`coalesce(product.sale_price, product.price, 0) > 0`,
			`(COALESCE("products"."sale_price", "products"."price", 0) > 0)`,
		},
		{`nullif("test", product.price) is not null`, `(NULLIF('test', "products"."price") IS NOT NULL)`},
		{
			`concat(product.sku, "-", product.price) = "111"`,
			`(CONCAT("products"."sku", '-', "products"."price") = '111')`,
		},
		{`product_custom.hfss = true`, `("products"."metadata" #>> '{custom,hfss}' = 'true')`},
		{`product_custom.test1 = 5`, `("products"."metadata" #>> '{custom,test1}' = '5')`},
		{`product_custom.test1 between 1 and 10`, `("products"."metadata" #>> '{custom,test1}' BETWEEN '1' AND '10')`},
		{`product_custom.test1 in (1, 2, 3)`, `("products"."metadata" #>> '{custom,test1}' IN ('1', '2', '3'))`},
		{
			`date_add(product.sale_price, 10, "year") > "2025-01-01"`,
			`(("products"."sale_price" + (10 || ' YEAR')::interval) > '2025-01-01 00:00:00')`,
		},
		{
			`date_sub(current_timestamp(), 10, "year") > "2025-01-01"`,
			`((CURRENT_TIMESTAMP - (10 || ' YEAR')::interval) > '2025-01-01 00:00:00')`,
		},
		{
			`date_diff(product.sale_price, current_date()) > 10`,
			`(("products"."sale_price"::date - CURRENT_DATE::date) > 10)`,
		},
		{
			`locate("10:", current_time(), 0) = 0`,
			`((CASE WHEN STRPOS(SUBSTRING(CURRENT_TIME FROM (0)+1), '10:') = 0 THEN 0 ELSE STRPOS(SUBSTRING(CURRENT_TIME FROM (0)+1), '10:') + (0) END) = 0)`,
		},
		{
			`lower(product.sku) = "test-100" and upper(product.sku) = "TEST-100"`,
			`(LOWER("products"."sku") = 'test-100' AND UPPER("products"."sku") = 'TEST-100')`,
		},
		{
			`bit_and(product.price, 200) = 0 and bit_or(product.price, 300) = 1`,
			`("products"."price" & 200 = 0 AND "products"."price" | 300 = 1)`,
		},
		{`mod(product.price, 2) = 0`, `(MOD("products"."price", 2) = 0)`},
		{`(2 + 2) * 2 = -6`, `((2 + 2) * 2 = -6)`},
		{
			`(product.price > 0) and not (product.sale_price > 0)`,
			`(("products"."price" > 0) AND NOT ("products"."sale_price" > 0))`,
		},
	}

	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			res, err := Compile(c.src, components, params)
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

func TestDatetimeFixture(t *testing.T) {
	loc, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	components := testComponents()

	res, err := Compile(`product.refreshed_at > "2024-09-02 10:00:00"`, components, WithTimezone(loc))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	want := `("products"."refreshed_at" > '2024-09-02 00:00:00')`
	if got := resolvedSQL(res); got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

// failedProvider(): each of
// these must fail to compile.
func TestFailedFixtures(t *testing.T) {
	components := testComponents()

	cases := []string{
		`product.sale_price = null`,
		`product.sale_price = 20 AN product.price = 10`,
		`product_custo.hffs = "yes"`,
		`product_custom.hffs = "yes"`,
		`product.price in (select id from products)`,
		`1 = ї`,
	}

	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			if _, err := Compile(src, components); err == nil {
				t.Fatalf("Compile(%q): expected an error, got none", src)
			}
		})
	}

	t.Run("date_add invalid unit", func(t *testing.T) {
		if _, err := Compile(`date_add(product.sale_price, 10, "years") > "2025-01-01"`, components); err == nil {
			t.Fatal("expected an error for an invalid DATE_ADD unit")
		}
	})
	t.Run("date_sub invalid unit", func(t *testing.T) {
		if _, err := Compile(`date_sub(product.sale_price, 10, "years") > "2025-01-01"`, components); err == nil {
			t.Fatal("expected an error for an invalid DATE_SUB unit")
		}
	})
}

// tallied by resolved database column, so two logical fields backed by the
// same JSON column both count against that one column.
func TestUsedFields(t *testing.T) {
	components := testComponents()
	src := `product.sku in ("test-001", "test-002") and product.price > 200 and product.brand = "Samsung" ` +
		`and product_custom.hfss = "yes"`

	res, err := Compile(src, components)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if len(res.UsedFields) != 1 {
		t.Fatalf("UsedFields tables = %v, want just [products]", res.UsedFields)
	}
	usage, ok := res.UsedFields["products"]
	if !ok {
		t.Fatalf("UsedFields missing 'products': %v", res.UsedFields)
	}

	want := map[string]int{"sku": 1, "price": 1, "metadata": 2}
	if len(usage.Fields) != len(want) {
		t.Fatalf("usage.Fields = %v, want %v", usage.Fields, want)
	}
	for k, v := range want {
		if usage.Fields[k] != v {
			t.Errorf("usage.Fields[%q] = %d, want %d", k, usage.Fields[k], v)
		}
	}
}

func TestReservedParamPrefixRejected(t *testing.T) {
	components := testComponents()
	_, err := Compile(`product.sku = :ql_evil`, components, WithParams(map[string]any{"ql_evil": "x"}))
	if err == nil {
		t.Fatal("expected an error for a parameter name using the reserved 'ql_' prefix")
	}
}

func TestPositional(t *testing.T) {
	components := testComponents()
	res, err := Compile(`product.sku = :sku and product.name = :name`, components,
		WithParams(map[string]any{"sku": "abc-123", "name": "Widget"}))
	if err != nil {
		t.Fatal(err)
	}
	sql, args := res.Positional()
	if !strings.Contains(sql, "$1") || !strings.Contains(sql, "$2") {
		t.Fatalf("Positional SQL = %q, want $1/$2 placeholders", sql)
	}
	if len(args) != 2 {
		t.Fatalf("Positional args = %v, want 2 values", args)
	}
}
