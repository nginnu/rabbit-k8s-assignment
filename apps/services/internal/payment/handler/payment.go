// Package handler provides gin HTTP handlers for payment-svc.
package handler

import (
	"net/http"
	"strconv"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/domain"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/gateway"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/payment/usecase"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/middleware"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// PaymentHandler handles payment HTTP endpoints.
type PaymentHandler struct {
	uc *usecase.PaymentUsecase
}

// New creates a PaymentHandler.
func New(uc *usecase.PaymentUsecase) *PaymentHandler {
	return &PaymentHandler{uc: uc}
}

// createPaymentRequest is the JSON body for POST /payments.
//
// No Amount field: the price is order-svc's word, read via
// PaymentUsecase.ProcessPayment → OrderGateway.Validate, never the client's.
// A body that still sends "amount" is accepted and ignored — encoding/json
// drops unknown-to-the-struct fields rather than erroring on them.
//
// Method is client-chosen and stays that way: unlike price, which channel
// pays is the caller's decision, not order-svc's. It is still validated
// below against the four channels the schema and the gateway both know
// about, so a typo fails here with a 400 instead of at the DB's enum
// constraint or, worse, silently at the gateway.
type createPaymentRequest struct {
	OrderID int    `json:"order_id" binding:"required"`
	Method  string `json:"method"   binding:"required"`
}

// CreatePayment handles POST /payments.
func (h *PaymentHandler) CreatePayment(c *gin.Context) {
	var req createPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	method := domain.PaymentMethod(req.Method)
	if !method.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "method must be one of card, truemoney, gwallet, cod"})
		return
	}

	claims, ok := middleware.UserFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found in context"})
		return
	}

	// Extract chaos headers from incoming request to forward to the payment gateway.
	chaos := gateway.ChaosHeaders{
		ErrorRate: c.GetHeader("X-Chaos-Error-Rate"),
		LatencyMs: c.GetHeader("X-Chaos-Latency-Ms"),
		ErrorType: c.GetHeader("X-Chaos-Error-Type"),
	}

	out, err := h.uc.ProcessPayment(c.Request.Context(), usecase.ProcessPaymentInput{
		OrderID: req.OrderID,
		UserID:  claims.UserID,
		Method:  method,
		Chaos:   chaos,
	})
	if err != nil {
		// If payment was created but charge failed, return 402 Payment Required.
		if out != nil && out.Status == "failed" {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":      "payment failed",
				"payment_id": out.PaymentID,
				"status":     out.Status,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"payment_id":  out.PaymentID,
		"status":      out.Status,
		"gateway_ref": out.GatewayRef,
	})
}

// PaymentSuccess handles GET /payments/:id/success — the confirmation the
// storefront lands on once a payment goes through.
//
// It exists for two reasons beyond showing a receipt. It gives the purchase
// flow a visible last step, so a trace search that ends here proves the journey
// completed rather than stopping somewhere unknown. And it gives a test a
// single call that answers "did this actually finish", instead of inferring
// success from the absence of an error.
func (h *PaymentHandler) PaymentSuccess(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payment id must be a number"})
		return
	}

	claims, ok := middleware.UserFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found in context"})
		return
	}

	span := trace.SpanFromContext(c.Request.Context())
	span.SetAttributes(
		attribute.Int("payment.id", id),
		attribute.Int("user.id", claims.UserID),
		attribute.String("checkout.stage", "complete"),
	)

	p, err := h.uc.GetPayment(c.Request.Context(), id, claims.UserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "payment not found"})
		return
	}

	// Only a payment that actually settled is a success. Reporting otherwise
	// would make the endpoint useless as a completion signal.
	if p.Status != "paid" {
		span.SetAttributes(attribute.String("checkout.stage", "incomplete"))
		c.JSON(http.StatusConflict, gin.H{
			"error":      "payment has not completed",
			"payment_id": p.ID,
			"status":     p.Status,
		})
		return
	}

	span.AddEvent("checkout completed")
	c.JSON(http.StatusOK, gin.H{
		"payment_id":  p.ID,
		"order_id":    p.OrderID,
		"amount":      p.Amount,
		"status":      p.Status,
		"gateway_ref": p.GatewayRef,
		"message":     "payment completed",
	})
}
