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

func (s *stubRepo) CreatePending(_ context.Context, orderID int, amount float64, method domain.PaymentMethod) (*domain.Payment, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &domain.Payment{ID: 42, OrderID: orderID, Amount: amount, Method: method, Status: domain.StatusPending}, nil
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

// stubOrder answers with the order-svc's word on price. 3290 is the fixture
// every test in this file expects to see charged unless it deliberately says
// otherwise — the whole point of the fix under test is that nothing in the
// request body can move this number.
type stubOrder struct {
	validateErr error
	amount      float64
}

func (s stubOrder) Validate(_ context.Context, _ int) (*domain.OrderInfo, error) {
	if s.validateErr != nil {
		return nil, s.validateErr
	}
	amount := s.amount
	if amount == 0 {
		amount = 3290
	}
	return &domain.OrderInfo{ID: 7, Status: "pending", Amount: amount}, nil
}
func (stubOrder) MarkPaid(context.Context, int) error { return nil }

// stubCharge is a pointer receiver so a test can read gotAmount back after
// the request completes — the only way to observe, from outside the usecase,
// what price actually reached the gateway.
type stubCharge struct {
	err       error
	gotAmount float64
	gotMethod domain.PaymentMethod
}

func (s *stubCharge) Charge(_ context.Context, amount float64, method domain.PaymentMethod, _ gateway.ChaosHeaders) (*domain.ChargeResult, error) {
	s.gotAmount = amount
	s.gotMethod = method
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
func newRouter(repo *stubRepo, charge *stubCharge, order stubOrder) *gin.Engine {
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
	r := newRouter(&stubRepo{}, &stubCharge{}, stubOrder{})

	rec := do(r, http.MethodPost, "/payments", signedToken(t, 1), `{"order_id":7,"method":"card"}`)
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

// This is the headline fix. The old handler read "amount" from the request
// body and charged it verbatim — a browser could post any price it liked.
// createPaymentRequest no longer has an Amount field at all, so a body that
// still sends one is bound and silently dropped; what reaches the gateway is
// whatever order-svc's Validate says the order costs (stubOrder answers
// 3290), never the 1 sent here.
func TestCreatePaymentIgnoresClientSuppliedAmount(t *testing.T) {
	charge := &stubCharge{}
	r := newRouter(&stubRepo{}, charge, stubOrder{amount: 3290})

	rec := do(r, http.MethodPost, "/payments", signedToken(t, 1), `{"order_id":7,"amount":1,"method":"card"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if charge.gotAmount != 3290 {
		t.Errorf("charged amount = %v, want 3290 from order-svc — a client-supplied amount must never reach the gateway", charge.gotAmount)
	}
}

// 402, not 500: the charge was declined, which is a client-visible outcome the
// storefront shows, not a server fault. The payment_id must ride along so the
// failed attempt is traceable.
func TestCreatePaymentChargeDeclinedIs402(t *testing.T) {
	r := newRouter(&stubRepo{}, &stubCharge{err: errors.New("gateway declined")}, stubOrder{})

	rec := do(r, http.MethodPost, "/payments", signedToken(t, 1), `{"order_id":7,"method":"card"}`)
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
	r := newRouter(&stubRepo{}, &stubCharge{}, stubOrder{validateErr: errors.New("order-svc down")})

	rec := do(r, http.MethodPost, "/payments", signedToken(t, 1), `{"order_id":7,"method":"card"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 for a non-charge failure", rec.Code)
	}
}

func TestCreatePaymentRejectsBadBody(t *testing.T) {
	r := newRouter(&stubRepo{}, &stubCharge{}, stubOrder{})

	cases := []struct {
		name string
		body string
	}{
		{"missing order_id", `{"method":"card"}`},
		{"zero order_id", `{"order_id":0,"method":"card"}`},
		{"garbage", `not json`},
		{"missing method", `{"order_id":7}`},
		{"empty method", `{"order_id":7,"method":""}`},
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

// A method outside the four the schema and the gateway both know about must
// die here, at the handler, never reach ChargeGateway.Charge. stubCharge
// would happily record whatever it is handed; the assertion on gotMethod
// being empty is what proves the handler never called it at all.
func TestCreatePaymentRejectsUnknownMethod(t *testing.T) {
	charge := &stubCharge{}
	r := newRouter(&stubRepo{}, charge, stubOrder{})

	cases := []string{"bitcoin", "CARD", "cash", " card"}
	for _, method := range cases {
		t.Run(method, func(t *testing.T) {
			body := `{"order_id":7,"method":"` + method + `"}`
			rec := do(r, http.MethodPost, "/payments", signedToken(t, 1), body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("method %q: status = %d, want 400; body: %s", method, rec.Code, rec.Body.String())
			}
		})
	}
	if charge.gotMethod != "" {
		t.Errorf("gateway was called with an unvalidated method %q", charge.gotMethod)
	}
}

// The four accepted channels, one call each, asserting the exact value that
// reaches ChargeGateway.Charge — not just that the request was accepted.
func TestCreatePaymentAcceptsAllFourMethods(t *testing.T) {
	methods := []domain.PaymentMethod{domain.MethodCard, domain.MethodTrueMoney, domain.MethodGWallet, domain.MethodCOD}
	for _, m := range methods {
		t.Run(string(m), func(t *testing.T) {
			charge := &stubCharge{}
			if m == domain.MethodCOD {
				charge.err = errors.New("cod settlement is not supported by this gateway")
			}
			r := newRouter(&stubRepo{}, charge, stubOrder{})

			body := `{"order_id":7,"method":"` + string(m) + `"}`
			rec := do(r, http.MethodPost, "/payments", signedToken(t, 1), body)

			if m == domain.MethodCOD {
				if rec.Code != http.StatusPaymentRequired {
					t.Fatalf("cod: status = %d, want 402; body: %s", rec.Code, rec.Body.String())
				}
			} else if rec.Code != http.StatusOK {
				t.Fatalf("%s: status = %d, want 200; body: %s", m, rec.Code, rec.Body.String())
			}
			if charge.gotMethod != m {
				t.Errorf("gateway saw method %q, want %q", charge.gotMethod, m)
			}
		})
	}
}

func TestCreatePaymentRequiresAuth(t *testing.T) {
	r := newRouter(&stubRepo{}, &stubCharge{}, stubOrder{})

	if rec := do(r, http.MethodPost, "/payments", "", `{"order_id":7}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", rec.Code)
	}

	wrong := signedToken(t, 1) + "x"
	if rec := do(r, http.MethodPost, "/payments", wrong, `{"order_id":7}`); rec.Code != http.StatusUnauthorized {
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
			r := newRouter(&stubRepo{find: tc.find, findErr: tc.findErr}, &stubCharge{}, stubOrder{})
			rec := do(r, http.MethodGet, "/payments/42/success", signedToken(t, 1), "")
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d; body: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestPaymentSuccessRejectsNonNumericID(t *testing.T) {
	r := newRouter(&stubRepo{find: &domain.Payment{ID: 42, Status: domain.StatusPaid}}, &stubCharge{}, stubOrder{})

	rec := do(r, http.MethodGet, "/payments/abc/success", signedToken(t, 1), "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
