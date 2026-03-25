package presentation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"system-stats/internal/app/apperror"
	hostservice "system-stats/internal/modules/hosts/application"
	hostentities "system-stats/internal/modules/hosts/infrastructure/entities"
	nodeservice "system-stats/internal/modules/nodes/application"
	"system-stats/internal/modules/nodes/infrastructure/cluster_config"
)

// NodesHandler handles node join, invite, and connect HTTP requests.
type NodesHandler struct {
	nodeService           nodeservice.Service
	hostService           hostservice.Service
	publicBaseURLOverride string // PUBLIC_BASE_URL; when set, used for join links and cluster UI URLs (agents must reach this URL)
}

// NewNodesHandler creates a new nodes handler.
func NewNodesHandler(nodeService nodeservice.Service, hostService hostservice.Service, publicBaseURL string) *NodesHandler {
	return &NodesHandler{
		nodeService:           nodeService,
		hostService:           hostService,
		publicBaseURLOverride: strings.TrimSpace(publicBaseURL),
	}
}

// resolvePublicBaseURL is the base URL agents should use to reach this main (push, join).
func (h *NodesHandler) resolvePublicBaseURL(c *gin.Context) string {
	if s := strings.TrimSpace(h.publicBaseURLOverride); s != "" {
		return strings.TrimSuffix(s, "/")
	}
	proto := "http"
	if xf := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); xf != "" {
		proto = strings.TrimSpace(strings.Split(xf, ",")[0])
	} else if c.Request.TLS != nil {
		proto = "https"
	}
	host := c.Request.Host
	if fh := c.GetHeader("X-Forwarded-Host"); fh != "" {
		host = strings.TrimSpace(strings.Split(fh, ",")[0])
	}
	return strings.TrimSuffix(proto+"://"+host, "/")
}

// JoinRequest represents the host info sent by an agent during join.
type JoinRequest struct {
	Name                 string `json:"name"`
	MacAddress           string `json:"mac_address"`
	IPv4                 string `json:"ipv4"`
	OS                   string `json:"os"`
	Platform             string `json:"platform"`
	PlatformFamily       string `json:"platform_family"`
	PlatformVersion      string `json:"platform_version"`
	KernelVersion        string `json:"kernel_version"`
	VirtualizationSystem string `json:"virtualization_system"`
	VirtualizationRole   string `json:"virtualization_role"`
	HostID string `json:"host_id"`
}

// Join handles node registration (public, no JWT).
//
// @Summary     Join cluster
// @Description Registers a node with the main server using a one-time join token. Returns host_id and node_access_token for push auth.
// @Tags        nodes
// @Accept      json
// @Produce     json
// @Param       token  query    string  true  "Join token"
// @Param       body   body     JoinRequest  true  "Host info"
// @Success     200    {object} map[string]interface{}
// @Failure     400    {object} map[string]string
// @Failure     500    {object} map[string]string
// @Router      /nodes/join [post]
func (h *NodesHandler) Join(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		_ = c.Error(apperror.BadRequest("token_required", "Join token is required"))
		return
	}

	var req JoinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperror.WithDetail(apperror.BadRequest("validation_error", "Invalid request data"), err.Error()))
		return
	}

	if req.Name == "" || req.MacAddress == "" {
		_ = c.Error(apperror.BadRequest("validation_error", "name and mac_address are required"))
		return
	}

	hostInfo := hostentities.HostInfo{
		Name:                 req.Name,
		MacAddress:           req.MacAddress,
		IPv4:                 req.IPv4,
		OS:                   req.OS,
		Platform:             req.Platform,
		PlatformFamily:       req.PlatformFamily,
		PlatformVersion:      req.PlatformVersion,
		KernelVersion:        req.KernelVersion,
		VirtualizationSystem: req.VirtualizationSystem,
		VirtualizationRole:   req.VirtualizationRole,
		HostID: req.HostID,
	}

	hostID, nodeToken, err := h.nodeService.Join(c.Request.Context(), token, hostInfo)
	if err != nil {
		if strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "expired") {
			_ = c.Error(apperror.BadRequest("join_failed", err.Error()))
		} else {
			_ = c.Error(apperror.Internal("join_failed", err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"host_id":           hostID,
			"node_access_token": nodeToken,
		},
	})
}

// CreateInvite creates a node join token and returns the link (admin only).
//
// @Summary     Create node invite
// @Description Creates a one-time join link for node registration. Admin only.
// @Tags        nodes
// @Produce     json
// @Success     200  {object} map[string]interface{}
// @Failure     401  {object} map[string]string
// @Failure     403  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Security    BearerAuth
// @Router      /nodes/invite [post]
func (h *NodesHandler) CreateInvite(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		_ = c.Error(apperror.Unauthorized("unauthorized", "Authentication required"))
		return
	}
	adminID := userID.(uint)

	baseURL := h.resolvePublicBaseURL(c)

	link, err := h.nodeService.CreateNodeInvite(c.Request.Context(), adminID, baseURL)
	if err != nil {
		_ = c.Error(apperror.Internal("internal_error", err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"link":       link,
			"expires_at": nil, // 24h from creation
		},
	})
}

// PushRequest represents the minimal metrics sent by an agent.
type PushRequest struct {
	Status             string  `json:"status"`
	UptimeSeconds      int64   `json:"uptime_seconds"`
	CPUUsagePercent    float64 `json:"cpu_usage_percent"`
	MemoryUsagePercent float64 `json:"memory_usage_percent"`
}

// Push handles metrics push from agent nodes.
//
// @Summary     Push metrics
// @Description Receives heartbeat/metrics from agent nodes. Auth via node_access_token.
// @Tags        nodes
// @Accept      json
// @Produce     json
// @Param       body  body  PushRequest  true  "Metrics payload"
// @Success     204  "No Content"
// @Failure     401  {object} map[string]string
// @Failure     500  {object} map[string]string
// @Security    BearerAuth
// @Router      /nodes/push [post]
func (h *NodesHandler) Push(c *gin.Context) {
	hostID, exists := c.Get("hostID")
	if !exists {
		_ = c.Error(apperror.Unauthorized("unauthorized", "Host ID not set"))
		return
	}

	var req PushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperror.WithDetail(apperror.BadRequest("validation_error", "Invalid request data"), err.Error()))
		return
	}

	if err := h.nodeService.HandlePush(c.Request.Context(), hostID.(uint)); err != nil {
		_ = c.Error(apperror.Internal("internal_error", err.Error()))
		return
	}

	c.Status(http.StatusNoContent)
}

// ConnectRequest represents the connect request body.
type ConnectRequest struct {
	JoinLink string `json:"join_link" binding:"required"`
}

// Connect handles "connect this node" from agent's admin UI.
// Parses join link, POSTs to main's join endpoint, persists config.
//
// @Summary     Connect to cluster
// @Description Connects this node to the main server using a join link. Persists MAIN_NODE_URL and NODE_ACCESS_TOKEN. Restart required.
// @Tags        nodes
// @Accept      json
// @Produce     json
// @Param       body  body  ConnectRequest  true  "Join link from main node"
// @Success     200   {object} map[string]interface{}
// @Failure     400   {object} map[string]string
// @Failure     401   {object} map[string]string
// @Failure     500   {object} map[string]string
// @Security    BearerAuth
// @Router      /nodes/connect [post]
func (h *NodesHandler) Connect(c *gin.Context) {
	var req ConnectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperror.WithDetail(apperror.BadRequest("validation_error", "join_link is required"), err.Error()))
		return
	}

	u, err := url.Parse(strings.TrimSpace(req.JoinLink))
	if err != nil {
		_ = c.Error(apperror.BadRequest("invalid_url", "Invalid join link URL"))
		return
	}

	token := u.Query().Get("token")
	if token == "" {
		_ = c.Error(apperror.BadRequest("token_required", "Join link must contain token parameter"))
		return
	}

	// When agent runs in Docker, localhost in join link points to container, not host.
	// NODE_HOST_ALIAS (e.g. host.docker.internal) replaces localhost for reaching main on host.
	host := u.Host
	if alias := os.Getenv("NODE_HOST_ALIAS"); alias != "" {
		if strings.HasPrefix(host, "localhost:") || strings.HasPrefix(host, "127.0.0.1:") {
			port := ""
			if idx := strings.LastIndex(host, ":"); idx >= 0 {
				port = host[idx:]
			}
			host = alias + port
		}
	}
	baseURL := u.Scheme + "://" + host

	hostInfo, err := h.hostService.GetCurrentHostInfo(c.Request.Context())
	if err != nil {
		_ = c.Error(apperror.Internal("internal_error", "Failed to get host info: "+err.Error()))
		return
	}

	joinBody := map[string]interface{}{
		"name":                  hostInfo.Name,
		"mac_address":           hostInfo.MacAddress,
		"ipv4":                  hostInfo.IPv4,
		"os":                    hostInfo.OS,
		"platform":              hostInfo.Platform,
		"platform_family":       hostInfo.PlatformFamily,
		"platform_version":      hostInfo.PlatformVersion,
		"kernel_version":        hostInfo.KernelVersion,
		"virtualization_system": hostInfo.VirtualizationSystem,
		"virtualization_role":   hostInfo.VirtualizationRole,
		"host_id":               hostInfo.HostID,
	}
	bodyBytes, _ := json.Marshal(joinBody)

	joinURL := baseURL + "/api/v1/nodes/join?token=" + url.QueryEscape(token)
	req2, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, joinURL, bytes.NewReader(bodyBytes))
	if err != nil {
		_ = c.Error(apperror.Internal("internal_error", err.Error()))
		return
	}
	req2.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req2)
	if err != nil {
		_ = c.Error(apperror.BadRequest("join_failed", "Failed to connect to main node: "+err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		errMsg := "Join failed"
		if e, ok := errBody["error"].(string); ok {
			errMsg = e
		}
		_ = c.Error(apperror.BadRequest("join_failed", fmt.Sprintf("Main node returned %d: %s", resp.StatusCode, errMsg)))
		return
	}

	var joinResp struct {
		Data struct {
			HostID          uint   `json:"host_id"`
			NodeAccessToken string `json:"node_access_token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&joinResp); err != nil {
		_ = c.Error(apperror.Internal("internal_error", "Invalid response from main node"))
		return
	}

	if err := cluster_config.Update(baseURL, joinResp.Data.NodeAccessToken); err != nil {
		_ = c.Error(apperror.Internal("internal_error", "Failed to save config: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"host_id":  joinResp.Data.HostID,
			"main_url": baseURL,
			"message":  "Connected. Push starts on the next metrics cycle. On main: Admin → Nodes → expand this host for URL / regenerate token if you lose .env.",
		},
	})
}

func parseHostIDParam(c *gin.Context) (uint, bool) {
	idStr := c.Param("id")
	id64, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil || id64 == 0 {
		_ = c.Error(apperror.BadRequest("invalid_host_id", "Invalid host id"))
		return 0, false
	}
	return uint(id64), true
}

// RegenerateAgentToken issues a new push token; response is node_access_token only (admin).
func (h *NodesHandler) RegenerateAgentToken(c *gin.Context) {
	hostID, ok := parseHostIDParam(c)
	if !ok {
		return
	}
	if _, err := h.hostService.GetHostByID(c.Request.Context(), hostID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = c.Error(apperror.NotFound("not_found", "Host not found"))
			return
		}
		_ = c.Error(apperror.Internal("internal_error", err.Error()))
		return
	}

	plain, err := h.nodeService.RegenerateNodeAccessToken(c.Request.Context(), hostID)
	if err != nil {
		_ = c.Error(apperror.Internal("internal_error", err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"node_access_token": plain,
		},
	})
}

// GetClusterUIStatus returns push URL and whether to show the "Connect this node" block (admin).
func (h *NodesHandler) GetClusterUIStatus(c *gin.Context) {
	host, err := h.hostService.GetCurrentHost(c.Request.Context())
	if err != nil {
		_ = c.Error(apperror.Internal("internal_error", err.Error()))
		return
	}
	base := h.resolvePublicBaseURL(c)
	status, err := h.nodeService.GetClusterUIStatus(c.Request.Context(), host.ID, base)
	if err != nil {
		_ = c.Error(apperror.Internal("internal_error", err.Error()))
		return
	}
	out := gin.H{
		"show_connect_block": status.ShowConnectBlock,
		"push_url":           status.PushURL,
		"is_agent":           status.IsAgent,
		"has_remote_agents":  status.HasRemoteAgents,
	}
	if status.IsAgent {
		out["main_node_url"] = status.AgentMainNodeURL
		out["node_access_token"] = status.AgentNodeAccessToken
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// AgentClusterConfigBody is the JSON body for updating agent cluster connection (admin).
type AgentClusterConfigBody struct {
	MainNodeURL      string `json:"main_node_url" binding:"required"`
	NodeAccessToken  string `json:"node_access_token" binding:"required"`
}

// DeleteAgentClusterConfig clears MAIN_NODE_URL and NODE_ACCESS_TOKEN on this agent (admin).
func (h *NodesHandler) DeleteAgentClusterConfig(c *gin.Context) {
	if err := h.nodeService.ClearAgentClusterConfig(); err != nil {
		_ = c.Error(apperror.Internal("internal_error", err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}

// UpdateAgentClusterConfig updates MAIN_NODE_URL and NODE_ACCESS_TOKEN on this agent (admin).
func (h *NodesHandler) UpdateAgentClusterConfig(c *gin.Context) {
	var body AgentClusterConfigBody
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = c.Error(apperror.BadRequest("validation_error", err.Error()))
		return
	}
	if err := h.nodeService.UpdateAgentClusterConfig(body.MainNodeURL, body.NodeAccessToken); err != nil {
		_ = c.Error(apperror.BadRequest("invalid_config", err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteRemoteHost removes a remote host and related data (admin).
func (h *NodesHandler) DeleteRemoteHost(c *gin.Context) {
	hostID, ok := parseHostIDParam(c)
	if !ok {
		return
	}
	current, err := h.hostService.GetCurrentHost(c.Request.Context())
	if err != nil {
		_ = c.Error(apperror.Internal("internal_error", err.Error()))
		return
	}
	err = h.nodeService.DeleteRemoteHost(c.Request.Context(), hostID, current.ID)
	if err != nil {
		if errors.Is(err, nodeservice.ErrCannotDeleteLocalHost) {
			_ = c.Error(apperror.Forbidden("forbidden", err.Error()))
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = c.Error(apperror.NotFound("not_found", "Host not found"))
			return
		}
		_ = c.Error(apperror.Internal("internal_error", err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}
