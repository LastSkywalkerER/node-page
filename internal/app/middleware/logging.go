package middleware

import (
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

// LoggingMiddleware adds request logging using custom logger
func LoggingMiddleware(logger *log.Logger) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		start := time.Now()
		c.Next()

		// Get response status
		status := c.Writer.Status()

		// Log request with detailed information
		logger.Info("HTTP Request",
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
