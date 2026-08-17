package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/domain"
)

// The product-list cache-aside tests moved to catalog/usecase with the code
// when the two contexts split. What is left here is the order lifecycle:
// creation guards and the transitions the settle path drives.

type fakeRepo struct {
	availErr   error
	createHook func(order *domain.Order) error
}

func (f *fakeRepo) CheckProductAvailability(context.Context, string) (*domain.Product, error) {
	if f.availErr != nil {
		return nil, f.availErr
	}
	return &domain.Product{ID: "LIV-H-24"}, nil
}

func (f *fakeRepo) CreateOrderTx(_ context.Context, order *domain.Order) error {
	order.ID = 7 // what MariaDB's autoIncrement would do
	if f.createHook != nil {
		return f.createHook(order)
	}
	return nil
}

func (f *fakeRepo) ListByUserID(context.Context, int) ([]domain.Order, error) { return nil, nil }
func (f *fakeRepo) FindByID(context.Context, int) (*domain.Order, error)      { return nil, nil }
func (f *fakeRepo) UpdateStatus(context.Context, int, domain.OrderStatus) (*domain.Order, error) {
	return nil, nil
}

func TestCreateOrder(t *testing.T) {
	uc := New(&fakeRepo{})

	order, err := uc.CreateOrder(context.Background(), 1, domain.CreateOrderRequest{ProductID: "LIV-H-24"})
	if err != nil {
		t.Fatalf("CreateOrder error: %v", err)
	}
	if order.ID != 7 {
		t.Errorf("order id = %d, want 7 from the insert", order.ID)
	}
	if order.Status != domain.OrderStatusPending {
		t.Errorf("status = %q, want pending — payment-svc validates on it", order.Status)
	}
	if order.UserID != 1 || order.ProductID != "LIV-H-24" {
		t.Errorf("order = %+v, want user 1 and the requested product", order)
	}
}

// An order for a product that does not exist must never reach the insert —
// the FK would reject it, but failing at the check is a better error.
func TestCreateOrderUnknownProduct(t *testing.T) {
	uc := New(&fakeRepo{availErr: errors.New(`product "X" not found`)})

	if _, err := uc.CreateOrder(context.Background(), 1, domain.CreateOrderRequest{ProductID: "X"}); err == nil {
		t.Error("unknown product accepted, want rejection")
	}
}

// The insert failing (DB down mid-transaction) must surface, not return a
// half-created order.
func TestCreateOrderInsertFails(t *testing.T) {
	uc := New(&fakeRepo{createHook: func(*domain.Order) error { return errors.New("insert failed") }})

	if _, err := uc.CreateOrder(context.Background(), 1, domain.CreateOrderRequest{ProductID: "LIV-H-24"}); err == nil {
		t.Error("insert failure swallowed, want error")
	}
}
