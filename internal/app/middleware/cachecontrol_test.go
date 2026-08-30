package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCacheControl(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/auth/login", "no-store"},
		{"/api/v1/cpu", "no-store"},
		{"/assets/index-abc123.js", "public, max-age=31536000, immutable"},
		{"/", "no-cache"},
		{"/machines/1/stats", "no-cache"},
	}

	for _, tc := range cases {
		r := gin.New()
		r.Use(CacheControl())
		r.NoRoute(func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))

		if got := w.Header().Get("Cache-Control"); got != tc.want {
			t.Errorf("%s: Cache-Control = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// Handlers that care about caching (the wallpaper proxy) set their own value;
// the middleware runs first, so the handler's header must win.
func TestCacheControlHandlerOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(CacheControl())
	r.GET("/api/v1/wallpaper", func(c *gin.Context) {
		c.Header("Cache-Control", "private, max-age=600")
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/wallpaper", nil))

	if got := w.Header().Get("Cache-Control"); got != "private, max-age=600" {
		t.Errorf("Cache-Control = %q, want handler value", got)
	}
}
