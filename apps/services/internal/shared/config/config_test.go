package config

import (
	"os"
	"strings"
	"testing"
)

// The required set LoadBase panics on. A typo'd panic message names the wrong
// var, so each one is asserted individually.
var requiredVars = []string{
	"DEPLOYMENT_ENV",
	"OTEL_EXPORTER_OTLP_ENDPOINT",
	"MARIADB_DSN",
	"JWT_SECRET",
}

// setAllBut sets every required var except skip, and returns a restore func —
// t.Setenv cannot unset, and the host may have any of these lying around.
func setAllBut(t *testing.T, skip string) {
	t.Helper()
	for _, v := range requiredVars {
		if v == skip {
			old, had := os.LookupEnv(v)
			_ = os.Unsetenv(v)
			t.Cleanup(func() {
				if had {
					_ = os.Setenv(v, old)
				} else {
					_ = os.Unsetenv(v)
				}
			})
			continue
		}
		t.Setenv(v, "set-for-test")
	}
}

func TestLoadBasePanicsOnEachMissingRequiredVar(t *testing.T) {
	for _, missing := range requiredVars {
		t.Run(missing, func(t *testing.T) {
			setAllBut(t, missing)

			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("LoadBase returned, want panic")
				}
				msg, _ := r.(string)
				if !strings.Contains(msg, missing) {
					t.Errorf("panic = %v, want it to name %q", r, missing)
				}
			}()
			LoadBase()
		})
	}
}

func TestLoadBaseHappyPath(t *testing.T) {
	t.Setenv("DEPLOYMENT_ENV", "local")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://alloy:4317")
	t.Setenv("MARIADB_DSN", "user:pass@tcp(mariadb:3306)/rabbitshop")
	t.Setenv("JWT_SECRET", "s3cret")
	t.Setenv("SVC_PREFIX", "platform")
	t.Setenv("JWT_EXPIRE_HOURS", "2")
	t.Setenv("FAIL_RATE", "0.25")

	cfg := LoadBase()
	if cfg.DeploymentEnv != "local" || cfg.MariaDBDSN == "" || cfg.JWTSecret != "s3cret" {
		t.Errorf("cfg = %+v, want the required vars read back", cfg)
	}
	if cfg.JWTExpireHours != 2 {
		t.Errorf("JWTExpireHours = %d, want 2", cfg.JWTExpireHours)
	}
	if cfg.FailRate != 0.25 {
		t.Errorf("FailRate = %v, want 0.25 — the canary experiment depends on it", cfg.FailRate)
	}
	if cfg.SvcPrefix != "platform" {
		t.Errorf("SvcPrefix = %q, want platform", cfg.SvcPrefix)
	}
}

// Non-numeric values must fail at boot, not at first parse: a FAIL_RATE of
// "0,5" (locale typo) silently becoming 0 would disable every chaos test.
func TestLoadBasePanicsOnBadNumber(t *testing.T) {
	for _, tc := range []struct{ env, val string }{
		{"JWT_EXPIRE_HOURS", "soon"},
		{"FAIL_RATE", "0,5"},
	} {
		t.Run(tc.env+"="+tc.val, func(t *testing.T) {
			for _, v := range requiredVars {
				t.Setenv(v, "set-for-test")
			}
			t.Setenv(tc.env, tc.val)

			defer func() {
				if recover() == nil {
					t.Fatal("LoadBase returned, want panic on non-numeric value")
				}
			}()
			LoadBase()
		})
	}
}

// ServiceName feeds the service_name label that dashboards and the o11y tests
// match on — its shape is a contract with the whole observability pipeline.
func TestServiceName(t *testing.T) {
	b := Base{SvcPrefix: "platform", DeploymentEnv: "local"}
	if got := b.ServiceName("payment-svc"); got != "platform-local-payment-svc" {
		t.Errorf("ServiceName = %q, want platform-local-payment-svc", got)
	}
}
