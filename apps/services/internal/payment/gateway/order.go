// Package gateway provides HTTP clients to external services.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/domain"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("payment-svc")

// OrderClient calls order-svc internal endpoints.
type OrderClient struct {
	baseURL string
	client  *http.Client
}

// NewOrderClient creates a client with otelhttp transport for trace propagation.
func NewOrderClient(baseURL string) *OrderClient {
	return &OrderClient{
		baseURL: baseURL,
		client: &http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

// Validate calls GET /internal/orders/:id and checks status=pending.
func (o *OrderClient) Validate(ctx context.Context, orderID int) (*domain.OrderInfo, error) {
	ctx, span := tracer.Start(ctx, "verify order exists")
	defer span.End()
	span.SetAttributes(attribute.Int("order.id", orderID))

	url := fmt.Sprintf("%s/internal/orders/%d", o.baseURL, orderID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, fmt.Errorf("call order-svc: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("order-svc returned %d", resp.StatusCode)
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, err
	}

	var info domain.OrderInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, fmt.Errorf("decode order: %w", err)
	}

	if info.Status != "pending" {
		err := fmt.Errorf("order status is %q, expected pending", info.Status)
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, err
	}

	return &info, nil
}

// MarkPaid calls PATCH /internal/orders/:id with status=paid.
func (o *OrderClient) MarkPaid(ctx context.Context, orderID int) error {
	ctx, span := tracer.Start(ctx, "mark order paid")
	defer span.End()
	span.SetAttributes(attribute.Int("order.id", orderID))

	url := fmt.Sprintf("%s/internal/orders/%d", o.baseURL, orderID)

	body := []byte(`{"status":"paid"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytesReader(body))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return fmt.Errorf("call order-svc: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("order-svc PATCH returned %d", resp.StatusCode)
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return err
	}
	return nil
}
