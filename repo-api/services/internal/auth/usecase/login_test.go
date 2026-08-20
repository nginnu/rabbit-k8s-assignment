package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/auth/domain"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/middleware"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type fakeUsers struct {
	user *domain.User
	err  error
}

func (f fakeUsers) FindByUsername(_ context.Context, _ string) (*domain.User, error) {
	return f.user, f.err
}

type fakeSessions struct {
	setCalls []struct {
		jti    string
		userID int
		ttl    time.Duration
	}
	err error
}

func (f *fakeSessions) Set(_ context.Context, jti string, userID int, ttl time.Duration) error {
	f.setCalls = append(f.setCalls, struct {
		jti    string
		userID int
		ttl    time.Duration
	}{jti, userID, ttl})
	return f.err
}

// hashOf mints a bcrypt hash in-test; cost 4 keeps the suite fast — bcrypt's
// security is not under test here, the comparison path is.
func hashOf(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), 4)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	return string(h)
}

func newLogin(users *fakeUsers, sessions *fakeSessions) *LoginUsecase {
	return NewLoginUsecase(users, sessions, "unit-test-secret", 1)
}

func TestLoginHappyPathIssuesVerifiableTokenAndSession(t *testing.T) {
	users := &fakeUsers{user: &domain.User{ID: 1, Username: "alice", PasswordHash: hashOf(t, "password")}}
	sessions := &fakeSessions{}
	uc := newLogin(users, sessions)

	res, err := uc.Login(context.Background(), "alice", "password")
	if err != nil {
		t.Fatalf("Login error: %v", err)
	}

	// The token must verify with the same secret and carry the claims the
	// middleware reads — uid for authorization, sid for the session links the
	// UI builds. A token that signs but forgets sid quietly breaks those.
	claims := &middleware.JWTClaims{}
	token, err := jwt.ParseWithClaims(res.Token, claims, func(*jwt.Token) (any, error) {
		return []byte("unit-test-secret"), nil
	})
	if err != nil || !token.Valid {
		t.Fatalf("issued token does not verify: %v", err)
	}
	if claims.UserID != 1 || claims.Username != "alice" {
		t.Errorf("claims uid/user = %d/%q, want 1/alice", claims.UserID, claims.Username)
	}
	if claims.SessionID != res.SessionID || claims.SessionID == "" {
		t.Errorf("claims sid = %q, result sid = %q — must match and be non-empty",
			claims.SessionID, res.SessionID)
	}

	// Expiry is plausibly in the future and the session was stored under the
	// token's jti with the user id.
	if !res.ExpiresAt.After(time.Now()) {
		t.Errorf("ExpiresAt = %v, want future", res.ExpiresAt)
	}
	if len(sessions.setCalls) != 1 {
		t.Fatalf("session Set calls = %d, want 1", len(sessions.setCalls))
	}
	if sessions.setCalls[0].userID != 1 {
		t.Errorf("stored userID = %d, want 1", sessions.setCalls[0].userID)
	}
	if sessions.setCalls[0].jti != claims.ID {
		t.Errorf("stored jti = %q, token jti = %q — the session must key on the token's id",
			sessions.setCalls[0].jti, claims.ID)
	}
}

// Unknown user and wrong password must both map to the same sentinel: the
// caller cannot learn which half was wrong, or login becomes an oracle.
func TestLoginInvalidCredentials(t *testing.T) {
	cases := []struct {
		name  string
		users *fakeUsers
		pass  string
	}{
		{"unknown user", &fakeUsers{user: nil}, "password"},
		{"wrong password", &fakeUsers{user: &domain.User{ID: 1, PasswordHash: hashOf(t, "right")}}, "wrong"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := newLogin(tc.users, &fakeSessions{})
			_, err := uc.Login(context.Background(), "alice", tc.pass)
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Errorf("err = %v, want ErrInvalidCredentials", err)
			}
		})
	}
}

// A DB outage is the server's fault and must surface as itself, not as
// "invalid credentials" — that would hide in monitoring as failed logins.
func TestLoginRepoErrorIsNotInvalidCredentials(t *testing.T) {
	uc := newLogin(&fakeUsers{err: errors.New("db down")}, &fakeSessions{})

	_, err := uc.Login(context.Background(), "alice", "password")
	if err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("err = %v, want the raw repo error", err)
	}
}

// If Redis rejects the session, the token is already signed and out — a token
// whose session does not exist is a credential nobody can revoke, so the
// login must fail loudly instead.
func TestLoginSessionStoreFailureFailsLogin(t *testing.T) {
	users := &fakeUsers{user: &domain.User{ID: 1, PasswordHash: hashOf(t, "password")}}
	uc := newLogin(users, &fakeSessions{err: errors.New("redis down")})

	if _, err := uc.Login(context.Background(), "alice", "password"); err == nil {
		t.Error("Login returned nil with a dead session store, want error")
	}
}
