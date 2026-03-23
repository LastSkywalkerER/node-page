package presentation

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"system-stats/internal/app/stream"
	hostservice "system-stats/internal/modules/hosts/application"
)

// StreamHandler serves the SSE endpoint.
type StreamHandler struct {
	broker *stream.Broker
	hosts  hostservice.Service
}

func NewStreamHandler(broker *stream.Broker, hosts hostservice.Service) *StreamHandler {
	return &StreamHandler{broker: broker, hosts: hosts}
}

// HandleStream streams live metrics to connected SSE clients.
//
// @Summary     Live metrics stream (SSE)
// @Description Establishes a Server-Sent Events connection that pushes aggregated system metrics every collection cycle for this server instance only.
// @Tags        stream
// @Produce     text/event-stream
// @Param       host_id  query    integer  false  "Host ID (0 = this server instance); remote hosts receive keepalive only"
// @Success     200  {string} string  "SSE event stream"
// @Failure     401  {object} map[string]string
// @Security    BearerAuth
// @Router      /stream [get]
func (h *StreamHandler) HandleStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable nginx buffering

	ctx := c.Request.Context()
	queryHost := uint(0)
	if s := c.Query("host_id"); s != "" {
		if v, err := strconv.ParseUint(s, 10, 32); err == nil {
			queryHost = uint(v)
		}
	}

	effectiveHost := queryHost
	if queryHost == 0 {
		cur, err := h.hosts.GetCurrentHost(ctx)
		if err == nil && cur != nil {
			effectiveHost = cur.ID
		}
	} else {
		_, err := h.hosts.GetHostByID(ctx, queryHost)
		if err != nil {
			// Unknown host — keepalive only
			h.keepaliveLoop(c)
			return
		}
	}

	current, err := h.hosts.GetCurrentHost(ctx)
	if err != nil || current == nil {
		h.keepaliveLoop(c)
		return
	}
	if effectiveHost != current.ID {
		h.keepaliveLoop(c)
		return
	}

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

func (h *StreamHandler) keepaliveLoop(c *gin.Context) {
	t := time.NewTicker(25 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			_, _ = fmt.Fprintf(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			return
		}
	}
}
