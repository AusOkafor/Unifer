package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RateLimit implements a sliding window rate limiter per merchant using Redis INCR+EXPIRE.
// rps is the maximum number of requests per second per merchant.
func RateLimit(redisClient *redis.Client, rps int) gin.HandlerFunc {
	return func(c *gin.Context) {
		merchant := GetMerchant(c)
		if merchant == nil {
			c.Next()
			return
		}

		key := fmt.Sprintf("ratelimit:%s:%d", merchant.ID.String(), time.Now().Unix())
		ctx := c.Request.Context()

		count, err := redisClient.Incr(ctx, key).Result()
		if err != nil {
			// On Redis error, allow the request through (fail open)
			c.Next()
			return
		}

		if count == 1 {
			// First request in this second — set TTL
			redisClient.Expire(ctx, key, 2*time.Second)
		}

		if int(count) > rps {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}

		c.Next()
	}
}
