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
	limiters      sync.Map
	cleanupOnce   sync.Once
	cleanupDone   chan struct{}
	cleanupTicker *time.Ticker
)

func startCleanup() {
	cleanupTicker = time.NewTicker(10 * time.Minute)
	cleanupDone = make(chan struct{})
	go func() {
		defer cleanupTicker.Stop()
		for {
			select {
			case <-cleanupTicker.C:
				cutoff := time.Now().Add(-10 * time.Minute).UnixNano()
				limiters.Range(func(key, val any) bool {
					if val.(*rateLimitEntry).lastSeen.Load() < cutoff {
						limiters.Delete(key)
					}
					return true
				})
			case <-cleanupDone:
				return
			}
		}
	}()
}

// StopRateLimiterCleanup stops the background cleanup goroutine.
// Safe to call multiple times. Call during graceful shutdown.
func StopRateLimiterCleanup() {
	if cleanupDone != nil {
		select {
		case <-cleanupDone:
			// already closed
		default:
			close(cleanupDone)
		}
	}
}

// RateLimitMiddleware limits requests per IP using a token bucket.
// r is the rate (requests/second), b is the burst size.
func RateLimitMiddleware(r rate.Limit, b int) gin.HandlerFunc {
	cleanupOnce.Do(startCleanup)

	return func(c *gin.Context) {
		ip := c.ClientIP()
		now := time.Now().UnixNano()

		val, _ := limiters.LoadOrStore(ip, &rateLimitEntry{
			limiter: rate.NewLimiter(r, b),
		})
		entry := val.(*rateLimitEntry)
		entry.lastSeen.Store(now)

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
