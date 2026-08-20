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

// productPrice is the jersey price CheckProductAvailability answers with —
// the value CreateOrder and GetOrder must both hand back as "amount", since
// neither payment-svc's amount nor orders itself keep an amount column of
// their own.
const productPrice = 3290.0

type fakeRepo struct {
	availErr   error
	createHook func(order *domain.Order) error
	findOrder  *domain.Order
	findErr    error
}

func (f *fakeRepo) CheckProductAvailability(context.Context, string) (*domain.Product, error) {
	if f.availErr != nil {
		return nil, f.availErr
	}
	return &domain.Product{ID: "LIV-H-24", Price: productPrice}, nil
}

func (f *fakeRepo) CreateOrderTx(_ context.Context, order *domain.Order) error {
	order.ID = 7 // what MariaDB's autoIncrement would do
	if f.createHook != nil {
		return f.createHook(order)
	}
	return nil
}

func (f *fakeRepo) ListByUserID(context.Context, int) ([]domain.Order, error) { return nil, nil }

func (f *fakeRepo) FindByID(context.Context, int) (*domain.Order, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.findOrder, nil
}

func (f *fakeRepo) UpdateStatus(context.Context, int, domain.OrderStatus) (*domain.Order, error) {
	return nil, nil
}

func TestCreateOrder(t *testing.T) {
	uc := New(&fakeRepo{})

	order, amount, err := uc.CreateOrder(context.Background(), 1, domain.CreateOrderRequest{ProductID: "LIV-H-24"})
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
	if amount != productPrice {
		t.Errorf("amount = %v, want %v from the product row — this is what payment-svc will charge", amount, productPrice)
	}
}

// An order for a product that does not exist must never reach the insert —
// the FK would reject it, but failing at the check is a better error.
func TestCreateOrderUnknownProduct(t *testing.T) {
	uc := New(&fakeRepo{availErr: errors.New(`product "X" not found`)})

	if _, _, err := uc.CreateOrder(context.Background(), 1, domain.CreateOrderRequest{ProductID: "X"}); err == nil {
		t.Error("unknown product accepted, want rejection")
	}
}

// The insert failing (DB down mid-transaction) must surface, not return a
// half-created order.
func TestCreateOrderInsertFails(t *testing.T) {
	uc := New(&fakeRepo{createHook: func(*domain.Order) error { return errors.New("insert failed") }})

	if _, _, err := uc.CreateOrder(context.Background(), 1, domain.CreateOrderRequest{ProductID: "LIV-H-24"}); err == nil {
		t.Error("insert failure swallowed, want error")
	}
}

// GetOrder backs GET /internal/orders/:id, the call payment-svc makes right
// before charging. Its amount must come from the same product join as
// CreateOrder — a second, independent computation is exactly how the two
// could drift.
func TestGetOrderJoinsProductPrice(t *testing.T) {
	repo := &fakeRepo{findOrder: &domain.Order{ID: 12, UserID: 1, ProductID: "LIV-H-24", Status: domain.OrderStatusPending}}
	uc := New(repo)

	order, amount, err := uc.GetOrder(context.Background(), 12)
	if err != nil {
		t.Fatalf("GetOrder error: %v", err)
	}
	if order.ID != 12 {
		t.Errorf("order id = %d, want 12", order.ID)
	}
	if amount != productPrice {
		t.Errorf("amount = %v, want %v joined from products.price", amount, productPrice)
	}
}

// A missing order must surface as an error, not a zero-value order silently
// paired with amount 0 — the caller (payment-svc's Validate) treats that as a
// decode success, not a 404.
func TestGetOrderMissingOrderPropagatesError(t *testing.T) {
	repo := &fakeRepo{findErr: errors.New("record not found")}
	uc := New(repo)

	if _, _, err := uc.GetOrder(context.Background(), 999); err == nil {
		t.Error("missing order accepted, want error")
	}
}

// If the product an existing order references has since vanished, GetOrder
// must fail rather than answer amount 0 — a payment-svc caller reading that
// as a valid price would settle the order for nothing.
func TestGetOrderProductGoneSurfacesError(t *testing.T) {
	repo := &fakeRepo{
		findOrder: &domain.Order{ID: 12, ProductID: "GONE"},
		availErr:  errors.New(`product "GONE" not found`),
	}
	uc := New(repo)

	if _, _, err := uc.GetOrder(context.Background(), 12); err == nil {
		t.Error("order with a deleted product accepted, want error")
	}
}
