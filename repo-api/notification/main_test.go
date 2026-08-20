package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testMux builds the real mux through newMux with the log sink the caller
// chooses: the log-burst test needs the buffer, everything else discards.
// Building the mux by hand here instead would drift from main and stop
// testing what ships.
func testMux(t *testing.T, log *slog.Logger) (*http.ServeMux, *chaos, *notifyStore) {
	t.Helper()
	c := &chaos{}
	notes := &notifyStore{}
	return newMux(log, c, notes), c, notes
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func postJSON(t *testing.T, mux *http.ServeMux, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func getJSON(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// --- notifyStore ---

func TestNotifyStoreAddSnapshot(t *testing.T) {
	s := &notifyStore{}
	in := notification{Status: "received", OrderID: 1, PaymentID: 2, Amount: 19.99, UserID: 3}
	s.add(in)

	got := s.snapshot()
	if len(got) != 1 {
		t.Fatalf("snapshot length = %d, want 1", len(got))
	}
	if got[0] != in {
		t.Errorf("snapshot record = %+v, want %+v", got[0], in)
	}
}

func TestNotifyStoreEmptySnapshot(t *testing.T) {
	s := &notifyStore{}
	if got := s.snapshot(); len(got) != 0 {
		t.Errorf("empty store snapshot length = %d, want 0", len(got))
	}
}

// The cap must drop the OLDEST record: if it dropped the newest, the checkout
// proof would silently lose the very notification it is there to catch.
func TestNotifyStoreCapDropsOldest(t *testing.T) {
	s := &notifyStore{}
	for i := 1; i <= maxNotifications+1; i++ {
		s.add(notification{OrderID: i})
	}

	got := s.snapshot()
	if len(got) != maxNotifications {
		t.Fatalf("snapshot length = %d, want %d", len(got), maxNotifications)
	}
	if got[0].OrderID != 2 {
		t.Errorf("oldest record survived: first OrderID = %d, want 2 (the first add)", got[0].OrderID)
	}
	if got[len(got)-1].OrderID != maxNotifications+1 {
		t.Errorf("newest record missing: last OrderID = %d, want %d", got[len(got)-1].OrderID, maxNotifications+1)
	}
}

// snapshot returns a copy: if it handed out the store's own backing array, a
// caller mutating the JSON-ready slice would corrupt later reads.
func TestNotifyStoreSnapshotIsCopy(t *testing.T) {
	s := &notifyStore{}
	s.add(notification{OrderID: 1})

	snap := s.snapshot()
	snap[0].OrderID = 999

	if got := s.snapshot(); got[0].OrderID != 1 {
		t.Errorf("mutating the returned slice changed the store: OrderID = %d, want 1", got[0].OrderID)
	}
}

// --- POST /notify ---

func TestNotifyValidBody(t *testing.T) {
	mux, _, notes := testMux(t, discardLogger())

	rec := postJSON(t, mux, "/notify", `{"order_id":11,"payment_id":22,"amount":19.99,"user_id":33}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Status    string `json:"status"`
		OrderID   int    `json:"order_id"`
		PaymentID int    `json:"payment_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "received" {
		t.Errorf("response status = %q, want %q", resp.Status, "received")
	}
	if resp.OrderID != 11 || resp.PaymentID != 22 {
		t.Errorf("response ids = (%d, %d), want (11, 22) echoed back", resp.OrderID, resp.PaymentID)
	}

	stored := notes.snapshot()
	if len(stored) != 1 {
		t.Fatalf("store length = %d, want 1", len(stored))
	}
	r := stored[0]
	if r.Status != "received" || r.OrderID != 11 || r.PaymentID != 22 || r.Amount != 19.99 || r.UserID != 33 {
		t.Errorf("stored record = %+v, want status received with ids/amount preserved", r)
	}
	if r.ReceivedAt.IsZero() {
		t.Error("stored record has no ReceivedAt")
	}
}

// amount and user_id are optional: a payload without them is still a proof a
// payment happened, so it must be accepted, not rejected for completeness.
func TestNotifyOptionalFieldsOmitted(t *testing.T) {
	mux, _, notes := testMux(t, discardLogger())

	rec := postJSON(t, mux, "/notify", `{"order_id":5,"payment_id":6}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	stored := notes.snapshot()
	if len(stored) != 1 {
		t.Fatalf("store length = %d, want 1", len(stored))
	}
	if stored[0].Status != "received" || stored[0].OrderID != 5 || stored[0].PaymentID != 6 {
		t.Errorf("stored record = %+v, want received with the sent ids", stored[0])
	}
}

// A record that cannot be tied back to a payment proves nothing, so a bad
// payload must be rejected AND left out of the store — otherwise
// GET /notifications would show a half-empty entry as if a call happened.
func TestNotifyRejectsBadPayloads(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing order_id", `{"payment_id":2}`},
		{"payment_id zero", `{"order_id":1,"payment_id":0}`},
		{"malformed JSON", `{"order_id":1,`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux, _, notes := testMux(t, discardLogger())
			rec := postJSON(t, mux, "/notify", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
			if got := notes.snapshot(); len(got) != 0 {
				t.Errorf("rejected payload was stored: %d records, want 0", len(got))
			}
		})
	}
}

// --- POST /notify-failed ---

// This endpoint exists to be SEEN failing: the 500 and the five-line error
// burst are the product, so both are asserted directly — the burst is what a
// Loki query catches, and it would be easy to break (wrong level, one line
// dropped) while every HTTP assertion still passed.
func TestNotifyFailed(t *testing.T) {
	var buf bytes.Buffer
	mux, _, notes := testMux(t, slog.New(slog.NewJSONHandler(&buf, nil)))

	rec := postJSON(t, mux, "/notify-failed", `{"order_id":7,"payment_id":8,"amount":5.5,"user_id":9}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Error     string `json:"error"`
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
		OrderID   int    `json:"order_id"`
		PaymentID int    `json:"payment_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != "notification delivery failed" {
		t.Errorf("error = %q, want %q", resp.Error, "notification delivery failed")
	}
	if resp.Code != "delivery_failed" {
		t.Errorf("code = %q, want %q", resp.Code, "delivery_failed")
	}
	if resp.Retryable {
		t.Error("retryable = true, want false")
	}
	if resp.OrderID != 7 || resp.PaymentID != 8 {
		t.Errorf("ids = (%d, %d), want (7, 8)", resp.OrderID, resp.PaymentID)
	}

	stored := notes.snapshot()
	if len(stored) != 1 {
		t.Fatalf("store length = %d, want 1", len(stored))
	}
	if stored[0].Status != "failed" || stored[0].OrderID != 7 || stored[0].PaymentID != 8 {
		t.Errorf("stored record = %+v, want status failed with the sent ids", stored[0])
	}

	// The log burst: exactly five ERROR lines, the first naming the failure.
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("log burst = %d lines, want 5; raw:\n%s", len(lines), buf.String())
	}
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line %d is not JSON: %v; raw: %s", i, err, line)
		}
		if rec["level"] != "ERROR" {
			t.Errorf("log line %d level = %v, want ERROR (line: %s)", i, rec["level"], line)
		}
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode first log line: %v", err)
	}
	if first["msg"] != "notification delivery failed" {
		t.Errorf("first log line msg = %v, want %q", first["msg"], "notification delivery failed")
	}
}

// --- GET /notifications ---

func TestGetNotifications(t *testing.T) {
	mux, _, _ := testMux(t, discardLogger())
	postJSON(t, mux, "/notify", `{"order_id":1,"payment_id":10}`)
	postJSON(t, mux, "/notify", `{"order_id":2,"payment_id":20}`)

	rec := getJSON(t, mux, "/notifications")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp struct {
		Count         int            `json:"count"`
		Notifications []notification `json:"notifications"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Count != 2 || len(resp.Notifications) != 2 {
		t.Fatalf("count = %d with %d records, want 2 and 2", resp.Count, len(resp.Notifications))
	}
	// In order: the checkout proof matches records to requests, so a store
	// that scrambled order would make the proof untrustworthy.
	if resp.Notifications[0].OrderID != 1 || resp.Notifications[1].OrderID != 2 {
		t.Errorf("records out of order: got [%d, %d], want [1, 2]",
			resp.Notifications[0].OrderID, resp.Notifications[1].OrderID)
	}
}

// --- chaos interplay ---

// With error rate 1.0 instrument fails the request BEFORE the handler runs.
// If the handler ran anyway, chaos would be failing requests whose side
// effect already happened — the store would hold a notification the caller
// was told never landed, and the checkout proof would lie.
func TestChaosFailsRequestBeforeHandler(t *testing.T) {
	mux, c, notes := testMux(t, discardLogger())
	c.setRate(1.0)

	rec := postJSON(t, mux, "/notify", `{"order_id":1,"payment_id":2,"amount":3,"user_id":4}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", rec.Code, rec.Body.String())
	}
	if got := notes.snapshot(); len(got) != 0 {
		t.Errorf("store grew under injected failure: %d records, want 0", len(got))
	}
}

// --- probes and info ---

func TestProbes(t *testing.T) {
	mux, _, _ := testMux(t, discardLogger())

	for path, want := range map[string]string{
		"/healthz": "ok",
		"/readyz":  "ready",
	} {
		rec := getJSON(t, mux, path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", path, rec.Code)
			continue
		}
		var body struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Status != want {
			t.Errorf("%s body = %s, want status %q", path, rec.Body.String(), want)
		}
	}
}

func TestRootServesServiceInfo(t *testing.T) {
	mux, _, _ := testMux(t, discardLogger())

	rec := getJSON(t, mux, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Service string `json:"service"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Service != "notification" {
		t.Errorf("service = %q, want notification", body.Service)
	}
}

// --- the chaos control plane must stay reachable under chaos ---

// The control plane is deliberately NOT wrapped by instrument: with every data
// endpoint failing, /chaos/error-rate is the only way to turn it off — if this
// ever breaks, a chaos experiment becomes an outage that needs a redeploy.
func TestChaosControlPlaneSurvivesInjectedFailure(t *testing.T) {
	mux, c, _ := testMux(t, discardLogger())
	c.setRate(1.0)

	// Data endpoint fails...
	if rec := postJSON(t, mux, "/notify", `{"order_id":1,"payment_id":2}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("data endpoint status = %d, want 500 under chaos", rec.Code)
	}
	// ...probes and chaos controls still answer.
	if rec := getJSON(t, mux, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("healthz under chaos = %d, want 200", rec.Code)
	}
	if rec := getJSON(t, mux, "/chaos"); rec.Code != http.StatusOK {
		t.Errorf("GET /chaos under chaos = %d, want 200", rec.Code)
	}
	if rec := postJSON(t, mux, "/chaos/error-rate", `{"rate":0}`); rec.Code != http.StatusOK {
		t.Fatalf("turning chaos off failed: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(t, mux, "/notify", `{"order_id":1,"payment_id":2}`); rec.Code != http.StatusOK {
		t.Errorf("notify still failing after chaos off: %d", rec.Code)
	}
}

func TestChaosRejectsOutOfRangeValues(t *testing.T) {
	mux, _, _ := testMux(t, discardLogger())

	cases := []struct {
		name string
		path string
		body string
	}{
		{"rate above 1", "/chaos/error-rate", `{"rate":1.5}`},
		{"negative rate", "/chaos/error-rate", `{"rate":-0.1}`},
		{"negative latency", "/chaos/latency", `{"ms":-1}`},
		{"not a number", "/chaos/error-rate", `{"rate":"high"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := postJSON(t, mux, tc.path, tc.body); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// readValue takes a query parameter over a JSON body, so the chaos endpoints
// work from a plain curl (and from shell tests) without quoting JSON.
func TestChaosAcceptsQueryParams(t *testing.T) {
	mux, c, _ := testMux(t, discardLogger())

	rec := postJSON(t, mux, "/chaos/error-rate?rate=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("query-param form status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if c.rate() != 1.0 {
		t.Errorf("rate = %v, want 1 after ?rate=1", c.rate())
	}
}

func TestChaosLatencyDelaysTheRequest(t *testing.T) {
	mux, c, _ := testMux(t, discardLogger())
	c.latencyMS.Store(40)

	start := time.Now()
	rec := postJSON(t, mux, "/notify", `{"order_id":1,"payment_id":2}`)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — latency injects delay, not failure", rec.Code)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("request took %v with 40ms injected, want at least 40ms", elapsed)
	}
}

// --- concurrency ---

// /notify and /notify-failed write while /notifications reads; the mutex is
// what keeps a snapshot from being a torn read. Run with -race (make unit-test
// does) — the assertion is that nothing panics or races, and every write lands.
func TestStoreConcurrentAddAndSnapshot(t *testing.T) {
	mux, _, notes := testMux(t, discardLogger())

	// Under the cap (maxNotifications) on purpose: this asserts no write is
	// lost to the race, which eviction would make unobservable.
	const writers, each = 4, 20
	done := make(chan struct{})
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			// +1 on both: id 0 is decodeNotify's "missing id" rejection,
			// and those writes are meant to be dropped, not stored.
			for i := 0; i < each; i++ {
				_ = postJSON(t, mux, "/notify",
					fmt.Sprintf(`{"order_id":%d,"payment_id":%d}`, w+1, i+1))
				_ = notes.snapshot()
			}
		}(w)
	}
	for w := 0; w < writers; w++ {
		<-done
	}

	// Every write must have landed: anything less means writes were lost to
	// the race (the cap is not in play at these counts).
	if got := len(notes.snapshot()); got != writers*each {
		t.Errorf("store holds %d of %d writes", got, writers*each)
	}
}
