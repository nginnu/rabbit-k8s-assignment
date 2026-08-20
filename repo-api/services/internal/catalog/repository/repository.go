// Package repository implements persistence for the catalog.
package repository

import (
	"context"
	"fmt"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/catalog/domain"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"
)

var tracer = otel.Tracer("catalog/repository")

// ProductRepository reads the products table. Read-only by design: the
// catalog lists what the seed loaded; nothing in the checkout flow writes it.
type ProductRepository struct {
	db *gorm.DB
}

// New creates a new ProductRepository.
func New(db *gorm.DB) *ProductRepository {
	return &ProductRepository{db: db}
}

// ListProducts returns all products (Premier League jerseys).
func (r *ProductRepository) ListProducts(ctx context.Context) ([]domain.Product, error) {
	ctx, span := tracer.Start(ctx, "read products")
	defer span.End()

	// gorm's Find leaves a nil slice when zero rows come back. A nil slice
	// marshals to JSON null, and the frontend's Array.isArray(data) is false
	// for null — an empty catalog would render as a permanent "Loading
	// products…" instead of an empty grid. make(..., 0) keeps it non-nil.
	products := make([]domain.Product, 0)
	if err := r.db.WithContext(ctx).Order("id").Find(&products).Error; err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("list products: %w", err)
	}
	span.SetAttributes(attribute.Int("product.count", len(products)))
	return products, nil
}
