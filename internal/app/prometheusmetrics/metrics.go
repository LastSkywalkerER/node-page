package prometheusmetrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	cpuservice "system-stats/internal/modules/cpu/application"
	diskservice "system-stats/internal/modules/disk/application"
	memoryservice "system-stats/internal/modules/memory/application"
	networkservice "system-stats/internal/modules/network/application"
)

// Metrics holds a dedicated Prometheus registry and all application metric instruments.
type Metrics struct {
	registry            *prometheus.Registry
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
}

// New creates a Prometheus registry populated with Go runtime metrics, process metrics,
// system metrics (CPU/RAM/disk/network), and HTTP request metrics.
func New(
	cpuSvc cpuservice.Service,
	memSvc memoryservice.Service,
	diskSvc diskservice.Service,
	netSvc networkservice.Service,
) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		newSystemCollector(cpuSvc, memSvc, diskSvc, netSvc),
	)

	httpReqs := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests, labeled by method, path, and status code.",
	}, []string{"method", "path", "status_code"})

	httpDur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds, labeled by method and path.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	reg.MustRegister(httpReqs, httpDur)

	return &Metrics{
		registry:            reg,
		httpRequestsTotal:   httpReqs,
		httpRequestDuration: httpDur,
	}
}

// Handler returns an http.Handler that serves the Prometheus metrics page.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// GinMiddleware returns a Gin middleware that records HTTP request metrics.
// Requests to /metrics and /swagger are excluded to avoid self-referential noise.
func (m *Metrics) GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/v1/metrics") || strings.HasPrefix(path, "/swagger") {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		m.httpRequestsTotal.WithLabelValues(
			c.Request.Method,
			path,
			strconv.Itoa(c.Writer.Status()),
		).Inc()
		m.httpRequestDuration.WithLabelValues(c.Request.Method, path).Observe(time.Since(start).Seconds())
	}
}
