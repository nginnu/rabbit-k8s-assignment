package gateway

import (
	"context"
	"fmt"

	orderdomain "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/domain"
	orderusecase "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/usecase"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/domain"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// OrderAdapter satisfies usecase.OrderGateway by calling order's usecase
// in-process. It replaces the OrderClient that used to call order-svc's
// GET/PATCH /internal/orders/:id over HTTP — order and payment now share one
// process and one transaction boundary, so Validate and MarkPaid are
// function calls, not requests that can time out or decode wrong.
type OrderAdapter struct {
	uc *orderusecase.OrderUsecase
}

// NewOrderAdapter wraps the order usecase both services now share.
func NewOrderAdapter(uc *orderusecase.OrderUsecase) *OrderAdapter {
	return &OrderAdapter{uc: uc}
}

// Validate loads the order and rejects anything not pending — the in-process
// equivalent of the old GET /internal/orders/:id plus its status check.
// Amount still comes from orderUC.GetOrder's product join, the same value
// CreateOrder hands back, so payment charges whatever order says the order
// costs and never anything the caller supplied.
func (a *OrderAdapter) Validate(ctx context.Context, orderID int) (*domain.OrderInfo, error) {
	ctx, span := tracer.Start(ctx, "verify order exists")
	defer span.End()
	span.SetAttributes(attribute.Int("order.id", orderID))

	o, amount, err := a.uc.GetOrder(ctx, orderID)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, fmt.Errorf("get order: %w", err)
	}

	if o.Status != orderdomain.OrderStatusPending {
		err := fmt.Errorf("order status is %q, expected pending", o.Status)
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, err
	}

	return &domain.OrderInfo{ID: o.ID, Status: string(o.Status), Amount: amount}, nil
}

// MarkPaid transitions the order to paid — the in-process equivalent of the
// old PATCH /internal/orders/:id with {"status":"paid"}.
func (a *OrderAdapter) MarkPaid(ctx context.Context, orderID int) error {
	ctx, span := tracer.Start(ctx, "mark order paid")
	defer span.End()
	span.SetAttributes(attribute.Int("order.id", orderID))

	if _, err := a.uc.UpdateOrderStatus(ctx, orderID, orderdomain.OrderStatusPaid); err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return fmt.Errorf("update order status: %w", err)
	}
	return nil
}
