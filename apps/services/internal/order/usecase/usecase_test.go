package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/order/domain"
)

// The cache seam is what makes this testable: the fakes below replace Redis
// and the DB without either existing, so what is pinned down is the
// cache-aside POLICY — when the repo is skipped, when the cache is written
// back, and which failures degrade instead of fail.

type fakeRepo struct {
	products   []domain.Product
	listErr    error
	listCalls  int
	availErr   error
	createHook func(order *domain.Order) error
}

func (f *fakeRepo) ListProducts(context.Context) ([]domain.Product, error) {
	f.listCalls++
	return f.products, f.listErr
}

func (f *fakeRepo) CheckProductAvailability(context.Context, string) (*domain.Product, error) {
	if f.availErr != nil {
		return nil, f.availErr
	}
	return &domain.Product{ID: "LIV-H-24"}, nil
}

func (f *fakeRepo) CreateOrderTx(_ context.Context, order *domain.Order) error {
	order.ID = 7 // what MariaDB's autoIncrement would do
	if f.createHook != nil {
		return f.createHook(order)
	}
	return nil
}

func (f *fakeRepo) ListByUserID(context.Context, int) ([]domain.Order, error) { return nil, nil }
func (f *fakeRepo) FindByID(context.Context, int) (*domain.Order, error)      { return nil, nil }
func (f *fakeRepo) UpdateStatus(context.Context, int, domain.OrderStatus) (*domain.Order, error) {
	return nil, nil
}

type fakeCache struct {
	get    []byte
	getErr error
	setErr error
	setKey string
	setVal []byte
	setTTL time.Duration
}

func (f *fakeCache) GetBytes(context.Context, string) ([]byte, error) { return f.get, f.getErr }
func (f *fakeCache) Set(_ context.Context, key string, v []byte, ttl time.Duration) error {
	f.setKey, f.setVal, f.setTTL = key, v, ttl
	return f.setErr
}

func products(n int) []domain.Product {
	out := make([]domain.Product, n)
	for i := range out {
		out[i] = domain.Product{ID: "P", Name: "shirt", Price: 100}
	}
	return out
}

func TestListProductsCacheHitSkipsDB(t *testing.T) {
	cached := products(6)
	data, _ := json.Marshal(cached)
	repo := &fakeRepo{products: products(1)}
	cache := &fakeCache{get: data}
	uc := New(repo, cache)

	got, err := uc.ListProducts(context.Background())
	if err != nil {
		t.Fatalf("ListProducts error: %v", err)
	}
	if len(got) != 6 {
		t.Errorf("returned %d products, want the 6 from cache", len(got))
	}
	// The whole point of the cache: a hit must not cost a DB query.
	if repo.listCalls != 0 {
		t.Errorf("repo queried %d times on a cache hit, want 0", repo.listCalls)
	}
}

func TestListProductsCacheMissQueriesAndWritesBack(t *testing.T) {
	repo := &fakeRepo{products: products(2)}
	cache := &fakeCache{getErr: domain.ErrCacheMiss}
	uc := New(repo, cache)

	got, err := uc.ListProducts(context.Background())
	if err != nil {
		t.Fatalf("ListProducts error: %v", err)
	}
	if len(got) != 2 || repo.listCalls != 1 {
		t.Fatalf("got %d products with %d repo calls, want 2 with 1", len(got), repo.listCalls)
	}
	// Write-back happened, under the shared key and TTL.
	if cache.setKey != "products:list" {
		t.Errorf("set key = %q, want products:list", cache.setKey)
	}
	if cache.setTTL != 60*time.Second {
		t.Errorf("set ttl = %v, want 60s", cache.setTTL)
	}
	var stored []domain.Product
	if err := json.Unmarshal(cache.setVal, &stored); err != nil || len(stored) != 2 {
		t.Errorf("cached value = %s, want the marshalled products", cache.setVal)
	}
}

// A corrupt cached payload is a miss with extra steps: it must degrade to the
// DB, not 500 the storefront's product page.
func TestListProductsCorruptCacheFallsThrough(t *testing.T) {
	repo := &fakeRepo{products: products(1)}
	cache := &fakeCache{get: []byte("not json{")}
	uc := New(repo, cache)

	got, err := uc.ListProducts(context.Background())
	if err != nil {
		t.Fatalf("corrupt cache value failed the request: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d products, want 1 from DB", len(got))
	}
}

// Redis being unreachable is the documented degradation path (main.go warns
// and continues): the request must still succeed from the DB.
func TestListProductsCacheNetworkErrorDegradesToDB(t *testing.T) {
	repo := &fakeRepo{products: products(1)}
	cache := &fakeCache{getErr: errors.New("connection refused")}
	uc := New(repo, cache)

	if _, err := uc.ListProducts(context.Background()); err != nil {
		t.Fatalf("dead cache failed the request: %v", err)
	}
	if repo.listCalls != 1 {
		t.Errorf("repo calls = %d, want 1", repo.listCalls)
	}
}

// The write-back is best effort by design: a failed SET must not fail a
// request whose data already came back correctly.
func TestListProductsFailedWriteBackIsNonFatal(t *testing.T) {
	repo := &fakeRepo{products: products(1)}
	cache := &fakeCache{getErr: domain.ErrCacheMiss, setErr: errors.New("cache full")}
	uc := New(repo, cache)

	if _, err := uc.ListProducts(context.Background()); err != nil {
		t.Fatalf("failed write-back failed the request: %v", err)
	}
}

func TestListProductsDBErrorPropagates(t *testing.T) {
	repo := &fakeRepo{listErr: errors.New("db down")}
	uc := New(repo, &fakeCache{getErr: domain.ErrCacheMiss})

	if _, err := uc.ListProducts(context.Background()); err == nil {
		t.Error("DB error swallowed, want it surfaced")
	}
}

func TestCreateOrder(t *testing.T) {
	uc := New(&fakeRepo{}, &fakeCache{})

	order, err := uc.CreateOrder(context.Background(), 1, domain.CreateOrderRequest{ProductID: "LIV-H-24"})
	if err != nil {
		t.Fatalf("CreateOrder error: %v", err)
	}
	if order.ID != 7 {
		t.Errorf("order id = %d, want 7 from the insert", order.ID)
	}
	if order.Status != domain.OrderStatusPending {
		t.Errorf("status = %q, want pending — payment-svc validates on it", order.Status)
	}
	if order.UserID != 1 || order.ProductID != "LIV-H-24" {
		t.Errorf("order = %+v, want user 1 and the requested product", order)
	}
}

// An order for a product that does not exist must never reach the insert —
// the FK would reject it, but failing at the check is a better error.
func TestCreateOrderUnknownProduct(t *testing.T) {
	uc := New(&fakeRepo{availErr: errors.New(`product "X" not found`)}, &fakeCache{})

	if _, err := uc.CreateOrder(context.Background(), 1, domain.CreateOrderRequest{ProductID: "X"}); err == nil {
		t.Error("unknown product accepted, want rejection")
	}
}
