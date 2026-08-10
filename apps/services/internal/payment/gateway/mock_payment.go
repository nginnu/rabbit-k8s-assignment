package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/domain"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// MockPaymentClient calls mock-payment POST /charge.
type MockPaymentClient struct {
	baseURL string
	client  *http.Client
}

// NewMockPaymentClient creates a client with otelhttp transport.
func NewMockPaymentClient(baseURL string) *MockPaymentClient {
	return &MockPaymentClient{
		baseURL: baseURL,
		client: &http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

// ChaosHeaders are forwarded from the incoming request to mock-payment.
type ChaosHeaders struct {
	ErrorRate string
	LatencyMs string
	ErrorType string
}

// Charge sends POST /charge with amount and forwards chaos headers.
func (m *MockPaymentClient) Charge(ctx context.Context, amount float64, chaos ChaosHeaders) (*domain.ChargeResult, error) {
	ctx, span := tracer.Start(ctx, "charge card")
	defer span.End()
	span.SetAttributes(attribute.Float64("payment.amount", amount))

	payload, _ := json.Marshal(map[string]float64{"amount": amount})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/charge", bytes.NewReader(payload))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Forward chaos headers so Playwright can control mock-payment behavior.
	if chaos.ErrorRate != "" {
		req.Header.Set("X-Chaos-Error-Rate", chaos.ErrorRate)
	}
	if chaos.LatencyMs != "" {
		req.Header.Set("X-Chaos-Latency-Ms", chaos.LatencyMs)
	}
	if chaos.ErrorType != "" {
		req.Header.Set("X-Chaos-Error-Type", chaos.ErrorType)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, fmt.Errorf("call mock-payment: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("mock-payment returned %d: %s", resp.StatusCode, string(bodyBytes))
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, err
	}

	var result domain.ChargeResult
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, fmt.Errorf("decode charge result: %w", err)
	}
	return &result, nil
}

// bytesReader is a helper to avoid importing bytes in order.go.
func bytesReader(b []byte) io.Reader {
	return bytes.NewReader(b)
}
