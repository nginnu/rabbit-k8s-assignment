package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/domain"
)

// The chaos headers are how Playwright (and the checkout test) steer
// the gateway's failure modes through payment-svc. If this forwarding breaks,
// every chaos test silently stops testing chaos and starts testing the happy
// path twice — so the headers are asserted at the sender, not at the gateway.
func TestChargeForwardsChaosHeaders(t *testing.T) {
	var gotRate, gotLatency, gotType string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRate = r.Header.Get("X-Chaos-Error-Rate")
		gotLatency = r.Header.Get("X-Chaos-Latency-Ms")
		gotType = r.Header.Get("X-Chaos-Error-Type")
		_, _ = w.Write([]byte(`{"status":"ok","ref":"pay-1"}`))
	}))
	defer ts.Close()

	chaos := ChaosHeaders{ErrorRate: "0.5", LatencyMs: "800", ErrorType: "timeout"}
	out, err := NewPaymentGatewayClient(ts.URL).Charge(context.Background(), 3290, domain.MethodCard, chaos)
	if err != nil {
		t.Fatalf("Charge error: %v", err)
	}
	if out.Ref != "pay-1" {
		t.Errorf("ref = %q, want pay-1", out.Ref)
	}
	if gotRate != "0.5" || gotLatency != "800" || gotType != "timeout" {
		t.Errorf("chaos headers = (%q, %q, %q), want (0.5, 800, timeout)",
			gotRate, gotLatency, gotType)
	}
}

// Empty chaos headers must not be sent at all: the gateway reads a present
// header as a real value, and an empty-but-present X-Chaos-Error-Rate parses
// as 0 — harmless today, but a footgun the moment the gateway defaults change.
func TestChargeOmitsEmptyChaosHeaders(t *testing.T) {
	var sawAny bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range []string{"X-Chaos-Error-Rate", "X-Chaos-Latency-Ms", "X-Chaos-Error-Type"} {
			if _, ok := r.Header[h]; ok {
				sawAny = true
			}
		}
		_, _ = w.Write([]byte(`{"status":"ok","ref":"pay-1"}`))
	}))
	defer ts.Close()

	if _, err := NewPaymentGatewayClient(ts.URL).Charge(context.Background(), 100, domain.MethodCard, ChaosHeaders{}); err != nil {
		t.Fatalf("Charge error: %v", err)
	}
	if sawAny {
		t.Error("empty chaos headers were sent")
	}
}

func TestChargeSendsAmountAsJSON(t *testing.T) {
	var body []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"status":"ok","ref":"pay-1"}`))
	}))
	defer ts.Close()

	if _, err := NewPaymentGatewayClient(ts.URL).Charge(context.Background(), 3290.50, domain.MethodCard, ChaosHeaders{}); err != nil {
		t.Fatalf("Charge error: %v", err)
	}
	if !strings.Contains(string(body), `"amount":3290.5`) {
		t.Errorf("body = %s, want the amount as JSON", body)
	}
	if !strings.Contains(string(body), `"method":"card"`) {
		t.Errorf("body = %s, want the method as JSON", body)
	}
}

// gatewayClientTimeout is deliberately generous — sized to outlast the pay
// page's own configurable chaos latency (up to 10s) rather than to enforce a
// tight SLA — so a wall-clock proof like TestValidateTimesOutOnHungServer in
// order_test.go would cost 15s+ of suite time for very little extra
// confidence: the enforcement mechanism (http.Client.Timeout) is already
// proven behaviorally there and in notification_test.go. What is specific to
// this constructor, and worth asserting directly, is that the constant is
// actually the one wired into the client.
func TestNewPaymentGatewayClientSetsBoundedTimeout(t *testing.T) {
	c := NewPaymentGatewayClient("http://example.invalid")
	if c.client.Timeout != gatewayClientTimeout {
		t.Errorf("client timeout = %v, want %v", c.client.Timeout, gatewayClientTimeout)
	}
}

func TestChargeDeclinedSurfacesCodeAndBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"gateway declined"}`))
	}))
	defer ts.Close()

	_, err := NewPaymentGatewayClient(ts.URL).Charge(context.Background(), 100, domain.MethodCard, ChaosHeaders{})
	if err == nil {
		t.Fatal("Charge returned nil on 500, want error")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "gateway declined") {
		t.Errorf("error = %v, want status code and body in message", err)
	}
}

// cod answers 402, which Charge treats the same as any other non-200: an
// error the usecase turns into a failed payment. The gateway's own decision
// to decline cod is proven in cmd/payment's own package (it has no test
// file today — the handler is exercised via tests/checkout.sh against the
// real binary); what belongs here is that this client does not special-case
// the status code and surfaces 402 like it would 500.
func TestChargeCODDeclinedSurfaces402(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"cod settlement is not supported by this gateway"}`))
	}))
	defer ts.Close()

	_, err := NewPaymentGatewayClient(ts.URL).Charge(context.Background(), 100, domain.MethodCOD, ChaosHeaders{})
	if err == nil {
		t.Fatal("Charge returned nil on 402, want error")
	}
	if !strings.Contains(err.Error(), "402") {
		t.Errorf("error = %v, want status code 402 in message", err)
	}
}
