package middleware

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type rateLimitEntry struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64
}

var (
	limiters    sync.Map
	cleanupOnce sync.Once
)

func startCleanup() {
	go func() {
		for range time.Tick(10 * time.Minute) {
			cutoff := time.Now().Add(-10 * time.Minute).UnixNano()
			limiters.Range(func(key, val any) bool {
				if val.(*rateLimitEntry).lastSeen.Load() < cutoff {
					limiters.Delete(key)
				}
				return true
			})
		}
	}()
}

// RateLimitMiddleware limits requests per IP using a token bucket.
// r is the rate (requests/second), b is the burst size.
func RateLimitMiddleware(r rate.Limit, b int) gin.HandlerFunc {
	cleanupOnce.Do(startCleanup)

	return func(c *gin.Context) {
		ip := c.ClientIP()
		now := time.Now().UnixNano()

		val, loaded := limiters.LoadOrStore(ip, &rateLimitEntry{
			limiter: rate.NewLimiter(r, b),
		})
		entry := val.(*rateLimitEntry)
		entry.lastSeen.Store(now)

		if loaded {
			// Entry already existed; nothing extra to do.
		}

		if !entry.limiter.Allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":  "rate_limit_exceeded",
				"error": "Too many requests, please try again later",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
