package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// AuthBearerToken returns middleware that validates a static Bearer token
// from the Authorization header. Used to protect the Prometheus /metrics endpoint.
func AuthBearerToken(expectedToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		token, found := strings.CutPrefix(header, "Bearer ")
		if !found {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization scheme"})
			return
		}

		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid token"})
			return
		}

		c.Next()
	}
}
