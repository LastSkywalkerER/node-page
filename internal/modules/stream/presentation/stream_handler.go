package presentation

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"system-stats/internal/app/stream"
)

// StreamHandler serves the SSE endpoint.
type StreamHandler struct {
	broker *stream.Broker
}

func NewStreamHandler(broker *stream.Broker) *StreamHandler {
	return &StreamHandler{broker: broker}
}

// HandleStream streams live metrics to connected SSE clients.
// GET /api/v1/stream
func (h *StreamHandler) HandleStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable nginx buffering

	ch := h.broker.Subscribe()
	defer h.broker.Unsubscribe(ch)

	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(c.Writer, "event: metrics\ndata: %s\n\n", data)
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			return
		}
	}
}
