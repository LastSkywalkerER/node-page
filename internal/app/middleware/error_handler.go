package middleware

import (
	"errors"

	"github.com/gin-gonic/gin"

	"system-stats/internal/app/apperror"
)

func ErrorHandler() gin.HandlerFunc {
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
				body["detail"] = appErr.Detail
			}
			c.JSON(appErr.HTTPStatus, body)
		} else {
			c.JSON(500, gin.H{"code": "internal_error", "error": "Internal server error"})
		}
	}
}
