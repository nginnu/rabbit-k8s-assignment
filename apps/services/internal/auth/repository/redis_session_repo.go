package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/nginnu/rabbit-k8s-assignment/apps/services/internal/auth/domain"
	"github.com/redis/go-redis/v9"
)

// redisSessionRepo implements domain.SessionRepository using Redis.
type redisSessionRepo struct {
	client *redis.Client
}

// NewRedisSessionRepo creates a new Redis-backed SessionRepository.
func NewRedisSessionRepo(client *redis.Client) domain.SessionRepository {
	return &redisSessionRepo{client: client}
}

// Set stores session:<jti> = userID with the given TTL.
func (r *redisSessionRepo) Set(ctx context.Context, jti string, userID int, ttl time.Duration) error {
	key := fmt.Sprintf("session:%s", jti)
	return r.client.Set(ctx, key, userID, ttl).Err()
}
