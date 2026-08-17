// Package repository implements persistence for the order bounded context.
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/domain"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

var tracer = otel.Tracer("order/repository")

// OrderRepository handles order + product DB operations.
type OrderRepository struct {
	db *gorm.DB
}

// New creates a new OrderRepository.
func New(db *gorm.DB) *OrderRepository {
	return &OrderRepository{db: db}
}

// ListProducts is gone with the split: catalog owns the product read
// path. The table is still queried below, but only inside order creation to
// validate the product_id before the insert.

// CheckProductAvailability verifies a product exists by ID.
// Custom span: check availability
func (r *OrderRepository) CheckProductAvailability(ctx context.Context, productID string) (*domain.Product, error) {
	ctx, span := tracer.Start(ctx, "check availability",
		trace.WithAttributes(attribute.String("product.id", productID)),
	)
	defer span.End()

	var p domain.Product
	if err := r.db.WithContext(ctx).Where("id = ?", productID).First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			span.SetAttributes(attribute.Bool("product.found", false))
			return nil, fmt.Errorf("product %q not found", productID)
		}
		span.RecordError(err)
		return nil, fmt.Errorf("query product: %w", err)
	}
	span.SetAttributes(attribute.Bool("product.found", true))
	return &p, nil
}

// CreateOrderTx inserts an order inside a transaction.
// Custom span: save order
func (r *OrderRepository) CreateOrderTx(ctx context.Context, order *domain.Order) error {
	ctx, span := tracer.Start(ctx, "save order",
		trace.WithAttributes(
			attribute.Int("user.id", order.UserID),
			attribute.String("order.product_id", order.ProductID),
		),
	)
	defer span.End()

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Re-check product inside transaction
		var p domain.Product
		if err := tx.WithContext(ctx).Where("id = ?", order.ProductID).First(&p).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				span.RecordError(err)
				return fmt.Errorf("product %q not found", order.ProductID)
			}
			span.RecordError(err)
			return fmt.Errorf("check product in tx: %w", err)
		}

		if err := tx.WithContext(ctx).Create(order).Error; err != nil {
			span.RecordError(err)
			return fmt.Errorf("insert order: %w", err)
		}

		span.SetAttributes(attribute.Int("order.id", order.ID))
		return nil
	})
}

// ListByUserID returns all orders for a given user.
func (r *OrderRepository) ListByUserID(ctx context.Context, userID int) ([]domain.Order, error) {
	var orders []domain.Order
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&orders).Error; err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return orders, nil
}

// FindByID returns a single order by ID or gorm.ErrRecordNotFound.
func (r *OrderRepository) FindByID(ctx context.Context, id int) (*domain.Order, error) {
	var o domain.Order
	if err := r.db.WithContext(ctx).First(&o, id).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

// UpdateStatus changes the status column for an order.
func (r *OrderRepository) UpdateStatus(ctx context.Context, id int, status domain.OrderStatus) (*domain.Order, error) {
	var o domain.Order
	if err := r.db.WithContext(ctx).First(&o, id).Error; err != nil {
		return nil, err
	}
	o.Status = status
	if err := r.db.WithContext(ctx).Save(&o).Error; err != nil {
		return nil, fmt.Errorf("update order status: %w", err)
	}
	return &o, nil
}
