// Package usecase implements business logic for the order bounded context.
// The product list and its cache moved to catalog with the split; what is
// left is the order lifecycle: create, list, and the status transitions the
// payment settle path drives.
package usecase

import (
	"context"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/domain"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("order/usecase")

// OrderUsecase orchestrates order operations.
type OrderUsecase struct {
	repo domain.OrderRepository
}

// New creates a new OrderUsecase.
func New(repo domain.OrderRepository) *OrderUsecase {
	return &OrderUsecase{repo: repo}
}

// CreateOrder validates input, checks product availability, and inserts an
// order. It also returns the product's price: orders carries no amount
// column, so the price payment will later charge is resolved here, from
// the same CheckProductAvailability call that already guards the insert,
// and handed to the caller rather than recomputed a second time.
// Custom span: create order
func (u *OrderUsecase) CreateOrder(ctx context.Context, userID int, req domain.CreateOrderRequest) (*domain.Order, float64, error) {
	ctx, span := tracer.Start(ctx, "create order",
		trace.WithAttributes(
			attribute.Int("user.id", userID),
			attribute.String("order.product_id", req.ProductID),
		),
	)
	defer span.End()

	product, err := u.repo.CheckProductAvailability(ctx, req.ProductID)
	if err != nil {
		span.RecordError(err)
		return nil, 0, err
	}

	order := &domain.Order{
		UserID:    userID,
		ProductID: req.ProductID,
		Status:    domain.OrderStatusPending,
	}
	if err := u.repo.CreateOrderTx(ctx, order); err != nil {
		span.RecordError(err)
		return nil, 0, err
	}

	span.SetAttributes(attribute.Int("order.id", order.ID))
	return order, product.Price, nil
}

// ListUserOrders returns all orders for a user.
func (u *OrderUsecase) ListUserOrders(ctx context.Context, userID int) ([]domain.Order, error) {
	return u.repo.ListByUserID(ctx, userID)
}

// GetOrder returns a single order by ID together with the price of the
// product it references. There is no orders.amount column — this is the
// join payment's OrderAdapter relies on (in-process, see
// internal/payment/gateway/order_adapter.go) to learn what to charge, same
// as CreateOrder above.
func (u *OrderUsecase) GetOrder(ctx context.Context, id int) (*domain.Order, float64, error) {
	o, err := u.repo.FindByID(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	product, err := u.repo.CheckProductAvailability(ctx, o.ProductID)
	if err != nil {
		return nil, 0, err
	}
	return o, product.Price, nil
}

// UpdateOrderStatus changes the status of an order.
func (u *OrderUsecase) UpdateOrderStatus(ctx context.Context, id int, status domain.OrderStatus) (*domain.Order, error) {
	return u.repo.UpdateStatus(ctx, id, status)
}
