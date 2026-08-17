package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/auth/domain"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/auth/usecase"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type fakeUsers struct{ err error }

func (f fakeUsers) FindByUsername(context.Context, string) (*domain.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	h, _ := bcrypt.GenerateFromPassword([]byte("password"), 4)
	return &domain.User{ID: 1, Username: "alice", PasswordHash: string(h)}, nil
}

type fakeSessions struct{ err error }

func (f fakeSessions) Set(context.Context, string, int, time.Duration) error { return f.err }

// newAuthRouter wires the real handler around a real usecase; only the edges
// (DB, Redis) are stubbed, because the mapping under test is the handler's:
// which error becomes which status code.
func newAuthRouter(userErr, sessionErr error) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewAuthHandler(usecase.NewLoginUsecase(fakeUsers{err: userErr}, fakeSessions{err: sessionErr}, "unit-test-secret", 1))
	r.POST("/auth/login", h.Login)
	return r
}

func login(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestLoginOkReturnsWholeResult(t *testing.T) {
	r := newAuthRouter(nil, nil)

	rec := login(r, `{"username":"alice","password":"password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	// The handler serialises the result struct whole; the test pins the keys
	// that must survive (token AND session_id) because the comment in Login
	// records a session_id-dropping incident this shape is there to prevent.
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"token", "session_id", "expires_at"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("response missing %q: %v", key, resp)
		}
	}
}

func TestLoginBadCredentialsIs401(t *testing.T) {
	r := newAuthRouter(nil, nil)

	rec := login(r, `{"username":"alice","password":"wrong"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body: %s", rec.Code, rec.Body.String())
	}
	// One fixed message: the response must not echo which attempt failed or
	// the credentials sent — the anti-oracle property the sentinel enforces.
	if strings.Contains(rec.Body.String(), "alice") || strings.Contains(rec.Body.String(), "wrong") {
		t.Errorf("body echoes the failed attempt: %s", rec.Body.String())
	}
}

func TestLoginUpstreamErrorIs500(t *testing.T) {
	r := newAuthRouter(errors.New("db down"), nil)

	if rec := login(r, `{"username":"alice","password":"password"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestLoginSessionStoreErrorIs500(t *testing.T) {
	r := newAuthRouter(nil, errors.New("redis down"))

	if rec := login(r, `{"username":"alice","password":"password"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestLoginRejectsIncompleteBody(t *testing.T) {
	r := newAuthRouter(nil, nil)

	if rec := login(r, `{"username":"alice"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
