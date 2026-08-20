package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// NotificationClient calls notification POST /notify.
type NotificationClient struct {
	baseURL string
	client  *http.Client
}

// NewNotificationClient creates a client with otelhttp transport.
//
// The 3s timeout is the last line of defence: a hung notification pod must
// not hold a checkout response with it.
func NewNotificationClient(baseURL string) *NotificationClient {
	return &NotificationClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout:   3 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

// Notify sends POST /notify for a settled payment; the notification service
// stores it so the checkout test can prove the call happened.
func (n *NotificationClient) Notify(ctx context.Context, paymentID, orderID int, amount float64, userID int) error {
	ctx, span := tracer.Start(ctx, "send notification")
	defer span.End()
	span.SetAttributes(
		attribute.Int("payment.id", paymentID),
		attribute.Int("order.id", orderID),
	)

	payload, _ := json.Marshal(map[string]any{
		"order_id":   orderID,
		"payment_id": paymentID,
		"amount":     amount,
		"user_id":    userID,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.baseURL+"/notify", bytes.NewReader(payload))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return fmt.Errorf("call notification: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("notification returned %d: %s", resp.StatusCode, string(bodyBytes))
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return err
	}
	return nil
}
