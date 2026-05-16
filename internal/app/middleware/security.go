package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders sets common defensive HTTP response headers.
// hstsEnabled should mirror cookieSecure: HSTS only when running behind TLS.
// CSP intentionally omitted — would need per-route tuning to not break Swagger UI / dev tools.
func SecurityHeaders(hstsEnabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("X-Permitted-Cross-Domain-Policies", "none")
		if hstsEnabled {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
