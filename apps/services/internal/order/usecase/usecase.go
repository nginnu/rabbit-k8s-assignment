// Package usecase implements business logic for the order bounded context.
// The product list and its cache moved to catalog-svc with the split; what is
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
