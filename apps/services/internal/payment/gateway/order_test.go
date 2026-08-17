package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/domain"
)

// fakeOrderSvc records the incoming request and answers with the status the
// test chooses, so Validate/MarkPaid are asserted against what left the
// client, not just what came back.
type fakeOrderSvc struct {
	respStatus string // order status to answer Validate with, "" = 404
	respCode   int    // HTTP status to answer with
	method     string
	path       string
	body       string
}

func (f *fakeOrderSvc) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.method, f.path, f.body = r.Method, r.URL.Path, string(b)
		if f.respCode != 0 && f.respCode != http.StatusOK {
			w.WriteHeader(f.respCode)
			return
		}
		if f.respStatus == "" { // simulate a missing order
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(domain.OrderInfo{ID: 7, Status: f.respStatus})
	})
}

func TestValidateAcceptsPendingOrder(t *testing.T) {
	svc := &fakeOrderSvc{respStatus: "pending"}
	ts := httptest.NewServer(svc.handler())
	defer ts.Close()

	info, err := NewOrderClient(ts.URL).Validate(context.Background(), 7)
	if err != nil {
		t.Fatalf("Validate(pending) error: %v", err)
	}
	if info.Status != "pending" || info.ID != 7 {
		t.Errorf("info = %+v, want order 7 pending", info)
	}
	if svc.method != http.MethodGet || svc.path != "/internal/orders/7" {
		t.Errorf("request = %s %s, want GET /internal/orders/7", svc.method, svc.path)
	}
}

// Validate is the gate before any money moves: an already-paid order slipping
// through means a double charge, so the status check is asserted, not assumed.
func TestValidateRejectsNonPendingOrder(t *testing.T) {
	ts := httptest.NewServer((&fakeOrderSvc{respStatus: "paid"}).handler())
	defer ts.Close()

	_, err := NewOrderClient(ts.URL).Validate(context.Background(), 7)
	if err == nil {
		t.Fatal("Validate(paid) returned nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "pending") {
		t.Errorf("error %q does not say what status was expected", err.Error())
	}
}

func TestValidateRejectsNon200(t *testing.T) {
	ts := httptest.NewServer((&fakeOrderSvc{respCode: http.StatusNotFound}).handler())
	defer ts.Close()

	_, err := NewOrderClient(ts.URL).Validate(context.Background(), 99)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want non-200 surfaced with the code", err)
	}
}

func TestValidateRejectsGarbageJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json at all`))
	}))
	defer ts.Close()

	if _, err := NewOrderClient(ts.URL).Validate(context.Background(), 7); err == nil {
		t.Error("Validate returned nil error on garbage body, want decode error")
	}
}

func TestMarkPaidSendsPatchWithStatus(t *testing.T) {
	svc := &fakeOrderSvc{respStatus: "pending"}
	ts := httptest.NewServer(svc.handler())
	defer ts.Close()

	if err := NewOrderClient(ts.URL).MarkPaid(context.Background(), 7); err != nil {
		t.Fatalf("MarkPaid error: %v", err)
	}
	if svc.method != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", svc.method)
	}
	if svc.body != `{"status":"paid"}` {
		t.Errorf("body = %s, want {\"status\":\"paid\"}", svc.body)
	}
}

func TestMarkPaidSurfacesNon200(t *testing.T) {
	ts := httptest.NewServer((&fakeOrderSvc{respCode: http.StatusConflict}).handler())
	defer ts.Close()

	err := NewOrderClient(ts.URL).MarkPaid(context.Background(), 7)
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Errorf("error = %v, want PATCH failure surfaced with the code", err)
	}
}
