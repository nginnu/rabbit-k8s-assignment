// Package domain defines the catalog entities and the interfaces around them.
package domain

import (
	"context"
	"errors"
	"time"
)

// ErrCacheMiss is what a ProductCache returns on a miss. The sentinel lives
// here rather than exposing go-redis's redis.Nil through the interface: the
// usecase decides what a miss means, the adapter decides how Redis says it.
var ErrCacheMiss = errors.New("cache miss")

// Product maps to the products table in MariaDB (Premier League jerseys).
type Product struct {
	ID        string    `json:"id"         gorm:"primaryKey;type:varchar(32)"`
	Name      string    `json:"name"       gorm:"column:name;type:varchar(128);not null"`
	Price     float64   `json:"price"      gorm:"column:price;type:decimal(10,2);not null"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
}

// TableName overrides GORM's default table name.
func (Product) TableName() string { return "products" }

// ProductRepository abstracts catalog persistence.
type ProductRepository interface {
	ListProducts(ctx context.Context) ([]Product, error)
}

// ProductCache abstracts the product-list cache. GetBytes returns
// ErrCacheMiss on a miss; any other error means the cache is unreachable and
// the caller falls through to the database.
type ProductCache interface {
	GetBytes(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}
