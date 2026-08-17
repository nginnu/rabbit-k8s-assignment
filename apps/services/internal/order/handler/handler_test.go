package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"context"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/domain"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/usecase"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/config"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/middleware"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// Handler tests go through the real Auth middleware and the real gin binding
// pipeline (not the usecase directly) so the amount field added to the
// order responses is proven on the wire, the shape a client actually sees.

const testSecret = "unit-test-secret"

// stubRepo implements domain.OrderRepository; CheckProductAvailability always
// answers the same priced jersey, since threading a real price through is
// what these tests are checking, not product lookup itself.
type stubRepo struct {
	availErr error
	order    *domain.Order
	findErr  error
	updated  domain.OrderStatus
}

const stubPrice = 3290.0

func (s *stubRepo) CheckProductAvailability(context.Context, string) (*domain.Product, error) {
	if s.availErr != nil {
		return nil, s.availErr
	}
	return &domain.Product{ID: "LIV-H-24", Price: stubPrice}, nil
}

func (s *stubRepo) CreateOrderTx(_ context.Context, order *domain.Order) error {
	order.ID = 12
	return nil
}

func (s *stubRepo) ListByUserID(context.Context, int) ([]domain.Order, error) { return nil, nil }

func (s *stubRepo) FindByID(context.Context, int) (*domain.Order, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	if s.order == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return s.order, nil
}

func (s *stubRepo) UpdateStatus(_ context.Context, _ int, status domain.OrderStatus) (*domain.Order, error) {
	if s.order == nil {
		return nil, gorm.ErrRecordNotFound
	}
	s.updated = status
	s.order.Status = status
	return s.order, nil
}

func signedToken(t *testing.T, userID int) string {
	t.Helper()
	claims := middleware.JWTClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
}

// newRouter mounts the same route shape as order's main: /orders behind
// Auth. /internal/orders/:id is gone with the payment-svc merge — payment
// reads and updates orders in-process now, through order/usecase directly,
// not over HTTP — so there is no route left here for it to hit.
func newRouter(repo *stubRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := config.Base{JWTSecret: testSecret}
	h := New(usecase.New(repo))

	public := r.Group("/", middleware.Auth(cfg))
	public.POST("/orders", h.CreateOrder)
	public.GET("/orders", h.ListOrders)
	return r
}

func do(r *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCreateOrderReturnsProductPriceAsAmount(t *testing.T) {
	r := newRouter(&stubRepo{})

	rec := do(r, http.MethodPost, "/orders", signedToken(t, 1), `{"product_id":"LIV-H-24"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID        int     `json:"id"`
		ProductID string  `json:"product_id"`
		Status    string  `json:"status"`
		Amount    float64 `json:"amount"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Amount != stubPrice {
		t.Errorf("amount = %v, want %v from the product row, not left off the response", resp.Amount, stubPrice)
	}
}

func TestCreateOrderRequiresAuth(t *testing.T) {
	r := newRouter(&stubRepo{})

	rec := do(r, http.MethodPost, "/orders", "", `{"product_id":"LIV-H-24"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestCreateOrderUnknownProductIs422(t *testing.T) {
	r := newRouter(&stubRepo{availErr: gorm.ErrRecordNotFound})

	rec := do(r, http.MethodPost, "/orders", signedToken(t, 1), `{"product_id":"NOPE"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

// GetOrder and UpdateOrderStatus (the usecase methods /internal/orders/:id
// used to front) are proven directly in usecase_test.go and, through
// OrderAdapter, in internal/payment/gateway/order_adapter_test.go — the HTTP
// route itself no longer exists for a handler test to hit.
