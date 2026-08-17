package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/shared/config"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel/baggage"
)

const secret = "unit-test-secret"

// tokenFor mints a JWT in the exact shape Auth parses, with the expiry the
// caller needs — the expired-token case is the one that catches a parser that
// forgot to check it.
func tokenFor(t *testing.T, userID int, expires time.Time) string {
	t.Helper()
	claims := JWTClaims{
		UserID:    userID,
		Username:  "alice",
		SessionID: "sess-1",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expires),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

// authedRouter mounts Auth in front of a handler that echoes the claims back,
// so the test observes both the verdict and what the handler received.
func authedRouter() (*gin.Engine, *bool) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	reached := new(bool)
	r.Use(Auth(config.Base{JWTSecret: secret}))
	r.GET("/orders", func(c *gin.Context) {
		*reached = true
		claims, _ := UserFromContext(c)
		c.JSON(http.StatusOK, gin.H{"uid": claims.UserID, "sid": claims.SessionID})
	})
	return r, reached
}

func doGet(r *gin.Engine, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAuthRejectsMissingAndBadTokens(t *testing.T) {
	cases := []struct {
		name  string
		token string
	}{
		{"no header", ""},
		{"garbage", "not-a-jwt"},
		{"tampered", "abc.def.ghi"},
		{"wrong secret", tokenSignedWith(t, "another-secret")},
		{"expired", tokenFor(t, 1, time.Now().Add(-time.Minute))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, reached := authedRouter()
			rec := doGet(r, "/orders", tc.token)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body: %s", rec.Code, rec.Body.String())
			}
			if *reached {
				t.Error("handler ran despite rejected token")
			}
		})
	}
}

// Before WithValidMethods was added to the parser, the keyfunc returned the
// same HMAC secret regardless of which signing method the token's header
// claimed, so a token minted with any HMAC variant — not just HS256 — still
// verified. This is real alg-confusion surface, not a hypothetical one: it
// pins verification to a single algorithm rather than trusting every caller
// to pick HS256 on their own.
func TestAuthRejectsTokenSignedWithADifferentAlgorithm(t *testing.T) {
	claims := JWTClaims{
		UserID: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	// Same secret as every accepted token in this file — only the algorithm
	// differs, so a rejection here can only be the alg pin, not a bad key.
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS384, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign HS384: %v", err)
	}

	r, reached := authedRouter()
	rec := doGet(r, "/orders", tok)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a non-HS256 token even with the correct secret", rec.Code)
	}
	if *reached {
		t.Error("handler ran on a token signed with a different algorithm")
	}
}

func tokenSignedWith(t *testing.T, s string) string {
	t.Helper()
	claims := JWTClaims{
		UserID: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s))
	return tok
}

func TestAuthAcceptsValidTokenAndExposesClaims(t *testing.T) {
	r, reached := authedRouter()

	rec := doGet(r, "/orders", tokenFor(t, 7, time.Now().Add(time.Hour)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if !*reached {
		t.Fatal("handler never ran")
	}
	want := `{"sid":"sess-1","uid":7}`
	if got := rec.Body.String(); got != want {
		t.Errorf("claims echoed = %s, want %s", got, want)
	}
}

// Auth's second job: baggage. Downstream services read user.id off the wire,
// so a token that authenticates but forgets the baggage silently makes every
// downstream span anonymous.
func TestAuthSetsBaggageForDownstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var userMember, sessionMember string
	r.Use(Auth(config.Base{JWTSecret: secret}))
	r.GET("/probe", func(c *gin.Context) {
		bag := baggage.FromContext(c.Request.Context())
		userMember = bag.Member("user.id").Value()
		sessionMember = bag.Member("session.id").Value()
		c.Status(http.StatusOK)
	})

	doGet(r, "/probe", tokenFor(t, 7, time.Now().Add(time.Hour)))
	if userMember != "7" {
		t.Errorf("user.id baggage = %q, want 7", userMember)
	}
	if sessionMember != "sess-1" {
		t.Errorf("session.id baggage = %q, want sess-1", sessionMember)
	}
}

// rate 0 is the passthrough every service runs with FAIL_RATE unset, rate 1
// fails always — the two deterministic ends of FailInjector, since anything
// between is a dice roll.
func TestFailInjectorEndpoints(t *testing.T) {
	run := func(rate float64, n int) int {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(FailInjector(rate))
		r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
		failed := 0
		for i := 0; i < n; i++ {
			if rec := doGet(r, "/", ""); rec.Code != http.StatusOK {
				failed++
			}
		}
		return failed
	}

	if failed := run(0, 50); failed != 0 {
		t.Errorf("rate 0 failed %d/50, want 0 — the default path must cost nothing", failed)
	}
	if failed := run(1, 50); failed != 50 {
		t.Errorf("rate 1 failed %d/50, want 50", failed)
	}
}

func TestUserFromContextWithoutAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	if _, ok := UserFromContext(c); ok {
		t.Error("UserFromContext returned ok with no Auth in the chain")
	}
}

func TestTraceResponseHeader(t *testing.T) {
	// Without otel Init the span context is invalid, so the header must be
	// skipped rather than written as 00-0000000000000000-...-01.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(TraceResponseHeader())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := doGet(r, "/", "")
	if v := rec.Header().Get("traceresponse"); v != "" {
		t.Errorf("traceresponse = %q, want unset for an invalid span context", v)
	}
}
