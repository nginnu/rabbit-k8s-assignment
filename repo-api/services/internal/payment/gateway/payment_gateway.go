package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/domain"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// gatewayClientTimeout has to clear the chaos knobs deliberately built into
// payment-gateway: the storefront's pay page (chaos panel) lets a caller ask
// for up to 10s of injected latency, and X-Chaos-Error-Type=timeout sleeps
// 5s before answering 504. A tighter client timeout would turn that
// legitimate, requested slow response into an indistinguishable connection
// error. This bound exists only for the case neither of those cover: a
// payment-gateway pod that is simply hung, with no chaos requested.
const gatewayClientTimeout = 15 * time.Second

// PaymentGatewayClient calls the payment gateway POST /charge.
type PaymentGatewayClient struct {
	baseURL string
	client  *http.Client
}

// NewPaymentGatewayClient creates a client with otelhttp transport.
func NewPaymentGatewayClient(baseURL string) *PaymentGatewayClient {
	return &PaymentGatewayClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout:   gatewayClientTimeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

// ChaosHeaders are forwarded from the incoming request to the payment gateway.
type ChaosHeaders struct {
	ErrorRate string
	LatencyMs string
	ErrorType string
}

// Charge sends POST /charge with amount and method, and forwards chaos headers.
func (m *PaymentGatewayClient) Charge(ctx context.Context, amount float64, method domain.PaymentMethod, chaos ChaosHeaders) (*domain.ChargeResult, error) {
	ctx, span := tracer.Start(ctx, "charge "+string(method))
	defer span.End()
	span.SetAttributes(
		attribute.Float64("payment.amount", amount),
		attribute.String("payment.method", string(method)),
	)

	payload, _ := json.Marshal(map[string]any{"amount": amount, "method": string(method)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/charge", bytes.NewReader(payload))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Forward chaos headers so a test run can steer the gateway's failure modes.
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
		return nil, fmt.Errorf("call payment-gateway: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("payment-gateway returned %d: %s", resp.StatusCode, string(bodyBytes))
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
