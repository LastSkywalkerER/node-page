package docker

import (
	"regexp"
	"sort"
	"strings"
)

// DockerApplication is a read-time projection that groups a host's containers
// into a single logical application: a docker-compose project, a swarm stack,
// or a standalone (non-compose) container. It is computed on read from the
// stored per-container rows — there is no applications table.
type DockerApplication struct {
	// Project is the grouping key (compose project / swarm namespace / container name).
	Project string `json:"project"`

	// HostID is the node this application runs on (set by the all-hosts aggregation).
	HostID uint `json:"host_id,omitempty"`

	// DisplayName is a humanized version of Project for the UI.
	DisplayName string `json:"display_name"`

	// IsSingleton is true when the application is a single non-compose container.
	IsSingleton bool `json:"is_singleton"`

	// TotalContainers / RunningContainers across the application.
	TotalContainers   int `json:"total_containers"`
	RunningContainers int `json:"running_containers"`

	// ServiceCount is the number of distinct compose services (or container count).
	ServiceCount int `json:"service_count"`

	// Containers are the members of this application, sorted by name.
	Containers []DockerContainer `json:"containers"`

	// Stats is the sum of resource usage across all containers.
	Stats DockerStats `json:"stats"`

	// Ports is the union of published (public) ports across containers.
	Ports []DockerPort `json:"ports"`

	// TotalSizeRw is the summed writable-layer size across containers (bytes) —
	// the app's unique on-disk data footprint.
	TotalSizeRw int64 `json:"total_size_rw"`

	// TotalSizeRootFs is the summed total filesystem size across containers (bytes).
	// Over-counts image layers shared between containers of the same image.
	TotalSizeRootFs int64 `json:"total_size_root_fs"`

	// Volumes is the de-duplicated union of the app's volume/bind mounts.
	Volumes []DockerMount `json:"volumes,omitempty"`

	// UpdatesAvailable is the number of containers with a newer image available.
	UpdatesAvailable int `json:"updates_available"`

	// IconSlug is the resolved icon slug (or explicit override) for the app.
	IconSlug string `json:"icon_slug,omitempty"`

	// PublicURL is a reverse-proxy / homepage derived public link, if detected.
	PublicURL string `json:"public_url,omitempty"`
}

// resolveProject returns (project, service, isSingleton) derived from container
// labels. Compose is preferred, then swarm; otherwise the container is its own
// standalone application.
func resolveProject(labels map[string]string, containerName string) (string, string, bool) {
	if labels != nil {
		if p := labels["com.docker.compose.project"]; p != "" {
			return p, labels["com.docker.compose.service"], false
		}
		if p := labels["com.docker.stack.namespace"]; p != "" {
			return p, labels["com.docker.swarm.service.name"], false
		}
	}
	return containerName, "", true
}

// isOneOff reports whether the container is a one-shot `docker compose run`
// container, which must not form a phantom application.
func isOneOff(labels map[string]string) bool {
	return labels != nil && strings.EqualFold(labels["com.docker.compose.oneoff"], "true")
}

// BuildApplications projects a DockerMetric (whose containers carry Project and
// Labels) into a sorted slice of applications.
func BuildApplications(m *DockerMetric) []DockerApplication {
	if m == nil {
		return nil
	}

	groups := map[string]*DockerApplication{}
	order := []string{}

	for _, st := range m.Stacks {
		for _, c := range st.Containers {
			// One-shot `compose run` containers must not form phantom apps.
			if isOneOff(c.Labels) {
				continue
			}
			key := c.Project
			if key == "" {
				key = c.Name
			}

			app := groups[key]
			if app == nil {
				_, _, singleton := resolveProject(c.Labels, c.Name)
				app = &DockerApplication{
					Project:     key,
					DisplayName: humanizeProject(key),
					IsSingleton: singleton,
				}
				groups[key] = app
				order = append(order, key)
			}

			app.Containers = append(app.Containers, c)
			app.TotalContainers++
			if c.State == "running" {
				app.RunningContainers++
			}
			sumStats(&app.Stats, c.Stats)
			app.Ports = mergePublicPorts(app.Ports, c.Ports)
			app.TotalSizeRw += c.SizeRw
			app.TotalSizeRootFs += c.SizeRootFs
			app.Volumes = mergeVolumes(app.Volumes, c.Mounts)
			if app.IconSlug == "" {
				app.IconSlug = iconSlug(c)
			}
			if app.PublicURL == "" {
				app.PublicURL = publicURL(c.Labels)
			}
		}
	}

	out := make([]DockerApplication, 0, len(order))
	for _, key := range order {
		app := groups[key]
		app.ServiceCount = countServices(app.Containers)
		// Deterministic ordering everywhere so rows don't jump between refreshes.
		sort.Slice(app.Containers, func(i, j int) bool { return app.Containers[i].Name < app.Containers[j].Name })
		sort.Slice(app.Ports, func(i, j int) bool {
			if app.Ports[i].PublicPort != app.Ports[j].PublicPort {
				return app.Ports[i].PublicPort < app.Ports[j].PublicPort
			}
			return app.Ports[i].Type < app.Ports[j].Type
		})
		sort.Slice(app.Volumes, func(i, j int) bool {
			if app.Volumes[i].Destination != app.Volumes[j].Destination {
				return app.Volumes[i].Destination < app.Volumes[j].Destination
			}
			return app.Volumes[i].Source < app.Volumes[j].Source
		})
		for _, c := range app.Containers {
			if c.UpdateAvailable {
				app.UpdatesAvailable++
			}
		}
		out = append(out, *app)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return out
}

// humanizeProject turns "my-cool_app" into "My Cool App" for display.
func humanizeProject(project string) string {
	if project == "" {
		return project
	}
	repl := strings.NewReplacer("-", " ", "_", " ")
	parts := strings.Fields(repl.Replace(project))
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func sumStats(dst *DockerStats, s DockerStats) {
	dst.CPUPercent += s.CPUPercent
	dst.CPUPercentOfLimit += s.CPUPercentOfLimit
	dst.CPULimit += s.CPULimit
	dst.MemoryUsage += s.MemoryUsage
	dst.MemoryLimit += s.MemoryLimit
	dst.NetworkRx += s.NetworkRx
	dst.NetworkTx += s.NetworkTx
	dst.BlockRead += s.BlockRead
	dst.BlockWrite += s.BlockWrite
	// MemoryPercent is recomputed from the summed usage/limit (averaging
	// per-container percentages is meaningless across different limits).
	if dst.MemoryLimit > 0 {
		dst.MemoryPercent = float64(dst.MemoryUsage) / float64(dst.MemoryLimit) * 100.0
	}
}

// mergePublicPorts unions published ports, de-duplicated by (PublicPort, Type).
func mergePublicPorts(acc []DockerPort, ports []DockerPort) []DockerPort {
	for _, p := range ports {
		if p.PublicPort == 0 {
			continue
		}
		dup := false
		for _, e := range acc {
			if e.PublicPort == p.PublicPort && e.Type == p.Type {
				dup = true
				break
			}
		}
		if !dup {
			acc = append(acc, p)
		}
	}
	return acc
}

// mergeVolumes unions volume/bind mounts, de-duplicated by (type, source,
// destination). tmpfs mounts (no host path) are skipped.
func mergeVolumes(acc []DockerMount, mounts []DockerMount) []DockerMount {
	for _, m := range mounts {
		if m.Type == "tmpfs" || m.Source == "" {
			continue
		}
		dup := false
		for _, e := range acc {
			if e.Type == m.Type && e.Source == m.Source && e.Destination == m.Destination {
				dup = true
				break
			}
		}
		if !dup {
			acc = append(acc, m)
		}
	}
	return acc
}

// countServices counts distinct compose services, falling back to the number of
// containers when services are not labeled (e.g. standalone containers).
func countServices(containers []DockerContainer) int {
	set := map[string]struct{}{}
	unnamed := 0
	for _, c := range containers {
		if c.Service != "" {
			set[c.Service] = struct{}{}
		} else {
			unnamed++
		}
	}
	return len(set) + unnamed
}

var (
	imageTagOrDigest = regexp.MustCompile(`[:@][^/]*$`)
	nonSlug          = regexp.MustCompile(`[^a-z0-9]+`)
	traefikHostRule  = regexp.MustCompile("Host\\(\\s*`([^`]+)`")
)

// iconSlug derives an icon slug for the container: an explicit override label
// wins, otherwise it is derived from the image reference, then the service /
// project name.
func iconSlug(c DockerContainer) string {
	if c.Labels != nil {
		if v := strings.TrimSpace(c.Labels["nodestats.icon"]); v != "" {
			return v
		}
	}
	if s := slugify(lastImageSegment(c.Image)); s != "" {
		return s
	}
	if s := slugify(c.Service); s != "" {
		return s
	}
	return slugify(c.Project)
}

// lastImageSegment strips registry/namespace and any tag/digest from an image
// reference: "lscr.io/linuxserver/plex:latest" -> "plex".
func lastImageSegment(image string) string {
	img := strings.TrimSpace(image)
	if img == "" {
		return ""
	}
	if i := strings.LastIndex(img, "/"); i >= 0 {
		img = img[i+1:]
	}
	return imageTagOrDigest.ReplaceAllString(img, "")
}

func slugify(s string) string {
	s = nonSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	return strings.Trim(s, "-")
}

// publicURL derives a public link from common reverse-proxy / dashboard labels.
func publicURL(labels map[string]string) string {
	if labels == nil {
		return ""
	}

	for _, k := range []string{
		"nodestats.url", "nodestats.href",
		"homepage.href",
		"homepage.instance.public.href", "homepage.instance.internal.href",
	} {
		if v := strings.TrimSpace(labels[k]); v != "" {
			return v
		}
	}

	// Traefik
	if strings.EqualFold(labels["traefik.enable"], "true") {
		for k, v := range labels {
			if strings.HasPrefix(k, "traefik.http.routers.") && strings.HasSuffix(k, ".rule") {
				if m := traefikHostRule.FindStringSubmatch(v); len(m) == 2 {
					scheme := "http"
					if traefikIsTLS(labels) {
						scheme = "https"
					}
					return scheme + "://" + m[1]
				}
			}
		}
	}

	// nginx-proxy
	if vh := firstToken(labels["VIRTUAL_HOST"]); vh != "" {
		scheme := "http"
		if labels["LETSENCRYPT_HOST"] != "" {
			scheme = "https"
		}
		if port := strings.TrimSpace(labels["VIRTUAL_PORT"]); port != "" && port != "80" && port != "443" {
			return scheme + "://" + vh + ":" + port
		}
		return scheme + "://" + vh
	}

	// Caddy (caddy-docker-proxy uses `caddy`; caddy-gen uses `virtual.host`)
	if v := strings.TrimSpace(labels["caddy"]); v != "" {
		v = firstToken(v)
		if strings.Contains(v, "://") {
			return v
		}
		return "https://" + v
	}
	if vh := firstToken(labels["virtual.host"]); vh != "" {
		return "https://" + vh
	}

	return ""
}

func traefikIsTLS(labels map[string]string) bool {
	for k, v := range labels {
		if !strings.HasPrefix(k, "traefik.http.routers.") {
			continue
		}
		switch {
		case strings.HasSuffix(k, ".tls") && strings.EqualFold(v, "true"):
			return true
		case strings.Contains(k, ".tls.certresolver"):
			return true
		case strings.HasSuffix(k, ".entrypoints") && strings.Contains(strings.ToLower(v), "websecure"):
			return true
		}
	}
	return false
}

// firstToken returns the first space/comma separated token of s.
func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, sep := range []string{",", " "} {
		if i := strings.Index(s, sep); i >= 0 {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}
