package ql

import (
	"gorm.io/gorm"

	"github.com/AlexKapustin/go-ql/schema"
)

// Where compiles src against components and returns a GORM scope applying
// it as a WHERE condition:
//
//	db.Scopes(ql.Where(`product.sku = :sku`, components,
//		ql.WithParams(map[string]any{"sku": "abc-123"}))).Find(&products)
//
// A compile error is recorded on db via AddError rather than panicking, so
// it surfaces through the normal *gorm.DB.Error path.
func Where(src string, components schema.Components, opts ...Option) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		res, err := Compile(src, components, opts...)
		if err != nil {
			db.AddError(err)
			return db
		}
		return db.Where(res.SQL, res.Args)
	}
}
