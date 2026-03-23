package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	nodeservice "system-stats/internal/modules/nodes/application"
	userservice "system-stats/internal/modules/users/application"
)

// AuthJWT middleware validates JWT tokens and sets user context.
// It reads the token from the access_token cookie first, then falls back to Authorization header.
func AuthJWT(tokenService userservice.TokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var tokenString string

		// Prefer HttpOnly cookie
		if cookie, err := c.Cookie("access_token"); err == nil && cookie != "" {
			tokenString = cookie
		} else {
			// Fallback to Authorization header for API clients / backward compat
			authHeader := c.GetHeader("Authorization")
			if authHeader == "" {
				c.JSON(http.StatusUnauthorized, gin.H{
					"code":  "unauthorized",
					"error": "Authorization required",
				})
				c.Abort()
				return
			}
			const bearerPrefix = "Bearer "
			if !strings.HasPrefix(authHeader, bearerPrefix) {
				c.JSON(http.StatusUnauthorized, gin.H{
					"code":  "unauthorized",
					"error": "Invalid authorization header format",
				})
				c.Abort()
				return
			}
			tokenString = strings.TrimPrefix(authHeader, bearerPrefix)
		}

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

// AuthNodeToken middleware validates node access tokens for push endpoint.
// Expects Authorization: Bearer {node_access_token}, sets hostID in context.
func AuthNodeToken(nodeService nodeservice.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":  "unauthorized",
				"error": "Authorization required",
			})
			c.Abort()
			return
		}
		const bearerPrefix = "Bearer "
		if !strings.HasPrefix(authHeader, bearerPrefix) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":  "unauthorized",
				"error": "Invalid authorization header format",
			})
			c.Abort()
			return
		}
		token := strings.TrimPrefix(authHeader, bearerPrefix)
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":  "unauthorized",
				"error": "Token required",
			})
			c.Abort()
			return
		}

		hostID, err := nodeService.ValidateNodeToken(c.Request.Context(), token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":  "unauthorized",
				"error": "Invalid node token",
			})
			c.Abort()
			return
		}

		c.Set("hostID", hostID)
		c.Next()
	}
}

