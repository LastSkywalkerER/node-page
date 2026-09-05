package appbackup

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

// Handler exposes the application backup / update / restore endpoints.
type Handler struct {
	logger  *log.Logger
	service Service
}

// NewHandler creates the application-backup HTTP handler.
func NewHandler(logger *log.Logger, service Service) *Handler {
	return &Handler{logger: logger, service: service}
}

// status maps a service error onto an HTTP code. A blocked action is a 409:
// the request was well formed, the node just cannot honour it right now, and
// the body says why.
func status(err error) int {
	switch {
	case errors.Is(err, ErrNoRepo), errors.Is(err, ErrNoController),
		errors.Is(err, ErrRemoteHost), errors.Is(err, ErrSelfProject), errors.Is(err, ErrBusy),
		errors.Is(err, ErrRepoReadOnly):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func hostIDParam(c *gin.Context) uint {
	v, _ := strconv.ParseUint(c.Query("host_id"), 10, 64)
	return uint(v)
}

// HandleStatus reports whether backups can run here, and why not.
//
// @Summary     Application backup status
// @Description Repository configuration, controller availability and restic state. Admin only.
// @Tags        appbackup
// @Produce     json
// @Success     200 {object} Status
// @Security    BearerAuth
// @Router      /app-backup/status [get]
func (h *Handler) HandleStatus(c *gin.Context) {
	st, err := h.service.Status(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, st)
}

// HandleSaveRepo persists the cluster-wide backup repository.
//
// @Summary     Configure the backup repository
// @Description Validates the credentials, installs restic, then stores the repository (secrets encrypted, replicated). Admin only.
// @Tags        appbackup
// @Accept      json
// @Produce     json
// @Success     200 {object} Status
// @Security    BearerAuth
// @Router      /app-backup/repo [put]
func (h *Handler) HandleSaveRepo(c *gin.Context) {
	var req RepoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	st, err := h.service.SaveRepo(c.Request.Context(), req)
	if err != nil {
		h.logger.Error("appbackup: save repository failed", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, st)
}

// HandleTestRepo validates repository credentials without persisting.
//
// @Summary     Test the backup repository
// @Tags        appbackup
// @Accept      json
// @Produce     json
// @Success     200 {object} map[string]interface{}
// @Security    BearerAuth
// @Router      /app-backup/repo/test [post]
func (h *Handler) HandleTestRepo(c *gin.Context) {
	var req RepoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.TestRepo(c.Request.Context(), req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// HandleDeleteRepo removes the repository configuration (not its data).
//
// @Summary     Remove the backup repository configuration
// @Tags        appbackup
// @Produce     json
// @Success     204
// @Security    BearerAuth
// @Router      /app-backup/repo [delete]
func (h *Handler) HandleDeleteRepo(c *gin.Context) {
	if err := h.service.DeleteRepo(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// HandleRepoStats reports the repository's total size and snapshot count.
//
// @Summary     Backup repository statistics
// @Description Total on-disk size, uncompressed size and snapshot count of the shared repository. Admin only.
// @Tags        appbackup
// @Produce     json
// @Success     200 {object} RepoStats
// @Security    BearerAuth
// @Router      /app-backup/repo/stats [get]
func (h *Handler) HandleRepoStats(c *gin.Context) {
	st, err := h.service.RepoStats(c.Request.Context())
	if err != nil {
		c.JSON(status(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, st)
}

// HandlePlan returns what would be backed up for one application.
//
// @Summary     Plan an application backup
// @Description Compose files, data locations and per-service versions, plus why actions are blocked when they are. Admin only.
// @Tags        appbackup
// @Produce     json
// @Param       project path string true "Compose project"
// @Success     200 {object} Plan
// @Security    BearerAuth
// @Router      /app-backup/{project}/plan [get]
func (h *Handler) HandlePlan(c *gin.Context) {
	plan, err := h.service.Plan(c.Request.Context(), hostIDParam(c), c.Param("project"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, plan)
}

// HandleVersions lists the tags an image's registry publishes.
//
// @Summary     List available image versions
// @Tags        appbackup
// @Produce     json
// @Param       image query string true "Image reference"
// @Success     200 {object} map[string]interface{}
// @Security    BearerAuth
// @Router      /app-backup/versions [get]
func (h *Handler) HandleVersions(c *gin.Context) {
	tags, err := h.service.Versions(c.Request.Context(), c.Query("image"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tags": tags})
}

// HandleSnapshots lists an application's snapshots.
//
// @Summary     List application snapshots
// @Tags        appbackup
// @Produce     json
// @Param       project path string true "Compose project"
// @Success     200 {object} map[string]interface{}
// @Security    BearerAuth
// @Router      /app-backup/{project}/snapshots [get]
func (h *Handler) HandleSnapshots(c *gin.Context) {
	snaps, err := h.service.Snapshots(c.Request.Context(), c.Param("project"))
	if err != nil {
		c.JSON(status(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"snapshots": snaps})
}

// HandleDeleteSnapshot forgets one snapshot and prunes its space.
//
// @Summary     Delete a snapshot
// @Tags        appbackup
// @Produce     json
// @Param       snapshot_id path string true "Snapshot id"
// @Success     204
// @Security    BearerAuth
// @Router      /app-backup/snapshots/{snapshot_id} [delete]
func (h *Handler) HandleDeleteSnapshot(c *gin.Context) {
	if err := h.service.DeleteSnapshot(c.Request.Context(), c.Param("snapshot_id")); err != nil {
		c.JSON(status(err), gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// HandleBackup queues a snapshot of an application.
//
// @Summary     Back up an application
// @Description Stops the project, snapshots its compose files and data, starts it again. Admin only.
// @Tags        appbackup
// @Produce     json
// @Param       project path string true "Compose project"
// @Success     202 {object} RunEntity
// @Security    BearerAuth
// @Router      /app-backup/{project}/backup [post]
func (h *Handler) HandleBackup(c *gin.Context) {
	run, err := h.service.Backup(c.Request.Context(), hostIDParam(c), c.Param("project"))
	if err != nil {
		c.JSON(status(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, run)
}

// updateRequest is the per-service version move the operator picked.
type updateRequest struct {
	Targets []ServiceTarget `json:"targets"`
}

// HandleUpdate queues a backup + image move.
//
// @Summary     Update an application's images
// @Description Snapshots, rewrites the compose file's image references (keeping a .bak), pulls and restarts. Admin only.
// @Tags        appbackup
// @Accept      json
// @Produce     json
// @Param       project path string true "Compose project"
// @Success     202 {object} RunEntity
// @Security    BearerAuth
// @Router      /app-backup/{project}/update [post]
func (h *Handler) HandleUpdate(c *gin.Context) {
	var req updateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	run, err := h.service.Update(c.Request.Context(), hostIDParam(c), c.Param("project"), req.Targets)
	if err != nil {
		c.JSON(status(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, run)
}

// restoreRequest names the snapshot to roll back to.
type restoreRequest struct {
	SnapshotID string `json:"snapshot_id"`
}

// HandleRestore queues a destructive restore.
//
// @Summary     Restore an application from a snapshot
// @Description Takes the project down, ERASES its current data and writes the snapshot back, compose file included. Admin only.
// @Tags        appbackup
// @Accept      json
// @Produce     json
// @Param       project path string true "Compose project"
// @Success     202 {object} RunEntity
// @Security    BearerAuth
// @Router      /app-backup/{project}/restore [post]
func (h *Handler) HandleRestore(c *gin.Context) {
	var req restoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	run, err := h.service.Restore(c.Request.Context(), hostIDParam(c), c.Param("project"), req.SnapshotID)
	if err != nil {
		c.JSON(status(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, run)
}

// HandleRuns returns the local run history for one application.
//
// @Summary     Application backup run history
// @Tags        appbackup
// @Produce     json
// @Param       project path string true "Compose project"
// @Success     200 {object} map[string]interface{}
// @Security    BearerAuth
// @Router      /app-backup/{project}/runs [get]
func (h *Handler) HandleRuns(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	runs, err := h.service.Runs(c.Request.Context(), hostIDParam(c), c.Param("project"), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": runs})
}
