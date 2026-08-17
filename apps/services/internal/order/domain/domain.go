// Package domain defines the core entities and interfaces for the order bounded context.
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

// OrderStatus represents the state of an order.
type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusPaid      OrderStatus = "paid"
	OrderStatusCancelled OrderStatus = "cancelled"
)

// Order maps to the orders table in MariaDB.
type Order struct {
	ID        int         `json:"id"         gorm:"primaryKey;autoIncrement"`
	UserID    int         `json:"user_id"    gorm:"column:user_id;not null"`
	ProductID string      `json:"product_id" gorm:"column:product_id;type:varchar(32);not null"`
	Status    OrderStatus `json:"status"     gorm:"column:status;type:enum('pending','paid','cancelled');default:'pending';not null"`
	CreatedAt time.Time   `json:"created_at" gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time   `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
}

// TableName overrides GORM's default table name.
func (Order) TableName() string { return "orders" }

// Product maps to the products table in MariaDB (Premier League jerseys).
type Product struct {
	ID        string    `json:"id"         gorm:"primaryKey;type:varchar(32)"`
	Name      string    `json:"name"       gorm:"column:name;type:varchar(128);not null"`
	Price     float64   `json:"price"      gorm:"column:price;type:decimal(10,2);not null"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
}

// TableName overrides GORM's default table name.
func (Product) TableName() string { return "products" }

// CreateOrderRequest is the input for creating an order.
type CreateOrderRequest struct {
	ProductID string `json:"product_id" binding:"required"`
}

// OrderResponse is the public API response shape.
type OrderResponse struct {
	ID        int         `json:"id"`
	ProductID string      `json:"product_id"`
	Status    OrderStatus `json:"status"`
}

// UpdateStatusRequest is the body for PATCH /internal/orders/:id.
type UpdateStatusRequest struct {
	Status OrderStatus `json:"status" binding:"required"`
}

// OrderRepository abstracts order + product persistence. The gorm
// implementation is internal/order/repository.
type OrderRepository interface {
	ListProducts(ctx context.Context) ([]Product, error)
	CheckProductAvailability(ctx context.Context, productID string) (*Product, error)
	CreateOrderTx(ctx context.Context, order *Order) error
	ListByUserID(ctx context.Context, userID int) ([]Order, error)
	FindByID(ctx context.Context, id int) (*Order, error)
	UpdateStatus(ctx context.Context, id int, status OrderStatus) (*Order, error)
}

// ProductCache abstracts the product-list cache. GetBytes returns
// ErrCacheMiss on a miss; any other error means the cache is unreachable and
// the caller falls through to the database.
type ProductCache interface {
	GetBytes(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}
