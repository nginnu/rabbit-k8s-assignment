package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/domain"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/gateway"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/usecase"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/config"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/middleware"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// Handler tests go through the real Auth middleware with a token signed by the
// same secret, because the claims flow (token → context → usecase input) is
// part of what breaks when someone "simplifies" the wiring.

const testSecret = "unit-test-secret"

type stubRepo struct {
	createErr error
	updateErr error
	find      *domain.Payment
	findErr   error
}

func (s *stubRepo) CreatePending(_ context.Context, orderID int, amount float64) (*domain.Payment, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &domain.Payment{ID: 42, OrderID: orderID, Amount: amount, Status: domain.StatusPending}, nil
}

func (s *stubRepo) UpdateStatus(_ context.Context, _ int, _ domain.PaymentStatus, _ string) error {
	return s.updateErr
}

func (s *stubRepo) FindByID(_ context.Context, _ int) (*domain.Payment, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	return s.find, nil
}

type stubOrder struct{ validateErr error }

func (s stubOrder) Validate(_ context.Context, _ int) (*domain.OrderInfo, error) {
	if s.validateErr != nil {
		return nil, s.validateErr
	}
	return &domain.OrderInfo{ID: 7, Status: "pending"}, nil
}
func (stubOrder) MarkPaid(context.Context, int) error { return nil }

type stubCharge struct{ err error }

func (s stubCharge) Charge(_ context.Context, _ float64, _ gateway.ChaosHeaders) (*domain.ChargeResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &domain.ChargeResult{Status: "ok", Ref: "pay-ref-1"}, nil
}

type stubNotify struct{}

func (stubNotify) Notify(context.Context, int, int, float64, int) error { return nil }

// signedToken mints the same JWT shape Auth parses.
func signedToken(t *testing.T, userID int) string {
	t.Helper()
	claims := middleware.JWTClaims{
		UserID:    userID,
		Username:  "alice",
		SessionID: "sess-1",
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

// newRouter mounts the payment endpoints behind Auth, the way payment-svc's
// main does, so a 401 here means what it means in production.
func newRouter(repo *stubRepo, charge stubCharge, order stubOrder) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := config.Base{JWTSecret: testSecret}
	h := New(usecase.New(repo, order, charge, stubNotify{}))
	authd := r.Group("/", middleware.Auth(cfg))
	authd.POST("/payments", h.CreatePayment)
	authd.GET("/payments/:id/success", h.PaymentSuccess)
	return r
}

func do(r *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCreatePaymentHappyPath(t *testing.T) {
	r := newRouter(&stubRepo{}, stubCharge{}, stubOrder{})

	rec := do(r, http.MethodPost, "/payments", signedToken(t, 1),
		`{"order_id":7,"amount":3290}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		PaymentID  int    `json:"payment_id"`
		Status     string `json:"status"`
		GatewayRef string `json:"gateway_ref"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PaymentID != 42 || resp.Status != "paid" || resp.GatewayRef != "pay-ref-1" {
		t.Errorf("response = %+v, want payment 42 paid with ref", resp)
	}
}

// 402, not 500: the charge was declined, which is a client-visible outcome the
// storefront shows, not a server fault. The payment_id must ride along so the
// failed attempt is traceable.
func TestCreatePaymentChargeDeclinedIs402(t *testing.T) {
	r := newRouter(&stubRepo{}, stubCharge{err: errors.New("gateway declined")}, stubOrder{})

	rec := do(r, http.MethodPost, "/payments", signedToken(t, 1),
		`{"order_id":7,"amount":3290}`)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		PaymentID int    `json:"payment_id"`
		Status    string `json:"status"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.PaymentID != 42 || resp.Status != "failed" {
		t.Errorf("response = %+v, want payment 42 failed", resp)
	}
}

func TestCreatePaymentUpstreamErrorIs500(t *testing.T) {
	r := newRouter(&stubRepo{}, stubCharge{}, stubOrder{validateErr: errors.New("order-svc down")})

	rec := do(r, http.MethodPost, "/payments", signedToken(t, 1),
		`{"order_id":7,"amount":3290}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 for a non-charge failure", rec.Code)
	}
}

func TestCreatePaymentRejectsBadBody(t *testing.T) {
	r := newRouter(&stubRepo{}, stubCharge{}, stubOrder{})

	cases := []struct {
		name string
		body string
	}{
		{"missing order_id", `{"amount":10}`},
		{"zero amount", `{"order_id":7,"amount":0}`},
		{"negative amount", `{"order_id":7,"amount":-1}`},
		{"garbage", `not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(r, http.MethodPost, "/payments", signedToken(t, 1), tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCreatePaymentRequiresAuth(t *testing.T) {
	r := newRouter(&stubRepo{}, stubCharge{}, stubOrder{})

	if rec := do(r, http.MethodPost, "/payments", "", `{"order_id":7,"amount":1}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", rec.Code)
	}

	wrong := signedToken(t, 1) + "x"
	if rec := do(r, http.MethodPost, "/payments", wrong, `{"order_id":7,"amount":1}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("tampered token: status = %d, want 401", rec.Code)
	}
}

func TestPaymentSuccessStates(t *testing.T) {
	paid := &domain.Payment{ID: 42, OrderID: 7, Status: domain.StatusPaid, GatewayRef: "pay-ref-1", Amount: 3290}
	pending := &domain.Payment{ID: 42, OrderID: 7, Status: domain.StatusPending}

	cases := []struct {
		name    string
		find    *domain.Payment
		findErr error
		want    int
	}{
		{"paid is 200", paid, nil, http.StatusOK},
		// 409, not 200: this endpoint is the completion signal — reporting an
		// unfinished payment as success would make every proof above it a lie.
		{"pending is 409", pending, nil, http.StatusConflict},
		{"missing is 404", nil, errors.New("not found"), http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRouter(&stubRepo{find: tc.find, findErr: tc.findErr}, stubCharge{}, stubOrder{})
			rec := do(r, http.MethodGet, "/payments/42/success", signedToken(t, 1), "")
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d; body: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestPaymentSuccessRejectsNonNumericID(t *testing.T) {
	r := newRouter(&stubRepo{find: &domain.Payment{ID: 42, Status: domain.StatusPaid}}, stubCharge{}, stubOrder{})

	rec := do(r, http.MethodGet, "/payments/abc/success", signedToken(t, 1), "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
