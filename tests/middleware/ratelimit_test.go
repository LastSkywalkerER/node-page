package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"system-stats/internal/app/middleware"
)

func newRateLimitRouter(r rate.Limit, burst int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.RateLimitMiddleware(r, burst))
	engine.GET("/test", func(c *gin.Context) { c.Status(200) })
	return engine
}

func doRequest(router *gin.Engine, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = ip + ":12345"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestRateLimit_AllowsBurst(t *testing.T) {
	router := newRateLimitRouter(1, 10)
	ip := "10.0.0.1"

	for i := range 10 {
		rec := doRequest(router, ip)
		if rec.Code != 200 {
			t.Fatalf("request %d: status = %d, want 200", i+1, rec.Code)
		}
	}
}

func TestRateLimit_BlocksExcess(t *testing.T) {
	router := newRateLimitRouter(1, 10)
	ip := "10.0.0.2"

	for range 10 {
		doRequest(router, ip)
	}

	rec := doRequest(router, ip)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("burst+1 request: status = %d, want 429", rec.Code)
	}
}

func TestRateLimit_IsolatesIPs(t *testing.T) {
	router := newRateLimitRouter(1, 5)
	ipA := "10.0.0.3"
	ipB := "10.0.0.4"

	for range 5 {
		doRequest(router, ipA)
	}

	rec := doRequest(router, ipA)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("ipA after burst: status = %d, want 429", rec.Code)
	}

	rec = doRequest(router, ipB)
	if rec.Code != 200 {
		t.Fatalf("ipB first request: status = %d, want 200", rec.Code)
	}
}
