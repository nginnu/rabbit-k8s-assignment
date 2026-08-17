package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	out, err := NewPaymentGatewayClient(ts.URL).Charge(context.Background(), 3290, chaos)
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

	if _, err := NewPaymentGatewayClient(ts.URL).Charge(context.Background(), 100, ChaosHeaders{}); err != nil {
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

	if _, err := NewPaymentGatewayClient(ts.URL).Charge(context.Background(), 3290.50, ChaosHeaders{}); err != nil {
		t.Fatalf("Charge error: %v", err)
	}
	if !strings.Contains(string(body), `"amount":3290.5`) {
		t.Errorf("body = %s, want the amount as JSON", body)
	}
}

func TestChargeDeclinedSurfacesCodeAndBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"gateway declined"}`))
	}))
	defer ts.Close()

	_, err := NewPaymentGatewayClient(ts.URL).Charge(context.Background(), 100, ChaosHeaders{})
	if err == nil {
		t.Fatal("Charge returned nil on 500, want error")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "gateway declined") {
		t.Errorf("error = %v, want status code and body in message", err)
	}
}
