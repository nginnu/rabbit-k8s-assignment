package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	orderdomain "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/domain"
	orderusecase "github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/usecase"
)

// stubOrderRepo implements orderdomain.OrderRepository directly — the same
// interface order's own handler tests stub — so OrderAdapter is proven
// against the real OrderUsecase, not a fake standing in for it. What used to
// be asserted at the wire (HTTP status codes, JSON decode errors) is now
// asserted at the call boundary that actually exists: what OrderAdapter
// hands back given what the repository returns.
type stubOrderRepo struct {
	order    *orderdomain.Order
	findErr  error
	availErr error
	updateErr error
	updated  orderdomain.OrderStatus
}

const stubPrice = 3290.0

func (s *stubOrderRepo) CheckProductAvailability(context.Context, string) (*orderdomain.Product, error) {
	if s.availErr != nil {
		return nil, s.availErr
	}
	return &orderdomain.Product{ID: "LIV-H-24", Price: stubPrice}, nil
}

func (s *stubOrderRepo) CreateOrderTx(context.Context, *orderdomain.Order) error { return nil }

func (s *stubOrderRepo) ListByUserID(context.Context, int) ([]orderdomain.Order, error) {
	return nil, nil
}

func (s *stubOrderRepo) FindByID(context.Context, int) (*orderdomain.Order, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	return s.order, nil
}

func (s *stubOrderRepo) UpdateStatus(_ context.Context, _ int, status orderdomain.OrderStatus) (*orderdomain.Order, error) {
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	s.updated = status
	if s.order != nil {
		s.order.Status = status
	}
	return s.order, nil
}

func newAdapter(repo *stubOrderRepo) *OrderAdapter {
	return NewOrderAdapter(orderusecase.New(repo))
}

func TestOrderAdapterValidateAcceptsPendingOrder(t *testing.T) {
	repo := &stubOrderRepo{order: &orderdomain.Order{ID: 7, ProductID: "LIV-H-24", Status: orderdomain.OrderStatusPending}}

	info, err := newAdapter(repo).Validate(context.Background(), 7)
	if err != nil {
		t.Fatalf("Validate(pending) error: %v", err)
	}
	if info.ID != 7 || info.Status != "pending" {
		t.Errorf("info = %+v, want order 7 pending", info)
	}
	// This is what ProcessPayment charges — dropping or zeroing it would
	// settle every order for free.
	if info.Amount != stubPrice {
		t.Errorf("info.Amount = %v, want %v from the product join", info.Amount, stubPrice)
	}
}

// Validate is the gate before any money moves: an already-paid order
// slipping through means a double charge, so the status check is asserted,
// not assumed.
func TestOrderAdapterValidateRejectsNonPendingOrder(t *testing.T) {
	repo := &stubOrderRepo{order: &orderdomain.Order{ID: 7, ProductID: "LIV-H-24", Status: orderdomain.OrderStatusPaid}}

	_, err := newAdapter(repo).Validate(context.Background(), 7)
	if err == nil {
		t.Fatal("Validate(paid) returned nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "pending") {
		t.Errorf("error %q does not say what status was expected", err.Error())
	}
}

func TestOrderAdapterValidateSurfacesMissingOrder(t *testing.T) {
	repo := &stubOrderRepo{findErr: errors.New("record not found")}

	if _, err := newAdapter(repo).Validate(context.Background(), 99); err == nil {
		t.Error("Validate against a missing order returned nil, want error")
	}
}

func TestOrderAdapterMarkPaidUpdatesStatus(t *testing.T) {
	repo := &stubOrderRepo{order: &orderdomain.Order{ID: 7, Status: orderdomain.OrderStatusPending}}

	if err := newAdapter(repo).MarkPaid(context.Background(), 7); err != nil {
		t.Fatalf("MarkPaid error: %v", err)
	}
	if repo.updated != orderdomain.OrderStatusPaid {
		t.Errorf("repo updated to %q, want paid", repo.updated)
	}
}

func TestOrderAdapterMarkPaidSurfacesRepoError(t *testing.T) {
	repo := &stubOrderRepo{order: &orderdomain.Order{ID: 7}, updateErr: errors.New("db down")}

	if err := newAdapter(repo).MarkPaid(context.Background(), 7); err == nil {
		t.Error("MarkPaid returned nil despite a repo error")
	}
}
