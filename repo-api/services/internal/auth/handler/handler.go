// Package handler provides gin HTTP handlers for the auth service.
package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/auth/usecase"
)

// AuthHandler holds the login usecase and exposes HTTP endpoints.
type AuthHandler struct {
	loginUC *usecase.LoginUsecase
}

// NewAuthHandler creates an AuthHandler.
func NewAuthHandler(loginUC *usecase.LoginUsecase) *AuthHandler {
	return &AuthHandler{loginUC: loginUC}
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Login handles POST /auth/login.
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password are required"})
		return
	}

	result, err := h.loginUC.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, usecase.ErrInvalidCredentials) {
			slog.WarnContext(c.Request.Context(), "login failed", "username", req.Username, "reason", "invalid_credentials")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
			return
		}
		slog.ErrorContext(c.Request.Context(), "login error", "username", req.Username, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// The result is serialised whole rather than copied field by field. Listing
	// keys by hand is what dropped session_id from this response: the usecase
	// filled it in and tagged it, and the handler simply never mentioned it, so
	// the UI's session links silently fell back to single-request trace links.
	// Returning the struct means a field added upstream cannot go missing here.
	c.JSON(http.StatusOK, result)
}
