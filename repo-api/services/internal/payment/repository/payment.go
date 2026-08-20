// Package repository provides gorm-backed persistence for payments.
package repository

import (
	"context"
	"fmt"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/domain"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"gorm.io/gorm"
)

// Named to match the order package's <context>/<layer> convention now that
// there is no standalone payment-svc process for "payment-svc" to refer to.
var tracer = otel.Tracer("payment/repository")

// PaymentRepo wraps gorm DB for the payments table.
type PaymentRepo struct {
	db *gorm.DB
}

// New creates a PaymentRepo.
func New(db *gorm.DB) *PaymentRepo {
	return &PaymentRepo{db: db}
}

// CreatePending inserts a payment with status=pending.
func (r *PaymentRepo) CreatePending(ctx context.Context, orderID int, amount float64, method domain.PaymentMethod) (*domain.Payment, error) {
	ctx, span := tracer.Start(ctx, "record pending payment")
	defer span.End()

	p := domain.Payment{
		OrderID: orderID,
		Amount:  amount,
		Method:  method,
		Status:  domain.StatusPending,
	}
	if err := r.db.WithContext(ctx).Create(&p).Error; err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, fmt.Errorf("create payment: %w", err)
	}
	return &p, nil
}

// UpdateStatus sets payment status and optionally gateway_ref.
func (r *PaymentRepo) UpdateStatus(ctx context.Context, id int, status domain.PaymentStatus, gatewayRef string) error {
	ctx, span := tracer.Start(ctx, "update payment status")
	defer span.End()

	updates := map[string]interface{}{
		"status": status,
	}
	if gatewayRef != "" {
		updates["gateway_ref"] = gatewayRef
	}
	if err := r.db.WithContext(ctx).Model(&domain.Payment{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return fmt.Errorf("update payment: %w", err)
	}
	return nil
}

// FindByID loads a single payment.
func (r *PaymentRepo) FindByID(ctx context.Context, id int) (*domain.Payment, error) {
	ctx, span := tracer.Start(ctx, "read payment")
	defer span.End()

	var p domain.Payment
	if err := r.db.WithContext(ctx).First(&p, id).Error; err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, fmt.Errorf("find payment %d: %w", id, err)
	}
	return &p, nil
}
