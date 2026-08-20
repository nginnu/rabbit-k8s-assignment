package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/catalog/domain"
	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/catalog/usecase"

	"github.com/gin-gonic/gin"
)

// fakeRepo and fakeCache stand in for gorm/Redis, wired through the real
// usecase so this asserts the wire response — the part the frontend's
// Array.isArray(pRes.data) check actually depends on. What this canNOT catch
// is gorm's own nil-slice-on-zero-rows behaviour inside repository.go: that
// is proven only against a real, seeded-then-emptied MariaDB table, the same
// boundary every other repository in this codebase is tested at (none of
// them have a repository-level go test either).
type fakeRepo struct{ products []domain.Product }

func (f fakeRepo) ListProducts(context.Context) ([]domain.Product, error) { return f.products, nil }

type fakeCache struct{}

func (fakeCache) GetBytes(context.Context, string) ([]byte, error)         { return nil, domain.ErrCacheMiss }
func (fakeCache) Set(context.Context, string, []byte, time.Duration) error { return nil }

func newRouter(products []domain.Product) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(usecase.New(fakeRepo{products: products}, fakeCache{}))
	r.GET("/products", h.ListProducts)
	return r
}

func get(r *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// The defect: gorm's Find leaves a nil slice on zero rows, and a nil slice
// marshals to JSON null, not []. The frontend's Array.isArray(pRes.data) is
// false for null, so an empty catalog renders as a permanent "Loading
// products…" instead of an empty grid.
func TestListProductsEmptyCatalogIsEmptyArrayNotNull(t *testing.T) {
	r := newRouter([]domain.Product{})

	rec := get(r, "/products")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "[]" {
		t.Errorf("body = %q, want the literal empty array, not null", got)
	}
}

func TestListProductsReturnsCatalog(t *testing.T) {
	r := newRouter([]domain.Product{{ID: "LIV-H-24", Name: "Liverpool Home 24/25", Price: 3290}})

	rec := get(r, "/products")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"id":"LIV-H-24"`, `"price":3290`} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %s, missing %q", body, want)
		}
	}
}
