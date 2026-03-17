package middleware

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

var limiters sync.Map

// RateLimitMiddleware limits requests per IP using a token bucket.
// r is the rate (requests/second), b is the burst size.
func RateLimitMiddleware(r rate.Limit, b int) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		val, _ := limiters.LoadOrStore(ip, rate.NewLimiter(r, b))
		if !val.(*rate.Limiter).Allow() {
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
