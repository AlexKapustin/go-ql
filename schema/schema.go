// Package schema provides the field-whitelisting abstraction that replaces
// a caller declares, per alias used in
// a query string, which table it refers to and which fields are queryable
// (and how each field maps onto an actual column, including columns backed
// by a JSON/JSONB blob).
package schema

// FieldMapping describes how a logical query-language field maps onto an
// actual database column.
type FieldMapping struct {
	// Column is the real column name backing this field.
	Column string
	// JSONPath, when non-empty, means Column holds a JSON/JSONB document
	// and the field's value lives at this dot-delimited path within it
	// (e.g. "custom.hfss" for a nested key). Empty means Column is a plain
	// scalar column.
	JSONPath string
}

// TableMetadata declares a table and its whitelisted, queryable fields. A
// hand-written implementation is the v1 way to declare a schema (mirroring
// it is not derived from a GORM model automatically.
type TableMetadata interface {
	// TableName is the real SQL table name.
	TableName() string
	// Fields maps a logical, queryable field name to its column mapping.
	// A field absent from this map is rejected as unknown/unauthorized.
	Fields() map[string]FieldMapping
}

// Components maps a query-language alias (as used in "alias.field" path
// expressions) to the table metadata it refers to. This is the symbol table
// a query is resolved and validated against
type Components map[string]TableMetadata

// FieldUsage tallies how many times each column of a table was referenced
// while walking a parsed query
type FieldUsage struct {
	Table  TableMetadata
	Fields map[string]int // logical field name -> reference count
}
