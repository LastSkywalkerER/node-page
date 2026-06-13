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

	cpu "system-stats/internal/metrics/cpu"
	disk "system-stats/internal/metrics/disk"
	memory "system-stats/internal/metrics/memory"
	network "system-stats/internal/metrics/network"
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
	cpuSvc cpu.Service,
	memSvc memory.Service,
	diskSvc disk.Service,
	netSvc network.Service,
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
		if raw := c.Request.URL.Path; strings.HasPrefix(raw, "/api/v1/metrics") || strings.HasPrefix(raw, "/swagger") {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		// Label on the ROUTE TEMPLATE (e.g. /api/v1/hosts/:id), NOT the concrete
		// request path. A Prometheus *Vec permanently retains one child series per
		// distinct label tuple with no eviction, so labeling by raw path — where
		// :id / :slug / :project churn (every host id, every app-icon slug the
		// frontend requests) — grows the registry without bound and is a real
		// memory leak. c.FullPath collapses concrete paths into their pattern;
		// unmatched routes (empty template) bucket under a single label.
		path := c.FullPath()
		if path == "" {
			path = "unmatched"
		}

		m.httpRequestsTotal.WithLabelValues(
			c.Request.Method,
			path,
			strconv.Itoa(c.Writer.Status()),
		).Inc()
		m.httpRequestDuration.WithLabelValues(c.Request.Method, path).Observe(time.Since(start).Seconds())
	}
}
