// Package handler provides HTTP handlers for the catalog service.
package handler

import (
	"log/slog"
	"net/http"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/catalog/usecase"

	"github.com/gin-gonic/gin"
)

// CatalogHandler exposes the product listing.
type CatalogHandler struct {
	uc *usecase.CatalogUsecase
}

// New creates a new CatalogHandler.
func New(uc *usecase.CatalogUsecase) *CatalogHandler {
	return &CatalogHandler{uc: uc}
}

// ListProducts handles GET /products (JWT required).
// Used by web-ui to show the product grid (Premier League jerseys).
func (h *CatalogHandler) ListProducts(c *gin.Context) {
	ctx := c.Request.Context()
	products, err := h.uc.ListProducts(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list products failed", "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, products)
}

// Healthz handles GET /healthz.
func Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
