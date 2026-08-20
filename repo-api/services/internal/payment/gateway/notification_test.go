package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// captured holds what the fake notification service saw, so the outgoing
// request can be asserted field by field instead of trusting a 200.
type captured struct {
	method string
	path   string
	ctype  string
	body   map[string]any
}

func captureHandler(sink chan captured, code int, respBody string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(b, &payload)
		sink <- captured{method: r.Method, path: r.URL.Path, ctype: r.Header.Get("Content-Type"), body: payload}
		w.WriteHeader(code)
		_, _ = io.WriteString(w, respBody)
	})
}

// If the request shape drifts (wrong path, plain-text content type, a renamed
// field), the notification service still answers 200 to a JSON body it can
// decode — the breakage would surface later as missing fields in the stored
// record, far from the code that caused it. So the wire format is asserted
// here, at the sender.
func TestNotifyRequestShape(t *testing.T) {
	sink := make(chan captured, 1)
	ts := httptest.NewServer(captureHandler(sink, http.StatusOK, `{"status":"received"}`))
	defer ts.Close()

	client := NewNotificationClient(ts.URL)
	// Argument order is paymentID then orderID; the wire body must be the
	// other way round in name, which is exactly the mix-up this test catches.
	if err := client.Notify(context.Background(), 2, 1, 19.99, 3); err != nil {
		t.Fatalf("Notify returned error on 200: %v", err)
	}

	got := <-sink
	if got.method != http.MethodPost {
		t.Errorf("method = %s, want POST", got.method)
	}
	if got.path != "/notify" {
		t.Errorf("path = %s, want /notify", got.path)
	}
	if got.ctype != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got.ctype)
	}
	for field, want := range map[string]float64{
		"order_id":   1,
		"payment_id": 2,
		"amount":     19.99,
		"user_id":    3,
	} {
		if v, ok := got.body[field].(float64); !ok || v != want {
			t.Errorf("body[%q] = %v, want %v", field, got.body[field], want)
		}
	}
}

func TestNotifySuccessReturnsNil(t *testing.T) {
	sink := make(chan captured, 1)
	ts := httptest.NewServer(captureHandler(sink, http.StatusOK, `{"status":"received"}`))
	defer ts.Close()

	if err := NewNotificationClient(ts.URL).Notify(context.Background(), 1, 1, 1, 1); err != nil {
		t.Errorf("Notify returned error on 200: %v", err)
	}
	<-sink
}

// The body snippet in the error is what makes a failed notify debuggable from
// payment-svc's logs alone — without it the operator has to correlate two
// services to learn what the notification side already said.
func TestNotifyServerErrorIncludesStatusAndBody(t *testing.T) {
	sink := make(chan captured, 1)
	ts := httptest.NewServer(captureHandler(sink, http.StatusInternalServerError, `{"error":"boom"}`))
	defer ts.Close()

	err := NewNotificationClient(ts.URL).Notify(context.Background(), 1, 1, 1, 1)
	<-sink
	if err == nil {
		t.Fatal("Notify returned nil on 500, want error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not mention status 500", err.Error())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not include the response body snippet", err.Error())
	}
}

func TestNotifyServerDownReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := ts.URL
	ts.Close()

	if err := NewNotificationClient(url).Notify(context.Background(), 1, 1, 1, 1); err == nil {
		t.Error("Notify returned nil against a closed server, want connection error")
	}
}

// The 3s client timeout is the guarantee that a hung notification pod cannot
// stall a checkout response, so it is tested, not assumed — dropping the
// timeout to 30s would pass every other test in this file and only surface as
// checkout requests hanging in production.
func TestNotifyTimeoutCutsHungServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handler only returns after the client should already have hung
		// up, so the only way this test passes is the timeout firing.
		time.Sleep(4 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	start := time.Now()
	err := NewNotificationClient(ts.URL).Notify(context.Background(), 1, 1, 1, 1)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Notify returned nil against a hung server, want timeout error")
	}
	// Cut around 3s: not much earlier (that would be an unrelated connection
	// failure) and not later (that would mean nothing was enforcing the cap).
	if elapsed < 2900*time.Millisecond {
		t.Errorf("Notify failed after %v, too fast to be the 3s timeout; err: %v", elapsed, err)
	}
	if elapsed > 3500*time.Millisecond {
		t.Errorf("Notify took %v, the 3s timeout did not cut the request; err: %v", elapsed, err)
	}
}
