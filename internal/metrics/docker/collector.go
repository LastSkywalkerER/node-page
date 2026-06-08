// Package docker provides metric collection implementations for Docker containers.
// This package contains collectors for Docker container statistics and other
// external data sources used by the monitoring application.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"golang.org/x/sync/errgroup"
)

// dockerMetricsCollector implements the DockerMetricsCollector interface.
// This collector gathers Docker container statistics and status information
// using the Docker API client with caching for availability checks and CPU calculations.
type dockerMetricsCollector struct {
	logger            *log.Logger
	client            *client.Client
	dockerAvailable   bool
	lastCheck         time.Time
	checkInterval     time.Duration
	containerCPUCache map[string]cpuStatsCache
	cacheMutex        sync.RWMutex

	// Container disk sizes are expensive to compute (the daemon walks layer
	// dirs), so we refresh them only every sizeRefreshInterval and serve cached
	// values on the fast (every-tick) collection cycles.
	containerSizeCache map[string]containerSize
	lastSizeAt         time.Time

	// Image update checks hit the registry (network), so they run rarely
	// (updateCheckInterval) in a background goroutine; results are cached per
	// image reference and applied to containers on every cycle.
	imageUpdateCache  map[string]imageUpdateInfo
	lastUpdateCheckAt time.Time
	updateChecking    bool

	// registryClient is used to resolve remote image versions (config blobs).
	registryClient *http.Client
}

// imageUpdateInfo caches the registry update-check result for one image ref.
type imageUpdateInfo struct {
	available     bool   // registry has a newer digest than the running image
	checked       bool   // a check completed (registry reachable)
	localDigest   string // digest of the running image (sha256:...)
	remoteDigest  string // digest the registry tag currently points to
	version       string // running image's human-readable version
	remoteVersion string // version of the image the registry tag now points to
}

// updateCheckInterval bounds how often the registry is queried for newer images.
const updateCheckInterval = time.Hour

// containerSize caches a container's writable-layer and total filesystem sizes.
type containerSize struct {
	rw     int64
	rootFs int64
}

// sizeRefreshInterval bounds how often the (slow) Size:true container list runs.
const sizeRefreshInterval = 60 * time.Second

// cpuStatsCache stores CPU statistics for percentage calculation.
// This structure holds previous CPU usage data needed to compute CPU percentages.
type cpuStatsCache struct {
	// totalUsage stores the total CPU usage from previous measurement
	totalUsage uint64

	// systemUsage stores the system CPU usage from previous measurement
	systemUsage uint64

	// timestamp indicates when the previous measurement was taken
	timestamp time.Time
}

// NewDockerCollector creates a new Docker metrics collector.
func NewDockerCollector(logger *log.Logger) DockerMetricsCollector {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cli := tryOpenDockerClient(ctx, logger)
	return &dockerMetricsCollector{
		logger:             logger,
		client:             cli,
		checkInterval:      5 * time.Second,
		containerCPUCache:  make(map[string]cpuStatsCache),
		containerSizeCache: make(map[string]containerSize),
		imageUpdateCache:   make(map[string]imageUpdateInfo),
		registryClient:     &http.Client{},
	}
}

// IsDockerAvailable checks if the Docker daemon is accessible and running.
// This method caches the result for 5 seconds to avoid excessive API calls.
func (c *dockerMetricsCollector) IsDockerAvailable(ctx context.Context) bool {
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()

	now := time.Now()

	// If less time has passed since the last check, return cached result
	if now.Sub(c.lastCheck) < c.checkInterval && c.lastCheck.After(time.Time{}) {
		return c.dockerAvailable
	}

	c.lastCheck = now

	if c.client != nil {
		if _, err := c.client.Ping(ctx); err == nil {
			c.dockerAvailable = true
			return true
		}
		_ = c.client.Close()
		c.client = nil
	}

	c.client = tryOpenDockerClient(ctx, c.logger)
	c.dockerAvailable = c.client != nil
	return c.dockerAvailable
}

// CollectDockerMetrics gathers Docker container statistics and status information.
// This method retrieves information about all running containers including their
// resource usage, network statistics, and metadata from the Docker API.
func (c *dockerMetricsCollector) CollectDockerMetrics(ctx context.Context) (DockerMetric, error) {
	c.logger.Debug("Collecting Docker container metrics")

	if !c.IsDockerAvailable(ctx) {
		c.logger.Warn("Docker is not available, returning empty metrics")
		return DockerMetric{
			DockerAvailable: false,
			Error:           "Cannot reach Docker daemon. Start Docker or set DOCKER_HOST.",
		}, nil
	}

	if c.client == nil {
		return DockerMetric{
			DockerAvailable: false,
			Error:           "Docker client not initialized",
		}, nil
	}

	// Computing container sizes makes the daemon walk layer dirs, which is slow.
	// Only request sizes on the slow cadence (sizeRefreshInterval); other cycles
	// reuse cached sizes so the 5s CPU/mem/net cycle stays fast and fresh.
	c.cacheMutex.RLock()
	withSize := c.lastSizeAt.IsZero() || time.Since(c.lastSizeAt) >= sizeRefreshInterval
	c.cacheMutex.RUnlock()

	containers, err := c.client.ContainerList(ctx, container.ListOptions{All: true, Size: withSize})
	if err != nil {
		return DockerMetric{
			DockerAvailable: true,
			Error:           fmt.Sprintf("Failed to list containers: %v", err),
		}, nil
	}
	if withSize {
		c.cacheMutex.Lock()
		c.lastSizeAt = time.Now()
		c.cacheMutex.Unlock()
	}

	// Structure for parallel container processing results
	type containerResult struct {
		container DockerContainer
		stackName string
		isRunning bool
		err       error
	}

	results := make(chan containerResult, len(containers))
	var runningCount int32 = 0

	// Function for parallel processing of one container.
	// Bounded by procCtx (which carries a 5s per-container deadline) so a hung Docker
	// inspect cannot wedge the goroutine forever.
	processContainer := func(procCtx context.Context, containerInfo container.Summary) {
		defer func() {
			if r := recover(); r != nil {
				results <- containerResult{err: fmt.Errorf("panic processing container %s: %v", containerInfo.ID, r)}
			}
		}()

		containerJSON, err := c.client.ContainerInspect(procCtx, containerInfo.ID)
		if err != nil {
			results <- containerResult{err: fmt.Errorf("failed to inspect container %s: %v", containerInfo.ID, err)}
			return
		}

		if c.logger.GetLevel() <= log.DebugLevel {
			containerJSONBytes, _ := json.Marshal(containerJSON)
			c.logger.Debug("Container JSON details", "container_id", containerInfo.ID, "json", string(containerJSONBytes))
		}

		// Parse container name (remove "/" prefix)
		var name string
		if len(containerInfo.Names) > 0 {
			name = strings.TrimPrefix(containerInfo.Names[0], "/")
		} else {
			name = "unknown"
		}

		// Extract stack name from compose labels first
		stackName := c.extractStackName(containerJSON.Config.Labels)
		if stackName == "" {
			stackName = ExtractStackNameFromContainerName(name)
		}

		// Resolve the application grouping key + service from labels (used by the
		// Applications view; the per-node Containers stacks above are unchanged).
		labels := containerJSON.Config.Labels
		project, service, _ := resolveProject(labels, name)

		// Convert ports (with nil protection)
		var ports []DockerPort
		if containerInfo.Ports != nil {
			ports = c.convertPorts(containerInfo.Ports)
		} else {
			ports = make([]DockerPort, 0)
		}

		// Get CPU limit from container configuration
		cpuLimit := c.getCPULimit(containerJSON)

		// Get real statistics for running containers
		var containerStats DockerStats
		isRunning := containerInfo.State == "running"

		if isRunning {
			// Safely get statistics with panic protection
			containerStats = c.getContainerResourceStats(procCtx, c.client, containerInfo.ID, cpuLimit)
		} else {
			// For stopped containers, use empty statistics but keep CPU limit
			containerStats = DockerStats{
				CPULimit: cpuLimit,
			}
		}

		// Safely get container ID
		containerID := containerInfo.ID
		if len(containerID) > 12 {
			containerID = containerID[:12]
		}

		finishedAt := c.parseContainerFinishedTime(containerJSON.State.FinishedAt)
		c.logger.Debug("Container finished time", "container_id", containerID, "finished_at_raw", containerJSON.State.FinishedAt, "finished_at_parsed", finishedAt)

		// Disk sizes: fresh from the daemon on slow cycles, cached otherwise.
		szRw, szRootFs := c.resolveContainerSize(containerInfo.ID, withSize, containerInfo.SizeRw, containerInfo.SizeRootFs)

		// Image update status from the (rarely-refreshed) registry check cache.
		upd := c.lookupImageUpdate(containerInfo.Image)

		dockerContainer := DockerContainer{
			ID:                 containerID,
			Name:               name,
			Image:              containerInfo.Image,
			State:              containerInfo.State,
			Status:             containerInfo.Status,
			Ports:              ports,
			Stats:              containerStats,
			Created:            c.parseContainerCreatedTime(containerJSON.Created),
			FinishedAt:         finishedAt,
			Project:            project,
			Service:            service,
			Labels:             labels,
			ComposeConfigFiles: labels["com.docker.compose.project.config_files"],
			ComposeWorkingDir:  labels["com.docker.compose.project.working_dir"],
			SizeRw:             szRw,
			SizeRootFs:         szRootFs,
			Mounts:             c.convertMounts(containerJSON.Mounts),
			ImageID:            containerJSON.Image,
			UpdateAvailable:    upd.available,
			UpdateChecked:      upd.checked,
			LocalDigest:        upd.localDigest,
			RemoteDigest:       upd.remoteDigest,
			ImageVersion:       upd.version,
			RemoteVersion:      upd.remoteVersion,
		}

		results <- containerResult{
			container: dockerContainer,
			stackName: stackName,
			isRunning: isRunning,
		}
	}

	// Start parallel processing of all containers with bounded parallelism and per-container deadline.
	// Closing results from a single goroutine after all workers complete prevents the collection
	// loop from hanging if a worker panics before sending.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, containerInfo := range containers {
		ci := containerInfo
		g.Go(func() error {
			pctx, cancel := context.WithTimeout(gctx, 5*time.Second)
			defer cancel()
			processContainer(pctx, ci)
			return nil
		})
	}
	go func() {
		_ = g.Wait()
		close(results)
	}()

	// Collect results and group by stacks
	stackMap := make(map[string]*DockerStack)
	for result := range results {
		if result.err != nil {
			c.logger.Warn("Failed to process container", "error", result.err)
			continue
		}
		if result.isRunning {
			atomic.AddInt32(&runningCount, 1)
		}

		// Get or create stack
		stack, exists := stackMap[result.stackName]
		if !exists {
			stack = &DockerStack{
				Name:       result.stackName,
				Containers: make([]DockerContainer, 0),
			}
			stackMap[result.stackName] = stack
		}

		// Add container to stack
		stack.Containers = append(stack.Containers, result.container)
		stack.TotalContainers++

		if result.isRunning {
			stack.RunningContainers++
		}
	}

	// Merge stacks with common prefixes (e.g., myapp-web, myapp-api -> myapp)
	mergedStackMap := c.mergeStacksByCommonPrefix(stackMap)

	// Convert map to slice
	dockerStacks := make([]DockerStack, 0, len(mergedStackMap))
	for _, stack := range mergedStackMap {
		dockerStacks = append(dockerStacks, *stack)
	}
	for i := range dockerStacks {
		sort.Slice(dockerStacks[i].Containers, func(a, b int) bool {
			return dockerStacks[i].Containers[a].Name < dockerStacks[i].Containers[b].Name
		})
	}
	sort.Slice(dockerStacks, func(i, j int) bool {
		return dockerStacks[i].Name < dockerStacks[j].Name
	})

	c.logger.Debug("Docker metrics collected successfully", "total_containers", len(containers), "running_containers", int(runningCount), "stacks", len(dockerStacks))

	// Log full content of all stacks and containers
	for i, stack := range dockerStacks {
		c.logger.Debug("Docker stack details", "stack_index", i, "stack_name", stack.Name, "containers_count", len(stack.Containers), "running_containers", stack.RunningContainers)
		for j, ctr := range stack.Containers {
			c.logger.Debug("Docker container details",
				"stack_name", stack.Name,
				"container_index", j,
				"id", ctr.ID,
				"name", ctr.Name,
				"image", ctr.Image,
				"state", ctr.State,
				"status", ctr.Status,
				"ports_count", len(ctr.Ports),
				"cpu_percent", ctr.Stats.CPUPercent,
				"cpu_limit", ctr.Stats.CPULimit,
				"cpu_percent_of_limit", ctr.Stats.CPUPercentOfLimit,
				"memory_usage", ctr.Stats.MemoryUsage,
				"memory_limit", ctr.Stats.MemoryLimit,
				"memory_percent", ctr.Stats.MemoryPercent,
				"network_rx", ctr.Stats.NetworkRx,
				"network_tx", ctr.Stats.NetworkTx,
				"block_read", ctr.Stats.BlockRead,
				"block_write", ctr.Stats.BlockWrite,
				"created", ctr.Created)

			// Log ports separately if they exist
			for k, port := range ctr.Ports {
				c.logger.Debug("Docker container port",
					"stack_name", stack.Name,
					"container_name", ctr.Name,
					"port_index", k,
					"private_port", port.PrivatePort,
					"public_port", port.PublicPort,
					"type", port.Type,
					"ip", port.IP)
			}
		}
	}

	// Evict CPU cache entries for containers that no longer exist (prevents unbounded growth).
	c.cacheMutex.Lock()
	currentIDs := make(map[string]struct{}, len(containers))
	for _, ci := range containers {
		currentIDs[ci.ID] = struct{}{}
	}
	for id := range c.containerCPUCache {
		if _, ok := currentIDs[id]; !ok {
			delete(c.containerCPUCache, id)
		}
	}
	for id := range c.containerSizeCache {
		if _, ok := currentIDs[id]; !ok {
			delete(c.containerSizeCache, id)
		}
	}
	c.cacheMutex.Unlock()

	// Kick off a background registry update check on the slow cadence.
	c.maybeRefreshImageUpdates(containers)

	return DockerMetric{
		Stacks:            dockerStacks,
		TotalContainers:   len(containers),
		RunningContainers: int(runningCount),
		DockerAvailable:   true,
	}, nil
}

// getCPULimit extracts CPU limit from container configuration
func (c *dockerMetricsCollector) getCPULimit(containerJSON container.InspectResponse) float64 {
	hostConfig := containerJSON.HostConfig

	// Check NanoCPUs (for --cpus flag)
	if hostConfig.NanoCPUs > 0 {
		return float64(hostConfig.NanoCPUs) / 1000000000.0 // Convert from nanocpus to CPU cores
	}

	// Check CPU quota (for --cpu-quota and --cpu-period)
	if hostConfig.CPUQuota > 0 && hostConfig.CPUPeriod > 0 {
		return float64(hostConfig.CPUQuota) / float64(hostConfig.CPUPeriod)
	}

	// If limits are not set, return system core count or default value
	// For simplicity, return 0, which will mean "unlimited"
	return 0.0
}

// convertPorts converts Docker ports to our structure, de-duplicating entries
// that differ only by bind IP (Docker reports IPv4 and IPv6 bindings separately).
func (c *dockerMetricsCollector) convertPorts(dockerPorts []container.Port) []DockerPort {
	ports := make([]DockerPort, 0, len(dockerPorts))
	seen := make(map[string]struct{}, len(dockerPorts))
	for _, port := range dockerPorts {
		dockerPort := DockerPort{
			PrivatePort: int(port.PrivatePort),
			Type:        port.Type,
		}
		if port.PublicPort > 0 {
			dockerPort.PublicPort = int(port.PublicPort)
			dockerPort.IP = port.IP
		}
		key := fmt.Sprintf("%d/%d/%s", dockerPort.PrivatePort, dockerPort.PublicPort, dockerPort.Type)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ports = append(ports, dockerPort)
	}
	return ports
}

// lookupImageUpdate returns the cached registry-update result for an image ref.
func (c *dockerMetricsCollector) lookupImageUpdate(ref string) imageUpdateInfo {
	c.cacheMutex.RLock()
	defer c.cacheMutex.RUnlock()
	return c.imageUpdateCache[ref]
}

// maybeRefreshImageUpdates launches a background registry update check when the
// interval has elapsed and one is not already running. Never blocks collection.
func (c *dockerMetricsCollector) maybeRefreshImageUpdates(containers []container.Summary) {
	c.cacheMutex.Lock()
	due := c.lastUpdateCheckAt.IsZero() || time.Since(c.lastUpdateCheckAt) >= updateCheckInterval
	if !due || c.updateChecking || c.client == nil {
		c.cacheMutex.Unlock()
		return
	}
	c.updateChecking = true
	c.lastUpdateCheckAt = time.Now()
	c.cacheMutex.Unlock()

	// Distinct image refs of ALL containers (running or stopped); skip bare IDs
	// / untagged refs that have no registry reference.
	seen := map[string]struct{}{}
	refs := make([]string, 0)
	for _, ci := range containers {
		ref := ci.Image
		if ref == "" || strings.HasPrefix(ref, "sha256:") || strings.Contains(ref, "<none>") {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}

	go c.refreshImageUpdates(refs)
}

// refreshImageUpdates queries the registry for each image ref and caches whether
// a newer image is available. Bounded concurrency + per-image timeout.
func (c *dockerMetricsCollector) refreshImageUpdates(refs []string) {
	defer func() {
		c.cacheMutex.Lock()
		c.updateChecking = false
		c.cacheMutex.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4)
	for _, ref := range refs {
		ref := ref
		g.Go(func() error {
			cctx, ccancel := context.WithTimeout(gctx, 25*time.Second)
			defer ccancel()
			info := c.checkImageUpdate(cctx, ref)
			c.cacheMutex.Lock()
			c.imageUpdateCache[ref] = info
			c.cacheMutex.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	c.logger.Debug("Image update check completed", "images", len(refs))
}

// checkImageUpdate compares the registry's current digest for ref with the local
// image's repo digest, and resolves the running image's version label. checked
// is false when the registry is unreachable (e.g. private/local-only images).
func (c *dockerMetricsCollector) checkImageUpdate(ctx context.Context, ref string) imageUpdateInfo {
	if c.client == nil {
		return imageUpdateInfo{}
	}
	dist, err := c.client.DistributionInspect(ctx, ref, "")
	if err != nil {
		return imageUpdateInfo{}
	}
	remote := dist.Descriptor.Digest.String()
	if remote == "" {
		return imageUpdateInfo{}
	}
	img, err := c.client.ImageInspect(ctx, ref)
	if err != nil || len(img.RepoDigests) == 0 {
		return imageUpdateInfo{}
	}

	// Pick the local repo digest matching ref's repository (strip the tag).
	repo := ref
	if at := strings.LastIndex(ref, "/"); at >= 0 {
		if colon := strings.LastIndex(ref[at:], ":"); colon >= 0 {
			repo = ref[:at+colon]
		}
	} else if colon := strings.LastIndex(ref, ":"); colon >= 0 {
		repo = ref[:colon]
	}
	local := ""
	for _, rd := range img.RepoDigests {
		i := strings.LastIndex(rd, "@")
		if i < 0 {
			continue
		}
		if local == "" {
			local = rd[i+1:] // fallback
		}
		if strings.HasPrefix(rd, repo+"@") {
			local = rd[i+1:]
			break
		}
	}

	var labels map[string]string
	var env []string
	if img.Config != nil {
		labels = img.Config.Labels
		env = img.Config.Env
	}
	version := resolveImageVersion(labels, env, ref)

	available := local != remote
	// Resolve what version the registry tag points to now (only when it differs,
	// to avoid extra registry traffic). Best-effort.
	remoteVer := ""
	if available {
		remoteVer = remoteImageVersion(ctx, c.registryClient, ref)
	}

	return imageUpdateInfo{
		available:     available,
		checked:       true,
		localDigest:   local,
		remoteDigest:  remote,
		version:       version,
		remoteVersion: remoteVer,
	}
}

// resolveImageVersion derives a human-readable version for an image: the OCI
// version label, else a `<NAME>_VERSION` env var matching the image name (e.g.
// REDIS_VERSION), else the single `*_VERSION` env, else the image tag.
func resolveImageVersion(labels map[string]string, env []string, ref string) string {
	if labels != nil {
		if v := strings.TrimSpace(labels["org.opencontainers.image.version"]); v != "" {
			return v
		}
	}

	base := strings.ToUpper(lastImageSegment(ref)) // "REDIS" from "redis:7-alpine"
	var nameMatch, only string
	count := 0
	for _, e := range env {
		eq := strings.IndexByte(e, '=')
		if eq <= 0 {
			continue
		}
		k := strings.ToUpper(e[:eq])
		val := strings.TrimSpace(e[eq+1:])
		if val == "" || !strings.HasSuffix(k, "_VERSION") {
			continue
		}
		count++
		only = val
		stem := strings.TrimSuffix(k, "_VERSION")
		if base != "" && (strings.Contains(k, base) || strings.Contains(base, stem)) {
			nameMatch = val
		}
	}
	if nameMatch != "" {
		return nameMatch
	}
	if count == 1 && only != "" {
		return only
	}
	return imageTag(ref)
}

// imageTag returns the tag portion of an image reference, or "latest".
func imageTag(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		return ref[colon+1:]
	}
	return "latest"
}

// resolveContainerSize returns the container's writable/total sizes: when
// withSize is set it stores the freshly-reported values in the cache; otherwise
// it serves the last cached values (so fast cycles don't recompute sizes).
func (c *dockerMetricsCollector) resolveContainerSize(id string, withSize bool, rw, rootFs int64) (int64, int64) {
	if withSize {
		c.cacheMutex.Lock()
		c.containerSizeCache[id] = containerSize{rw: rw, rootFs: rootFs}
		c.cacheMutex.Unlock()
		return rw, rootFs
	}
	c.cacheMutex.RLock()
	e := c.containerSizeCache[id]
	c.cacheMutex.RUnlock()
	return e.rw, e.rootFs
}

// convertMounts maps Docker mount points to our DockerMount structure.
func (c *dockerMetricsCollector) convertMounts(mounts []container.MountPoint) []DockerMount {
	out := make([]DockerMount, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, DockerMount{
			Type:        string(m.Type),
			Name:        m.Name,
			Source:      m.Source,
			Destination: m.Destination,
			RW:          m.RW,
		})
	}
	return out
}

// getContainerResourceStats gets container resource usage statistics
func (c *dockerMetricsCollector) getContainerResourceStats(ctx context.Context, cli *client.Client, containerID string, cpuLimit float64) DockerStats {

	stats, err := cli.ContainerStats(ctx, containerID, false)
	if err != nil {
		return DockerStats{} // Return empty statistics on error
	}
	defer stats.Body.Close()

	var containerStats container.StatsResponse
	decoder := json.NewDecoder(stats.Body)
	if err := decoder.Decode(&containerStats); err != nil {
		return DockerStats{} // Return empty statistics on error
	}

	// Safely get memory statistics
	memoryUsage := containerStats.MemoryStats.Usage
	memoryLimit := containerStats.MemoryStats.Limit
	memoryPercent := float64(0)
	if memoryLimit > 0 {
		memoryPercent = float64(memoryUsage) / float64(memoryLimit) * 100.0
	}

	// Safely get network statistics
	var networkRx, networkTx uint64
	if containerStats.Networks != nil {
		for _, network := range containerStats.Networks {
			networkRx += network.RxBytes
			networkTx += network.TxBytes
		}
	}
	c.logger.Debug("Network stats for container", "container_id", containerID, "network_rx_bytes", networkRx, "network_tx_bytes", networkTx)

	// Safely get disk statistics
	var blockRead, blockWrite uint64
	if containerStats.BlkioStats.IoServiceBytesRecursive != nil {
		for _, ioStat := range containerStats.BlkioStats.IoServiceBytesRecursive {
			if ioStat.Op == "read" {
				blockRead += ioStat.Value
			} else if ioStat.Op == "write" {
				blockWrite += ioStat.Value
			}
		}
	}

	// Panic protection for CPU calculation
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("panic in CPU calculation", "container", containerID, "error", r)
		}
	}()
	cpuPercent := c.calculateCPUPercentWithCache(containerID, &containerStats)

	// Calculate CPU usage percentage relative to container limit
	cpuPercentOfLimit := c.calculateCPUPercentOfLimit(&containerStats, cpuLimit)

	return DockerStats{
		CPUPercent:        cpuPercent,
		CPULimit:          cpuLimit,
		CPUPercentOfLimit: cpuPercentOfLimit,
		MemoryUsage:       memoryUsage,
		MemoryLimit:       memoryLimit,
		MemoryPercent:     memoryPercent,
		NetworkRx:         networkRx,
		NetworkTx:         networkTx,
		BlockRead:         blockRead,
		BlockWrite:        blockWrite,
	}
}

// calculateCPUPercentWithCache calculates CPU usage percentage using cached previous values (thread-safe)
func (c *dockerMetricsCollector) calculateCPUPercentWithCache(containerID string, stats *container.StatsResponse) float64 {
	currentTotal := stats.CPUStats.CPUUsage.TotalUsage
	currentSystem := stats.CPUStats.SystemUsage
	currentTime := time.Now()

	// Use read lock for reading from cache
	c.cacheMutex.RLock()
	prev, exists := c.containerCPUCache[containerID]
	c.cacheMutex.RUnlock()

	if !exists {
		// First request for this container - save current values and return 0
		c.cacheMutex.Lock()
		c.containerCPUCache[containerID] = cpuStatsCache{
			totalUsage:  currentTotal,
			systemUsage: currentSystem,
			timestamp:   currentTime,
		}
		c.cacheMutex.Unlock()
		return 0.0
	}

	// Calculate time difference
	timeDelta := currentTime.Sub(prev.timestamp).Seconds()
	if timeDelta <= 0 {
		return 0.0
	}

	// Calculate CPU usage difference
	cpuDelta := float64(currentTotal) - float64(prev.totalUsage)
	systemDelta := float64(currentSystem) - float64(prev.systemUsage)

	// Update cache with write lock
	c.cacheMutex.Lock()
	c.containerCPUCache[containerID] = cpuStatsCache{
		totalUsage:  currentTotal,
		systemUsage: currentSystem,
		timestamp:   currentTime,
	}
	c.cacheMutex.Unlock()

	// Calculate CPU percentage
	// Docker TotalUsage already accounts for all system cores, so we don't multiply by core count
	if systemDelta > 0 && cpuDelta > 0 {
		return (cpuDelta / systemDelta) * 100.0
	}

	return 0.0
}

// calculateCPUPercentOfLimit calculates CPU usage percentage relative to container limit
// If limit is not set, calculates relative to total system CPU cores
func (c *dockerMetricsCollector) calculateCPUPercentOfLimit(stats *container.StatsResponse, cpuLimit float64) float64 {
	// If limit is not set, use system core count as "allocated to Docker daemon"
	actualLimit := cpuLimit
	if cpuLimit <= 0 {
		actualLimit = float64(runtime.NumCPU())
		c.logger.Debug("CPU limit not set, using system CPU cores as limit", "system_cores", actualLimit)
	}

	c.logger.Debug("Calculating CPU percent of limit", "actual_limit", actualLimit, "pre_total_usage", stats.PreCPUStats.CPUUsage.TotalUsage, "current_total_usage", stats.CPUStats.CPUUsage.TotalUsage)

	// Check if previous data exists
	if stats.PreCPUStats.CPUUsage.TotalUsage == 0 {
		// No previous data, return 0
		c.logger.Debug("No previous CPU stats, returning 0")
		return 0.0
	}

	// Calculate time difference between measurements
	timeDelta := stats.Read.Sub(stats.PreRead).Seconds()

	if timeDelta <= 0 {
		c.logger.Debug("Invalid time delta, returning 0", "time_delta", timeDelta)
		return 0.0
	}

	// Calculate container CPU usage difference
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage) - float64(stats.PreCPUStats.CPUUsage.TotalUsage)

	// Calculate average CPU usage in nanoseconds per second
	cpuUsagePerSecond := cpuDelta / timeDelta

	// Convert to seconds (1 CPU core = 1000000000 nanoseconds per second)
	cpuUsageCores := cpuUsagePerSecond / 1000000000.0

	// Calculate percentage of limit
	result := (cpuUsageCores / actualLimit) * 100.0
	c.logger.Debug("CPU percent of limit calculated", "cpu_usage_cores", cpuUsageCores, "actual_limit", actualLimit, "result", result)

	return result
}

// parseContainerCreatedTime parses container creation time from string and returns string
func (c *dockerMetricsCollector) parseContainerCreatedTime(createdStr string) string {
	// Check for empty string
	if createdStr == "" {
		return time.Now().Format(time.RFC3339)
	}

	// Try to parse in RFC3339 format
	if t, err := time.Parse(time.RFC3339, createdStr); err == nil {
		return t.Format(time.RFC3339)
	}
	// Try to parse in RFC3339Nano format
	if t, err := time.Parse(time.RFC3339Nano, createdStr); err == nil {
		return t.Format(time.RFC3339)
	}
	// If failed, return current time
	return time.Now().Format(time.RFC3339)
}

// parseContainerFinishedTime parses container finished time from string and returns string
func (c *dockerMetricsCollector) parseContainerFinishedTime(finishedStr string) string {
	// Check for empty string (container is still running)
	if finishedStr == "" || finishedStr == "0001-01-01T00:00:00Z" {
		return ""
	}

	// Try to parse in RFC3339 format
	if t, err := time.Parse(time.RFC3339, finishedStr); err == nil {
		return t.Format(time.RFC3339)
	}
	// Try to parse in RFC3339Nano format
	if t, err := time.Parse(time.RFC3339Nano, finishedStr); err == nil {
		return t.Format(time.RFC3339)
	}
	// If failed, return empty string
	return ""
}

// extractStackName extracts the docker-compose project name from container labels
func (c *dockerMetricsCollector) extractStackName(labels map[string]string) string {
	if labels == nil {
		return ""
	}

	// Primary Docker Compose project label
	if project, exists := labels["com.docker.compose.project"]; exists && project != "" {
		return project
	}

	// Alternative compose labels that might contain project name
	if project, exists := labels["com.docker.compose.project.config_files"]; exists && project != "" {
		return project
	}

	// Docker Swarm stack namespace
	if stack, exists := labels["com.docker.stack.namespace"]; exists && stack != "" {
		return stack
	}

	// Kubernetes namespace (for containers running in k8s)
	if namespace, exists := labels["io.kubernetes.pod.namespace"]; exists && namespace != "" {
		return namespace
	}

	// Custom stack label (for manually labeled containers)
	if stack, exists := labels["custom.stack.name"]; exists && stack != "" {
		return stack
	}

	// Environment variable based stack detection
	if stack, exists := labels["env.STACK_NAME"]; exists && stack != "" {
		return stack
	}

	return ""
}

// mergeStacksByCommonPrefix merges stacks that have common prefixes in their names
// For example: myapp-web, myapp-api, myapp-db -> merged into myapp stack
func (c *dockerMetricsCollector) mergeStacksByCommonPrefix(stackMap map[string]*DockerStack) map[string]*DockerStack {
	if len(stackMap) <= 1 {
		return stackMap
	}

	// Collect all stack names
	stackNames := make([]string, 0, len(stackMap))
	for name := range stackMap {
		stackNames = append(stackNames, name)
	}

	// Find merge candidates - stacks with common prefixes
	mergeGroups := c.findCommonPrefixGroups(stackNames)

	// If no merges needed, return original map
	if len(mergeGroups) == 0 {
		return stackMap
	}

	// Create new map with merged stacks
	mergedMap := make(map[string]*DockerStack)

	// Track which stacks have been merged
	mergedStacks := make(map[string]bool)

	// Process each merge group
	for commonPrefix, groupStacks := range mergeGroups {
		if len(groupStacks) < 2 {
			continue // Skip groups with only one stack
		}

		c.logger.Debug("Merging stacks with common prefix", "prefix", commonPrefix, "stacks", groupStacks)

		// Create merged stack
		mergedStack := &DockerStack{
			Name:       commonPrefix,
			Containers: make([]DockerContainer, 0),
		}

		// Collect all containers from stacks in this group
		for _, stackName := range groupStacks {
			if stack, exists := stackMap[stackName]; exists {
				mergedStack.Containers = append(mergedStack.Containers, stack.Containers...)
				mergedStack.TotalContainers += stack.TotalContainers
				mergedStack.RunningContainers += stack.RunningContainers
				mergedStacks[stackName] = true
			}
		}

		mergedMap[commonPrefix] = mergedStack
	}

	// Add unmerged stacks
	for stackName, stack := range stackMap {
		if !mergedStacks[stackName] {
			mergedMap[stackName] = stack
		}
	}

	c.logger.Debug("Stack merging completed", "original_stacks", len(stackMap), "merged_stacks", len(mergedMap))
	return mergedMap
}

// findCommonPrefixGroups finds groups of stack names that share common prefixes
func (c *dockerMetricsCollector) findCommonPrefixGroups(stackNames []string) map[string][]string {
	prefixGroups := make(map[string][]string)

	for _, stackName := range stackNames {
		// Skip single containers or unknown stacks
		if stackName == "unknown" || !strings.Contains(stackName, "-") {
			continue
		}

		// Extract potential prefixes (parts before the last hyphen)
		parts := strings.Split(stackName, "-")
		if len(parts) < 2 {
			continue
		}

		// Try different prefix lengths
		for i := 1; i < len(parts); i++ {
			prefix := strings.Join(parts[:i], "-")

			// Skip too short prefixes
			if len(prefix) < 3 {
				continue
			}

			// Check if this prefix makes sense (should be followed by a service name)
			if i >= len(parts)-1 {
				continue
			}

			prefixGroups[prefix] = append(prefixGroups[prefix], stackName)
		}
	}

	// Filter groups to only include those with multiple stacks
	result := make(map[string][]string)
	for prefix, stacks := range prefixGroups {
		if len(stacks) >= 2 {
			// Remove duplicates
			uniqueStacks := c.removeDuplicates(stacks)
			if len(uniqueStacks) >= 2 {
				result[prefix] = uniqueStacks
			}
		}
	}

	return result
}

// removeDuplicates removes duplicate strings from slice
func (c *dockerMetricsCollector) removeDuplicates(items []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(items))

	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}

	return result
}

// GetContainerLogs returns the last `tail` lines of a container's logs (stdout +
// stderr) with timestamps. Non-TTY streams are multiplexed and demuxed via
// stdcopy; TTY streams are copied raw.
func (c *dockerMetricsCollector) GetContainerLogs(ctx context.Context, containerID string, tail int) (string, error) {
	if !c.IsDockerAvailable(ctx) || c.client == nil {
		return "", fmt.Errorf("docker daemon not available")
	}
	if tail <= 0 {
		tail = 200
	}

	tty := false
	if info, err := c.client.ContainerInspect(ctx, containerID); err == nil && info.Config != nil {
		tty = info.Config.Tty
	}

	rc, err := c.client.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return "", err
	}
	defer rc.Close()

	var buf bytes.Buffer
	if tty {
		if _, err := io.Copy(&buf, rc); err != nil && !errors.Is(err, io.EOF) {
			return buf.String(), err
		}
	} else {
		if _, err := stdcopy.StdCopy(&buf, &buf, rc); err != nil && !errors.Is(err, io.EOF) {
			return buf.String(), err
		}
	}
	return buf.String(), nil
}

func (c *dockerMetricsCollector) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// extraDockerHostCandidates are tried when FromEnv/default socket does not answer.
func extraDockerHostCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		var hosts []string
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			hosts = append(hosts, "unix://"+filepath.Join(home, ".docker/run/docker.sock"))
		}
		return append(hosts, "unix:///var/run/docker.sock")
	case "linux":
		return []string{"unix:///var/run/docker.sock"}
	case "windows":
		return []string{"npipe:////./pipe/docker_engine"}
	default:
		return nil
	}
}

// tryOpenDockerClient returns a client that successfully Ping()'s, or nil.
func tryOpenDockerClient(ctx context.Context, logger *log.Logger) *client.Client {
	if cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation()); err == nil {
		if _, err := cli.Ping(ctx); err == nil {
			logger.Info("Docker API reachable", "host", cli.DaemonHost())
			return cli
		}
		logger.Debug("Docker default client ping failed", "host", cli.DaemonHost(), "error", err)
		_ = cli.Close()
	} else {
		logger.Debug("Docker client from environment failed", "error", err)
	}

	for _, h := range extraDockerHostCandidates() {
		cli, err := client.NewClientWithOpts(
			client.WithHost(h),
			client.WithAPIVersionNegotiation(),
		)
		if err != nil {
			logger.Debug("Docker client create failed", "host", h, "error", err)
			continue
		}
		if _, err := cli.Ping(ctx); err != nil {
			logger.Debug("Docker ping failed", "host", h, "error", err)
			_ = cli.Close()
			continue
		}
		logger.Info("Docker API reachable", "host", h)
		return cli
	}

	logger.Warn("Docker daemon not reachable — start Docker or set DOCKER_HOST")
	return nil
}
