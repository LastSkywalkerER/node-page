package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	userservice "system-stats/internal/modules/users/application"
)

// AuthJWT middleware validates JWT tokens and sets user context
func AuthJWT(tokenService userservice.TokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract token from Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":  "unauthorized",
				"error": "Authorization header required",
			})
			c.Abort()
			return
		}

		// Check Bearer token format
		const bearerPrefix = "Bearer "
		if !strings.HasPrefix(authHeader, bearerPrefix) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":  "unauthorized",
				"error": "Invalid authorization header format",
			})
			c.Abort()
			return
		}

		tokenString := strings.TrimPrefix(authHeader, bearerPrefix)
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":  "unauthorized",
				"error": "Token required",
			})
			c.Abort()
			return
		}

		// Validate token
		claims, err := tokenService.ValidateAccessToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":  "unauthorized",
				"error": "Invalid or expired token",
			})
			c.Abort()
			return
		}

		// Set user context
		c.Set("userID", claims.UserID)
		c.Set("userEmail", claims.Email)
		c.Set("userRole", claims.Role)
		c.Set("tokenJTI", claims.JTI)

		c.Next()
	}
}

// RequireRole middleware checks if the user has the required role
func RequireRole(requiredRole string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get("userRole")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":  "unauthorized",
				"error": "Authentication required",
			})
			c.Abort()
			return
		}

		if userRole.(string) != requiredRole {
			c.JSON(http.StatusForbidden, gin.H{
				"code":  "forbidden",
				"error": "Insufficient role",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAdmin middleware checks if the user has admin role
func RequireAdmin() gin.HandlerFunc {
	return RequireRole("ADMIN")
}

