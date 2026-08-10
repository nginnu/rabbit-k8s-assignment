// Package usecase implements business logic for the order bounded context.
package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/domain"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/repository"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("order/usecase")

const (
	productListCacheKey = "products:list"
	productListCacheTTL = 60 * time.Second
)

// OrderUsecase orchestrates order operations.
type OrderUsecase struct {
	repo  *repository.OrderRepository
	redis *redis.Client
}

// New creates a new OrderUsecase.
func New(repo *repository.OrderRepository, rdb *redis.Client) *OrderUsecase {
	return &OrderUsecase{repo: repo, redis: rdb}
}

// ListProducts returns all available products with cache-aside pattern.
//
// Spans:
//   usecase.list_products
//     ├─ cache.product.get  → HIT → return
//     │                     → MISS → continue
//     ├─ repo.product.list  (otelgorm SELECT)
//     └─ cache.product.set  (TTL 60s)
func (u *OrderUsecase) ListProducts(ctx context.Context) ([]domain.Product, error) {
	ctx, span := tracer.Start(ctx, "list products")
	defer span.End()

	// 1. Try cache (otelredis instruments GET as a span automatically)
	cached, err := u.redis.Get(ctx, productListCacheKey).Bytes()
	if err == nil {
		var products []domain.Product
		if jsonErr := json.Unmarshal(cached, &products); jsonErr == nil {
			span.SetAttributes(
				attribute.String("cache.result", "hit"),
				attribute.Int("product.count", len(products)),
			)
			return products, nil
		}
		// Cache value corrupt → fall through to DB
	} else if !errors.Is(err, redis.Nil) {
		// Network/Redis error — log but don't fail (degrade to DB)
		span.AddEvent("cache.error", trace.WithAttributes(attribute.String("err", err.Error())))
	}

	span.SetAttributes(attribute.String("cache.result", "miss"))

	// 2. Cache miss → query DB
	products, err := u.repo.ListProducts(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	// 3. Write back to cache (best effort — ไม่ fail ถ้า Redis SET พัง)
	if data, marshalErr := json.Marshal(products); marshalErr == nil {
		if setErr := u.redis.Set(ctx, productListCacheKey, data, productListCacheTTL).Err(); setErr != nil {
			span.AddEvent("cache.set_failed",
				trace.WithAttributes(attribute.String("err", setErr.Error())))
		}
	}

	span.SetAttributes(attribute.Int("product.count", len(products)))
	return products, nil
}

// CreateOrder validates input, checks product availability, and inserts an order.
// Custom span: create order
func (u *OrderUsecase) CreateOrder(ctx context.Context, userID int, req domain.CreateOrderRequest) (*domain.Order, error) {
	ctx, span := tracer.Start(ctx, "create order",
		trace.WithAttributes(
			attribute.Int("user.id", userID),
			attribute.String("order.product_id", req.ProductID),
		),
	)
	defer span.End()

	if _, err := u.repo.CheckProductAvailability(ctx, req.ProductID); err != nil {
		span.RecordError(err)
		return nil, err
	}

	order := &domain.Order{
		UserID:    userID,
		ProductID: req.ProductID,
		Status:    domain.OrderStatusPending,
	}
	if err := u.repo.CreateOrderTx(ctx, order); err != nil {
		span.RecordError(err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("order.id", order.ID))
	return order, nil
}

// ListUserOrders returns all orders for a user.
func (u *OrderUsecase) ListUserOrders(ctx context.Context, userID int) ([]domain.Order, error) {
	return u.repo.ListByUserID(ctx, userID)
}

// GetOrder returns a single order by ID.
func (u *OrderUsecase) GetOrder(ctx context.Context, id int) (*domain.Order, error) {
	return u.repo.FindByID(ctx, id)
}

// UpdateOrderStatus changes the status of an order.
func (u *OrderUsecase) UpdateOrderStatus(ctx context.Context, id int, status domain.OrderStatus) (*domain.Order, error) {
	return u.repo.UpdateStatus(ctx, id, status)
}
