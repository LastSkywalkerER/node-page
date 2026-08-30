package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

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

// CacheControl keeps browsers and any reverse proxy in front of node-stats
// (nginx/NPM, Traefik, a carrier cache) from serving a stale app or a stale
// API answer:
//
//   - /api/*    → no-store. API responses are per-user and cookie-bound; a
//     cached one served to the wrong client is both a bug and a leak.
//   - /assets/* → immutable. Vite content-hashes these, so a new build gets a
//     new URL and the old file can be cached forever.
//   - anything else (the SPA shell, index.html) → no-cache: cached, but
//     revalidated every time. Without this the shell falls under *heuristic*
//     caching, so a device that visited weeks ago can keep booting a months-old
//     bundle against a current API — which looks like "login works on my
//     laptop but not on my phone" and reports itself as whatever the stale
//     bundle happens to choke on.
func CacheControl() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		switch {
		case strings.HasPrefix(p, "/api/"):
			c.Header("Cache-Control", "no-store")
		case strings.HasPrefix(p, "/assets/"):
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		default:
			c.Header("Cache-Control", "no-cache")
		}
		c.Next()
	}
}
