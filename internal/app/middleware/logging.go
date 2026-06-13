package middleware

import (
	"net/http"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

// LoggingMiddleware adds request logging using custom logger.
//
// Successful (2xx/3xx) GET requests — the bulk of the traffic: SSE, metric
// polls, static assets — log at Debug so they don't drown the Info stream.
// Non-GET requests and any >=400 response stay at Info so they remain visible
// at the default level; >=500 logs at Error.
func LoggingMiddleware(logger *log.Logger) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		start := time.Now()
		c.Next()

		// Get response status
		status := c.Writer.Status()

		level := log.InfoLevel
		switch {
		case status >= 500:
			level = log.ErrorLevel
		case status >= 400:
			// Client/server errors stay visible at Info.
			level = log.InfoLevel
		case c.Request.Method == http.MethodGet:
			// Successful read traffic is noise at Info.
			level = log.DebugLevel
		}

		logger.Log(level, "HTTP Request",
			"method", c.Request.Method,
			"path", c.Request.RequestURI,
			"status", status,
			"duration", time.Since(start),
			"client_ip", c.ClientIP(),
			"user_agent", c.GetHeader("User-Agent"),
		)
	})
}

// CORSMiddleware adds CORS headers. allowOrigin should be "*" or a specific origin.
// Set allowCredentials to true when using HttpOnly cookies (requires a specific origin, not "*").
func CORSMiddleware(allowOrigin string, allowCredentials bool) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", allowOrigin)
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
		if allowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})
}
