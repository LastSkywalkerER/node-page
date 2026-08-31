package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// contentSecurityPolicy is tuned to what the SPA actually loads:
//   - default/script/connect 'self': the app bundle is same-origin, and every
//     browser request (REST, the SSE /stream, the wallpaper proxy) hits our own
//     origin — no third-party script or XHR host is allowed, so an injected
//     <script src=evil> or exfil fetch is blocked.
//   - style 'unsafe-inline' + fonts.googleapis: Recharts injects an inline
//     <style> for chart colors and Tailwind emits inline style attributes, and
//     the font stylesheet is @imported from Google Fonts. Inline STYLE can't run
//     JS, so this is a far smaller surface than inline script (which stays off).
//   - font-src gstatic: the Google Fonts stylesheet pulls its font files there.
//   - img-src 'self' data: blob: https:: the dynamic wallpaper (Pexels CDN) and
//     app icons come from arbitrary https image hosts; images don't execute, so
//     allowing https images is safe while still blocking http (mixed content).
//   - object-src 'none', base-uri 'self', frame-ancestors 'none',
//     form-action 'self': kill plugin embedding, <base> hijacking, clickjacking
//     (stronger than X-Frame-Options), and cross-origin form posts.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"img-src 'self' data: blob: https:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"frame-ancestors 'none'; " +
	"form-action 'self'"

// SecurityHeaders sets common defensive HTTP response headers.
// hstsEnabled should mirror cookieSecure: HSTS only when running behind TLS.
func SecurityHeaders(hstsEnabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("X-Permitted-Cross-Domain-Policies", "none")
		// CSP applies to the app's own pages. Swagger UI is exempt: it ships an
		// inline bootstrap script and inline styles that a strict script-src
		// would break, and it's an admin/dev diagnostic, not user-facing.
		if !strings.HasPrefix(c.Request.URL.Path, "/swagger") {
			c.Header("Content-Security-Policy", contentSecurityPolicy)
		}
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
