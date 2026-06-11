package appicons

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handle serves GET /api/v1/app-icons/:slug. A hit is immutable enough for a
// day of browser caching; a miss is cached shorter so newly-published icons
// appear without a redeploy.
func (s *Service) Handle(c *gin.Context) {
	data, ctype, ok := s.Get(c.Request.Context(), c.Param("slug"))
	if !ok {
		c.Header("Cache-Control", "public, max-age=3600")
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=86400")
	c.Data(http.StatusOK, ctype, data)
}
