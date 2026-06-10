package docker

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"

	"system-stats/internal/app/httputil"
	"system-stats/internal/app/metricshost"
	hosts "system-stats/internal/cluster/hosts"
)

// Handler handles HTTP requests for Docker container metrics.
type Handler struct {
	logger  *log.Logger
	service Service
	hosts   hosts.Service
}

// NewHandler creates a new HTTP handler for Docker metrics endpoints.
func NewHandler(logger *log.Logger, service Service, hostsvc hosts.Service) *Handler {
	return &Handler{
		logger:  logger,
		service: service,
		hosts:   hostsvc,
	}
}

// HandleDockerStats returns Docker container statistics and status information with latest and historical data.
//
// @Summary     Docker metrics
// @Description Returns Docker container stats (running count, resource usage) with history.
// @Tags        metrics
// @Produce     json
// @Param       hours    query    number   false  "History window in hours"                                  default(0.0833)
// @Param       host_id  query    integer  false  "Host ID (0 = this server instance)"
// @Success     200      {object} map[string]interface{}
// @Failure     401      {object} map[string]string
// @Failure     500      {object} map[string]string
// @Security    BearerAuth
// @Router      /docker [get]
func (h *Handler) HandleDockerStats(c *gin.Context) {
	hours := httputil.ParseHoursQuery(c)
	queryHost := httputil.ParseHostIdQuery(c)
	ctx := c.Request.Context()

	effective, err := metricshost.EffectiveHostID(ctx, h.hosts, queryHost)
	if errors.Is(err, metricshost.ErrHostNotFound) {
		c.JSON(http.StatusOK, metricshost.EmptyDockerPayload())
		return
	}
	if err != nil {
		h.logger.Error("Failed to resolve host for Docker metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":            err.Error(),
			"docker_available": false,
		})
		return
	}

	latestMetrics, err := h.service.GetLatestByHost(ctx, effective)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			h.logger.Info("Client canceled request while fetching latest Docker metrics")
			return
		}
		h.logger.Error("Failed to fetch latest Docker metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":            err.Error(),
			"docker_available": false,
		})
		return
	}

	historyMetrics, err := h.service.GetHistoricalByHost(ctx, effective, hours)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			h.logger.Info("Client canceled request while fetching historical Docker metrics")
			return
		}
		h.logger.Error("Failed to fetch historical Docker metrics", "error", err, "hours", hours, "host_id", effective)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":            err.Error(),
			"docker_available": false,
		})
		return
	}

	dockerAvailable := false
	if latestMetrics != nil {
		dockerAvailable = latestMetrics.DockerAvailable
	}

	c.JSON(http.StatusOK, gin.H{
		"latest":           latestMetrics,
		"history":          historyMetrics,
		"docker_available": dockerAvailable,
	})
}

// resolveHost resolves the host_id query into an effective host ID, writing the
// appropriate error response and returning ok=false on failure.
func (h *Handler) resolveHost(c *gin.Context, ctx context.Context) (uint, bool) {
	queryHost := httputil.ParseHostIdQuery(c)
	effective, err := metricshost.EffectiveHostID(ctx, h.hosts, queryHost)
	if errors.Is(err, metricshost.ErrHostNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "host not found"})
		return 0, false
	}
	if err != nil {
		h.logger.Error("Failed to resolve host", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return 0, false
	}
	return effective, true
}

// HandleApplications returns applications grouped from containers. With no
// host_id it aggregates across every host (main screen); with host_id it scopes
// to that node. Cross-node data is available locally because Raft replicates
// each host's docker snapshot into every node's DB.
//
// @Summary     Docker applications
// @Tags        metrics
// @Produce     json
// @Param       host_id  query    integer  false  "Host ID (omit = all hosts; 0 = this server instance)"
// @Success     200      {object} map[string]interface{}
// @Security    BearerAuth
// @Router      /applications [get]
func (h *Handler) HandleApplications(c *gin.Context) {
	ctx := c.Request.Context()

	// No host_id param → aggregate across all hosts (main screen).
	if _, hasHost := c.GetQuery("host_id"); !hasHost {
		allHosts, err := h.hosts.GetAllHosts(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			h.logger.Error("Failed to list hosts for applications", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		apps := []DockerApplication{}
		for _, host := range allHosts {
			hostApps, err := h.service.GetApplicationsByHost(ctx, host.ID)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				h.logger.Warn("Failed to fetch applications for host", "error", err, "host_id", host.ID)
				continue
			}
			for i := range hostApps {
				hostApps[i].HostID = host.ID
			}
			apps = append(apps, hostApps...)
		}
		c.JSON(http.StatusOK, gin.H{"applications": apps, "docker_available": true})
		return
	}

	effective, ok := h.resolveHost(c, ctx)
	if !ok {
		return
	}
	apps, err := h.service.GetApplicationsByHost(ctx, effective)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if apps == nil {
		apps = []DockerApplication{}
	}
	for i := range apps {
		apps[i].HostID = effective
	}
	c.JSON(http.StatusOK, gin.H{"applications": apps, "docker_available": true})
}

// HandleApplicationDetail returns a single application with its containers.
//
// @Summary  Application detail
// @Tags     metrics
// @Produce  json
// @Param    project  path   string   true   "Application project key"
// @Param    host_id  query  integer  false  "Host ID"
// @Security BearerAuth
// @Router   /applications/{project} [get]
func (h *Handler) HandleApplicationDetail(c *gin.Context) {
	ctx := c.Request.Context()
	effective, ok := h.resolveHost(c, ctx)
	if !ok {
		return
	}
	app, err := h.service.GetApplication(ctx, effective, c.Param("project"))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if app == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"application": app})
}

// HandleApplicationCompose returns the composition (synthetic always; real
// compose file only for the local node, when its path is reachable).
//
// @Summary  Application composition
// @Tags     metrics
// @Produce  json
// @Param    project  path   string   true   "Application project key"
// @Param    host_id  query  integer  false  "Host ID"
// @Security BearerAuth
// @Router   /applications/{project}/compose [get]
func (h *Handler) HandleApplicationCompose(c *gin.Context) {
	ctx := c.Request.Context()
	effective, ok := h.resolveHost(c, ctx)
	if !ok {
		return
	}
	allowReal := effective == hosts.LocalCollectorHostID
	view, err := h.service.GetComposeView(ctx, effective, c.Param("project"), allowReal)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if view == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}
	c.JSON(http.StatusOK, view)
}

// HandleApplicationLogs returns recent logs for a container of an application,
// read from this node's docker daemon. Containers running on a different node
// are not on the local daemon, so their logs return available=false.
//
// @Summary  Application container logs
// @Tags     metrics
// @Produce  json
// @Param    project    path   string   true   "Application project key"
// @Param    host_id    query  integer  false  "Host ID"
// @Param    container  query  string   false  "Container ID (defaults to first running)"
// @Param    tail       query  integer  false  "Trailing log lines"  default(200)
// @Security BearerAuth
// @Router   /applications/{project}/logs [get]
func (h *Handler) HandleApplicationLogs(c *gin.Context) {
	ctx := c.Request.Context()
	effective, ok := h.resolveHost(c, ctx)
	if !ok {
		return
	}
	app, err := h.service.GetApplication(ctx, effective, c.Param("project"))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if app == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}

	// Resolve + validate the container against the application's members.
	requested := strings.TrimSpace(c.Query("container"))
	var chosen *DockerContainer
	for i := range app.Containers {
		ctr := &app.Containers[i]
		if requested == "" {
			if ctr.State == "running" {
				chosen = ctr
				break
			}
			continue
		}
		if ctr.ID == requested || strings.HasPrefix(ctr.ID, requested) || strings.HasPrefix(requested, ctr.ID) {
			chosen = ctr
			break
		}
	}
	if chosen == nil && requested == "" && len(app.Containers) > 0 {
		chosen = &app.Containers[0]
	}
	if chosen == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "container not found in application"})
		return
	}
	containerID := chosen.ID
	// The stored id can be stale (swarm recreates task containers); pass enough
	// identity so the collector resolves the current live container.
	ref := ContainerLogRef{
		ID:           chosen.ID,
		Name:         chosen.Name,
		Project:      chosen.Project,
		Service:      chosen.Service,
		SwarmService: chosen.Labels["com.docker.swarm.service.name"],
	}

	tail := 200
	if t := c.Query("tail"); t != "" {
		if n, perr := strconv.Atoi(t); perr == nil {
			tail = n
		}
	}
	if tail < 1 {
		tail = 1
	}
	if tail > 5000 {
		tail = 5000
	}

	logs, err := h.service.GetContainerLogs(ctx, ref, tail)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		// Container not on this node's daemon (running on a remote node) or read failed.
		reason := "Logs are not available from this node for that container."
		if effective == hosts.LocalCollectorHostID {
			reason = err.Error()
		}
		c.JSON(http.StatusOK, gin.H{"available": false, "logs": "", "reason": reason, "container": containerID})
		return
	}
	c.JSON(http.StatusOK, gin.H{"available": true, "logs": logs, "container": containerID})
}

// HandleTraefikDiscovery returns the domain-detection diagnostic: which Traefik
// config dirs were configured/discovered and are readable from this process,
// which routes parsed out of them, and why each route did or didn't attach to
// a container. Reflects the LOCAL node's daemon and filesystem only.
//
// GET /api/v1/docker/traefik-discovery
func (h *Handler) HandleTraefikDiscovery(c *gin.Context) {
	c.JSON(http.StatusOK, h.service.TraefikDiscovery(c.Request.Context()))
}
