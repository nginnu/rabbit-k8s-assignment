// Package handler provides HTTP handlers for the order service.
package handler

import (
	"log/slog"
	"net/http"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/domain"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/usecase"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/middleware"

	"github.com/gin-gonic/gin"
)

// OrderHandler exposes order endpoints. GET /products moved to catalog
// with the split; the route in front of this service now only carries orders.
type OrderHandler struct {
	uc *usecase.OrderUsecase
}

// New creates a new OrderHandler.
func New(uc *usecase.OrderUsecase) *OrderHandler {
	return &OrderHandler{uc: uc}
}

// CreateOrder handles POST /orders (JWT required).
func (h *OrderHandler) CreateOrder(c *gin.Context) {
	claims, ok := middleware.UserFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req domain.CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	slog.InfoContext(ctx, "creating order",
		"user_id", claims.UserID,
		"product_id", req.ProductID,
	)

	order, amount, err := h.uc.CreateOrder(ctx, claims.UserID, req)
	if err != nil {
		slog.ErrorContext(ctx, "create order failed",
			"user_id", claims.UserID,
			"error", err.Error(),
		)
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	slog.InfoContext(ctx, "order created",
		"user_id", claims.UserID,
		"order_id", order.ID,
	)

	c.JSON(http.StatusCreated, gin.H{
		"id":         order.ID,
		"product_id": order.ProductID,
		"status":     order.Status,
		"amount":     amount,
	})
}

// ListOrders handles GET /orders (JWT required).
func (h *OrderHandler) ListOrders(c *gin.Context) {
	claims, ok := middleware.UserFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	ctx := c.Request.Context()
	orders, err := h.uc.ListUserOrders(ctx, claims.UserID)
	if err != nil {
		slog.ErrorContext(ctx, "list orders failed",
			"user_id", claims.UserID,
			"error", err.Error(),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	resp := make([]domain.OrderResponse, 0, len(orders))
	for _, o := range orders {
		resp = append(resp, domain.OrderResponse{
			ID:        o.ID,
			ProductID: o.ProductID,
			Status:    o.Status,
		})
	}
	c.JSON(http.StatusOK, resp)
}

// Healthz handles GET /healthz.
func Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
