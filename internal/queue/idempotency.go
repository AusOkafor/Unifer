package queue

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// WebhookIdempotencyStore marks Shopify webhook IDs as processed so that
// Shopify retries (up to 19 attempts over 48 hours) are silently dropped
// instead of triggering duplicate detection or cache mutations.
type WebhookIdempotencyStore struct {
	redis *redis.Client
	// ttl is how long a processed key is kept in Redis.
	// 25 hours covers Shopify's entire retry window with a safety margin.
	ttl time.Duration
}

func NewWebhookIdempotencyStore(client *redis.Client) *WebhookIdempotencyStore {
	return &WebhookIdempotencyStore{
		redis: client,
		ttl:   25 * time.Hour,
	}
}

// IsProcessed checks whether the given webhook ID has already been handled.
// If not, it atomically marks it as processed and returns false.
// If yes (duplicate delivery), it returns true — the caller should skip processing.
func (s *WebhookIdempotencyStore) IsProcessed(ctx context.Context, webhookID string) (bool, error) {
	key := "webhook:processed:" + webhookID
	// SET key 1 NX EX <ttl> — only sets if key does not exist.
	// Returns nil if key was set (first delivery), ErrNil if it already existed.
	set, err := s.redis.SetNX(ctx, key, 1, s.ttl).Result()
	if err != nil {
		return false, err
	}
	// SetNX returns true when the key was newly created (first delivery).
	// We want IsProcessed to return true when it's a duplicate (key already existed).
	return !set, nil
}
