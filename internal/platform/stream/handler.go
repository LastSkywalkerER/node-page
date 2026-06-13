package stream

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	appstream "system-stats/internal/app/stream"
	hosts "system-stats/internal/cluster/hosts"
)

// Handler serves the SSE endpoint.
type Handler struct {
	broker *appstream.Broker
	hosts  hosts.Service
}

// NewHandler creates a new stream handler.
func NewHandler(broker *appstream.Broker, hostsvc hosts.Service) *Handler {
	return &Handler{broker: broker, hosts: hostsvc}
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
func (h *Handler) HandleStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable nginx buffering

	ctx := c.Request.Context()
	// A specific, unknown host_id gets keepalives only (nothing to stream).
	if s := c.Query("host_id"); s != "" {
		if v, err := strconv.ParseUint(s, 10, 32); err == nil && v != 0 {
			if _, err := h.hosts.GetHostByID(ctx, uint(v)); err != nil {
				h.keepaliveLoop(c)
				return
			}
		}
	}

	// Every client subscribes to this node's metric stream and filters by
	// collecting_host_id on the client. The node publishes its own host's
	// metrics each collection cycle AND every replicated peer's metrics as
	// their Raft batches apply, so any host viewed on any node updates live.
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

func (h *Handler) keepaliveLoop(c *gin.Context) {
	t := time.NewTicker(25 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			_, _ = fmt.Fprintf(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			return
		case <-h.broker.Done():
			// Broker shut down (graceful shutdown) — exit so the handler
			// returns and the connection can drain.
			return
		}
	}
}
