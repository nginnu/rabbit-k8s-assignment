// Package usecase implements the catalog's business logic: the product list
// behind a cache-aside, moved here from order-svc when the two contexts split.
package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/catalog/domain"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("catalog/usecase")

const (
	productListCacheKey = "products:list"
	productListCacheTTL = 60 * time.Second
)

// redisCache adapts *redis.Client to domain.ProductCache. It is the only file
// in the flow that knows go-redis exists, so a test replaces the cache without
// a Redis on the other end.
type redisCache struct {
	rdb *redis.Client
}

// NewRedisCache wraps a live client for the usecase.
func NewRedisCache(rdb *redis.Client) domain.ProductCache {
	return redisCache{rdb: rdb}
}

func (c redisCache) GetBytes(ctx context.Context, key string) ([]byte, error) {
	b, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, domain.ErrCacheMiss
	}
	return b, err
}

func (c redisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

// CatalogUsecase serves product reads.
type CatalogUsecase struct {
	repo  domain.ProductRepository
	cache domain.ProductCache
}

// New creates a new CatalogUsecase.
func New(repo domain.ProductRepository, cache domain.ProductCache) *CatalogUsecase {
	return &CatalogUsecase{repo: repo, cache: cache}
}

// ListProducts returns all available products with cache-aside pattern.
//
// Spans:
//
//	usecase.list_products
//	  ├─ cache.product.get  → HIT → return
//	  │                     → MISS → continue
//	  ├─ repo.product.list  (otelgorm SELECT)
//	  └─ cache.product.set  (TTL 60s)
func (u *CatalogUsecase) ListProducts(ctx context.Context) ([]domain.Product, error) {
	ctx, span := tracer.Start(ctx, "list products")
	defer span.End()

	// 1. Try cache (otelredis instruments GET as a span automatically)
	cached, err := u.cache.GetBytes(ctx, productListCacheKey)
	if err == nil {
		var products []domain.Product
		if jsonErr := json.Unmarshal(cached, &products); jsonErr == nil {
			span.SetAttributes(
				attribute.String("cache.result", "hit"),
				attribute.Int("product.count", len(products)),
			)
			return products, nil
		}
		// Cache value corrupt → fall through to DB
	} else if !errors.Is(err, domain.ErrCacheMiss) {
		// Network/Redis error — log but don't fail (degrade to DB)
		span.AddEvent("cache.error", trace.WithAttributes(attribute.String("err", err.Error())))
	}

	span.SetAttributes(attribute.String("cache.result", "miss"))

	// 2. Cache miss → query DB
	products, err := u.repo.ListProducts(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	// 3. Write back to cache, best effort — a failed SET does not fail the request.
	if data, marshalErr := json.Marshal(products); marshalErr == nil {
		if setErr := u.cache.Set(ctx, productListCacheKey, data, productListCacheTTL); setErr != nil {
			span.AddEvent("cache.set_failed",
				trace.WithAttributes(attribute.String("err", setErr.Error())))
		}
	}

	span.SetAttributes(attribute.Int("product.count", len(products)))
	return products, nil
}
