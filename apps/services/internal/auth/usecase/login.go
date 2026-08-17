// Package usecase contains application-level business logic for auth.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/bcrypt"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/auth/domain"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/middleware"
)

var tracer = otel.Tracer("auth")

// ErrInvalidCredentials is returned when username or password is wrong.
var ErrInvalidCredentials = errors.New("invalid credentials")

// LoginResult holds the token and expiry returned on successful login.
type LoginResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	SessionID string    `json:"session_id"`
}

// LoginUsecase handles the login business logic.
type LoginUsecase struct {
	userRepo    domain.UserRepository
	sessionRepo domain.SessionRepository
	jwtSecret   []byte
	jwtExpiry   time.Duration
}

// NewLoginUsecase creates a LoginUsecase.
func NewLoginUsecase(
	userRepo domain.UserRepository,
	sessionRepo domain.SessionRepository,
	jwtSecret string,
	jwtExpireHours int,
) *LoginUsecase {
	return &LoginUsecase{
		userRepo:    userRepo,
		sessionRepo: sessionRepo,
		jwtSecret:   []byte(jwtSecret),
		jwtExpiry:   time.Duration(jwtExpireHours) * time.Hour,
	}
}

// Login authenticates a user and returns a JWT token.
func (uc *LoginUsecase) Login(ctx context.Context, username, password string) (*LoginResult, error) {
	ctx, span := tracer.Start(ctx, "log in")
	defer span.End()

	// 1. Find user by username
	user, err := uc.findUser(ctx, username)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if user == nil {
		span.SetStatus(codes.Error, "user not found")
		return nil, ErrInvalidCredentials
	}

	span.SetAttributes(attribute.Int("user.id", user.ID))

	// 2. Compare password with bcrypt hash
	if err := uc.compareBcrypt(ctx, user.PasswordHash, password); err != nil {
		span.SetStatus(codes.Error, "password mismatch")
		return nil, ErrInvalidCredentials
	}

	// 3. Generate session ID + JTI (session ≠ jti — session stable across refresh)
	sessionID := uuid.New().String()
	jti := uuid.New().String()
	expiresAt := time.Now().Add(uc.jwtExpiry)

	span.SetAttributes(
		attribute.String("session.id", sessionID),
	)

	// 4. Sign JWT (includes session_id claim)
	token, err := uc.signJWT(ctx, user, sessionID, jti, expiresAt)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "jwt sign failed")
		return nil, fmt.Errorf("sign jwt: %w", err)
	}

	// 5. Store session in Redis (key by jti)
	if err := uc.storeSession(ctx, jti, user.ID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "session store failed")
		return nil, fmt.Errorf("store session: %w", err)
	}

	slog.InfoContext(ctx, "login ok",
		"user_id", user.ID,
		"username", username,
		"session_id", sessionID,
	)

	return &LoginResult{
		Token:     token,
		ExpiresAt: expiresAt,
		SessionID: sessionID,
	}, nil
}

// findUser wraps the repository call with a custom span for business attributes.
func (uc *LoginUsecase) findUser(ctx context.Context, username string) (*domain.User, error) {
	ctx, span := tracer.Start(ctx, "look up user")
	defer span.End()

	span.SetAttributes(attribute.String("db.username_query", username))

	user, err := uc.userRepo.FindByUsername(ctx, username)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if user != nil {
		span.SetAttributes(attribute.Int("user.id", user.ID))
	}
	return user, nil
}

// compareBcrypt compares the hash with the plain password under a custom span.
func (uc *LoginUsecase) compareBcrypt(ctx context.Context, hash, password string) error {
	_, span := tracer.Start(ctx, "verify password")
	defer span.End()

	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		span.SetAttributes(attribute.Bool("crypto.match", false))
		return err
	}
	span.SetAttributes(attribute.Bool("crypto.match", true))
	return nil
}

// signJWT creates a signed JWT token under a custom span.
func (uc *LoginUsecase) signJWT(ctx context.Context, user *domain.User, sessionID, jti string, expiresAt time.Time) (string, error) {
	_, span := tracer.Start(ctx, "issue token")
	defer span.End()

	claims := middleware.JWTClaims{
		UserID:    user.ID,
		Username:  user.Username,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(uc.jwtSecret)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	span.SetAttributes(
		attribute.Int("user.id", user.ID),
		attribute.String("session.id", sessionID),
		attribute.String("jwt.jti", jti),
	)
	return signed, nil
}

// storeSession saves the session in Redis under a custom span.
func (uc *LoginUsecase) storeSession(ctx context.Context, jti string, userID int) error {
	ctx, span := tracer.Start(ctx, "store session",
		trace.WithAttributes(
			attribute.Int("user.id", userID),
			attribute.String("session.jti", jti),
		),
	)
	defer span.End()

	err := uc.sessionRepo.Set(ctx, jti, userID, uc.jwtExpiry)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
