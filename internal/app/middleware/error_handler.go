package middleware

import (
	"errors"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	"system-stats/internal/app/apperror"
)

// ErrorHandler renders AppError responses. When debug is false, the Detail field
// (which can contain raw SQL/library errors) is logged but not exposed to clients.
func ErrorHandler(debug ...bool) gin.HandlerFunc {
	expose := false
	if len(debug) > 0 {
		expose = debug[0]
	}
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}

		err := c.Errors.Last().Err
		var appErr *apperror.AppError
		if errors.As(err, &appErr) {
			body := gin.H{"code": appErr.Code, "error": appErr.Message}
			if appErr.Detail != "" {
				if expose {
					body["detail"] = appErr.Detail
				} else {
					log.Debug("error detail (hidden from client)", "code", appErr.Code, "detail", appErr.Detail)
				}
			}
			c.JSON(appErr.HTTPStatus, body)
		} else {
			c.JSON(500, gin.H{"code": "internal_error", "error": "Internal server error"})
		}
	}
}
