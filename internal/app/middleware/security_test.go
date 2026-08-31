package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func runSecurity(t *testing.T, path string, hsts bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders(hsts))
	r.GET("/*any", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func TestSecurityHeadersBaseline(t *testing.T) {
	w := runSecurity(t, "/machines", false)
	for h, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := w.Header().Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must be absent when not behind TLS")
	}
}

func TestSecurityHeadersHSTSWhenSecure(t *testing.T) {
	w := runSecurity(t, "/machines", true)
	if !strings.Contains(w.Header().Get("Strict-Transport-Security"), "max-age=31536000") {
		t.Error("HSTS must be set when secure")
	}
}

func TestCSPPresentOnAppRoutes(t *testing.T) {
	w := runSecurity(t, "/machines", false)
	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("CSP must be set on app routes")
	}
	// Load-bearing directives: no third-party scripts, no framing, images over
	// https allowed (wallpaper/icons), fonts from Google.
	for _, must := range []string{
		"script-src 'self'",
		"frame-ancestors 'none'",
		"object-src 'none'",
		"img-src 'self' data: blob: https:",
		"font-src 'self' https://fonts.gstatic.com",
		"connect-src 'self'",
	} {
		if !strings.Contains(csp, must) {
			t.Errorf("CSP missing %q; got: %s", must, csp)
		}
	}
	// Inline script must NOT be allowed (only inline style is).
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Error("script-src must not allow unsafe-inline")
	}
}

func TestCSPExemptOnSwagger(t *testing.T) {
	w := runSecurity(t, "/swagger/index.html", false)
	if w.Header().Get("Content-Security-Policy") != "" {
		t.Error("Swagger UI must be exempt from CSP (needs inline bootstrap script)")
	}
}
