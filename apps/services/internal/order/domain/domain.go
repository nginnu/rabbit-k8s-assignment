// Package domain defines the core entities and interfaces for the order bounded context.
// The product list moved to catalog with the split; Product stays because
// order creation still checks the product exists before inserting.
package domain

import (
	"context"
	"time"
)

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

// OrderRepository abstracts order + product persistence. The gorm
// implementation is internal/order/repository. Listing products is not here:
// that read path belongs to catalog since the split — this context only
// touches products to validate an order's product_id.
type OrderRepository interface {
	CheckProductAvailability(ctx context.Context, productID string) (*Product, error)
	CreateOrderTx(ctx context.Context, order *Order) error
	ListByUserID(ctx context.Context, userID int) ([]Order, error)
	FindByID(ctx context.Context, id int) (*Order, error)
	UpdateStatus(ctx context.Context, id int, status OrderStatus) (*Order, error)
}
