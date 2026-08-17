// Package usecase orchestrates the payment flow.
package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/domain"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/gateway"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
)

// Named to match the order package's <context>/<layer> convention now that
// there is no standalone payment-svc process for "payment-svc" to refer to.
var tracer = otel.Tracer("payment/usecase")

// ProcessPaymentInput is the input for ProcessPayment.
//
// There is deliberately no Amount field. The price the client saw when it
// posted this request is not trusted for anything: order's Validate call
// below is the only source ProcessPayment reads a price from.
//
// Method is different: unlike price, which channel pays is genuinely the
// caller's choice, so it does travel from the client. handler.CreatePayment
// has already validated it against the four known channels before this
// struct is built.
type ProcessPaymentInput struct {
	OrderID int
	UserID  int
	Method  domain.PaymentMethod
	Chaos   gateway.ChaosHeaders
}

// ProcessPaymentOutput is the result of a successful payment.
type ProcessPaymentOutput struct {
	PaymentID  int                  `json:"payment_id"`
	Status     domain.PaymentStatus `json:"status"`
	GatewayRef string               `json:"gateway_ref"`
}

// The four dependencies as interfaces, defined here on the consumer side
// rather than in domain: ChargeGateway's signature carries gateway.ChaosHeaders,
// and domain cannot import gateway without a cycle (gateway already imports
// domain for its DTOs). The concrete gorm repository, the in-process order
// adapter, and the HTTP clients satisfy these without knowing they exist;
// tests supply fakes that record calls, which is how the saga asserts step
// order, not just outcomes.
type (
	// PaymentRepository persists the payments table.
	PaymentRepository interface {
		CreatePending(ctx context.Context, orderID int, amount float64, method domain.PaymentMethod) (*domain.Payment, error)
		UpdateStatus(ctx context.Context, id int, status domain.PaymentStatus, gatewayRef string) error
		FindByID(ctx context.Context, id int) (*domain.Payment, error)
	}

	// OrderGateway is order's usecase, called in-process — see
	// internal/payment/gateway/order_adapter.go.
	OrderGateway interface {
		Validate(ctx context.Context, orderID int) (*domain.OrderInfo, error)
		MarkPaid(ctx context.Context, orderID int) error
	}

	// ChargeGateway is the external payment gateway.
	ChargeGateway interface {
		Charge(ctx context.Context, amount float64, method domain.PaymentMethod, chaos gateway.ChaosHeaders) (*domain.ChargeResult, error)
	}

	// Notifier is the notification service.
	Notifier interface {
		Notify(ctx context.Context, paymentID, orderID int, amount float64, userID int) error
	}
)

// PaymentUsecase contains the business logic for payments.
type PaymentUsecase struct {
	repo   PaymentRepository
	order  OrderGateway
	charge ChargeGateway
	notify Notifier
}

// New creates a PaymentUsecase.
func New(repo PaymentRepository, order OrderGateway, charge ChargeGateway, notify Notifier) *PaymentUsecase {
	return &PaymentUsecase{
		repo:   repo,
		order:  order,
		charge: charge,
		notify: notify,
	}
}

// ProcessPayment runs the full payment flow per PLAN section 9.3.
func (u *PaymentUsecase) ProcessPayment(ctx context.Context, in ProcessPaymentInput) (*ProcessPaymentOutput, error) {
	// order.id goes into baggage before the span is created, so payment-gateway
	// picks it up through otelhttp without being told; the in-process order
	// adapter needs no such propagation, it reads in.OrderID directly.
	ctx = withOrderBaggage(ctx, in.OrderID)

	ctx, span := tracer.Start(ctx, "process payment")
	defer span.End()

	span.SetAttributes(
		attribute.Int("order.id", in.OrderID),
		attribute.Int("user.id", in.UserID),
		attribute.String("payment.method", string(in.Method)),
	)

	slog.InfoContext(ctx, "payment started",
		"order_id", in.OrderID,
		"user_id", in.UserID,
		"method", in.Method,
	)

	// Step 1: Validate order exists and is pending. order's response is
	// also where the price comes from — info.Amount, derived server-side
	// from products.price, is what every later step charges. Nothing in
	// ProcessPaymentInput can override it because nothing in it carries a
	// price at all.
	info, err := u.order.Validate(ctx, in.OrderID)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		slog.ErrorContext(ctx, "order validation failed",
			"order_id", in.OrderID,
			"error", err,
		)
		return nil, fmt.Errorf("validate order: %w", err)
	}

	span.SetAttributes(attribute.Float64("payment.amount", info.Amount))
	slog.InfoContext(ctx, "order validated",
		"order_id", in.OrderID,
		"amount", info.Amount,
	)

	// Step 2: Create pending payment record.
	payment, err := u.repo.CreatePending(ctx, in.OrderID, info.Amount, in.Method)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		slog.ErrorContext(ctx, "create pending payment failed",
			"order_id", in.OrderID,
			"error", err,
		)
		return nil, fmt.Errorf("create pending: %w", err)
	}

	slog.InfoContext(ctx, "payment pending created",
		"payment_id", payment.ID,
		"order_id", in.OrderID,
	)

	// Step 3: Call the payment gateway to charge. A cod method fails here on
	// every call — gateway.PaymentGatewayClient.Charge treats the 402
	// payment-gateway answers with the same as any other non-200: this branch
	// already marks the payment failed and returns it below, so cod needs no
	// special case at all. TestProcessPaymentCODAlwaysFails pins that down.
	chargeResult, err := u.charge.Charge(ctx, info.Amount, in.Method, in.Chaos)
	if err != nil {
		// Gateway returned error (500 or 402) -- mark payment as failed.
		_ = u.repo.UpdateStatus(ctx, payment.ID, domain.StatusFailed, "")
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		slog.ErrorContext(ctx, "gateway charge failed",
			"payment_id", payment.ID,
			"method", in.Method,
			"error", err,
		)
		return &ProcessPaymentOutput{
			PaymentID: payment.ID,
			Status:    domain.StatusFailed,
		}, fmt.Errorf("charge failed: %w", err)
	}

	// Step 4: Charge succeeded -- update payment to paid with gateway ref.
	if err := u.repo.UpdateStatus(ctx, payment.ID, domain.StatusPaid, chargeResult.Ref); err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		slog.ErrorContext(ctx, "update payment status failed",
			"payment_id", payment.ID,
			"error", err,
		)
		return nil, fmt.Errorf("update status: %w", err)
	}

	slog.InfoContext(ctx, "payment charged",
		"payment_id", payment.ID,
		"gateway_ref", chargeResult.Ref,
	)

	// Step 5: Mark order as paid.
	if err := u.order.MarkPaid(ctx, in.OrderID); err != nil {
		// Non-fatal: payment succeeded, order update is best-effort.
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		slog.ErrorContext(ctx, "mark order paid failed (non-fatal)",
			"order_id", in.OrderID,
			"error", err,
		)
	}

	// Step 6: Notify the notification service.
	// Non-fatal, same reasoning as Step 5: the payment already cleared, so a
	// broken notification must never fail a checkout that succeeded.
	// The 3s timeout means a hanging notification cannot stall the checkout
	// response — the caller must not depend on the callee's configuration.
	notifyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := u.notify.Notify(notifyCtx, payment.ID, in.OrderID, info.Amount, in.UserID); err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		slog.ErrorContext(ctx, "notification send failed (non-fatal)",
			"order_id", in.OrderID,
			"payment_id", payment.ID,
			"error", err,
		)
	} else {
		slog.InfoContext(ctx, "notification sent",
			"order_id", in.OrderID,
			"payment_id", payment.ID,
		)
	}

	slog.InfoContext(ctx, "payment completed",
		"payment_id", payment.ID,
		"order_id", in.OrderID,
		"status", "paid",
	)

	return &ProcessPaymentOutput{
		PaymentID:  payment.ID,
		Status:     domain.StatusPaid,
		GatewayRef: chargeResult.Ref,
	}, nil
}

// withOrderBaggage adds order.id to the existing baggage without dropping what
// is already there, such as user.id from Auth. otelhttp injects it as a header.
func withOrderBaggage(ctx context.Context, orderID int) context.Context {
	existing := baggage.FromContext(ctx)
	member, err := baggage.NewMember("order.id", strconv.Itoa(orderID))
	if err != nil {
		return ctx
	}
	bag, err := existing.SetMember(member)
	if err != nil {
		return ctx
	}
	return baggage.ContextWithBaggage(ctx, bag)
}

// GetPayment loads a payment for the confirmation step.
//
// Ownership is not enforced here and deliberately so: payments carry no
// user_id, and order's Validate call returns only id, status, and amount, so
// there is nothing to check against. Any authenticated user could read
// another's receipt by guessing an id. Acceptable for a lab, unacceptable in
// production — closing it needs domain.OrderInfo to carry the owning user,
// which is a change to order's contract rather than something payments can
// fix alone.
func (u *PaymentUsecase) GetPayment(ctx context.Context, paymentID, userID int) (*domain.Payment, error) {
	ctx, span := tracer.Start(ctx, "confirm checkout")
	defer span.End()
	span.SetAttributes(
		attribute.Int("payment.id", paymentID),
		attribute.Int("user.id", userID),
	)

	p, err := u.repo.FindByID(ctx, paymentID)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	span.SetAttributes(attribute.String("payment.status", string(p.Status)))
	return p, nil
}
