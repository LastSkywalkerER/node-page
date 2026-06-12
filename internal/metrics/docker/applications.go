package docker

import (
	"os"
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

	// UpdatesAvailable is the number of containers with a newer *version* in the
	// registry (the version label increased, e.g. 7.4.8 → 7.4.9), plus updates
	// whose remote version couldn't be resolved.
	UpdatesAvailable int `json:"updates_available"`

	// PatchesAvailable is the number of containers whose image was rebuilt under
	// the same version tag — a newer digest with an identical version label
	// (typically rebuilt point releases / security patches of a rolling tag like
	// postgres:18). Tracked separately from UpdatesAvailable.
	PatchesAvailable int `json:"patches_available"`

	// MajorUpdatesAvailable is the number of containers with a newer MAJOR
	// version tag available in the registry (e.g. 1.2.1 → 2.0.0; potentially
	// breaking). Tracked separately so a major bump is never silently lumped in
	// with routine updates.
	MajorUpdatesAvailable int `json:"major_updates_available"`

	// IconSlug is the resolved icon slug (or explicit override) for the app.
	IconSlug string `json:"icon_slug,omitempty"`

	// PublicURL is a reverse-proxy / homepage derived public link, if detected.
	PublicURL string `json:"public_url,omitempty"`
}

// resolveProject returns (project, service, isSingleton) derived from container
// labels. Compose is preferred, then swarm stacks, then standalone swarm
// services; otherwise the container is its own standalone application.
func resolveProject(labels map[string]string, containerName string) (string, string, bool) {
	if labels != nil {
		// docker-compose project (compose v2 / dokploy "Compose").
		if p := labels["com.docker.compose.project"]; p != "" {
			return p, labels["com.docker.compose.service"], false
		}
		// Swarm stack (`docker stack deploy`): group by stack namespace. Stack
		// service names are "<namespace>_<service>" — trim the prefix so the
		// service column is just the service.
		if p := labels["com.docker.stack.namespace"]; p != "" {
			svc := labels["com.docker.swarm.service.name"]
			return p, strings.TrimPrefix(svc, p+"_"), false
		}
		// Standalone Swarm service (dokploy's default deploy mode): no stack
		// namespace, but a service name is present. Group all replicas /
		// task-attempts under the service so each task ("<svc>.<slot>.<id>")
		// doesn't become a phantom singleton.
		if p := labels["com.docker.swarm.service.name"]; p != "" {
			return p, p, false
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
			if app.PublicURL == "" {
				app.PublicURL = publicURL(c.Labels)
			}
			// Fall back to a reverse-proxy route URL discovered from the
			// Traefik file provider (attached to a container port at collection
			// time) when no label-based URL was found.
			if app.PublicURL == "" {
				app.PublicURL = firstContainerPortURL(c)
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
			a, b := app.Ports[i], app.Ports[j]
			if a.PublicPort != b.PublicPort {
				return a.PublicPort < b.PublicPort
			}
			if a.PrivatePort != b.PrivatePort {
				return a.PrivatePort < b.PrivatePort
			}
			if a.Type != b.Type {
				return a.Type < b.Type
			}
			// Final tiebreaker so routed-only ports (PublicPort=0, same type)
			// don't reorder between refreshes under the unstable sort.
			return a.PublicURL < b.PublicURL
		})
		sort.Slice(app.Volumes, func(i, j int) bool {
			if app.Volumes[i].Destination != app.Volumes[j].Destination {
				return app.Volumes[i].Destination < app.Volumes[j].Destination
			}
			return app.Volumes[i].Source < app.Volumes[j].Source
		})
		// Resolve the icon from the application's MAIN (user-facing) container —
		// not the first one, which is often a database/cache.
		app.IconSlug = resolveAppIcon(app.Containers)
		for _, c := range app.Containers {
			rebuild := isRebuildUpdate(c)
			if rebuild {
				app.PatchesAvailable++
			}
			// A "version update": a same-tag digest bump whose version label changed,
			// or a newer same-major version tag (e.g. pinned 1.2.1 → 1.3.2).
			if (c.UpdateAvailable && !rebuild) || c.NewerVersion != "" {
				app.UpdatesAvailable++
			}
			if c.NewerMajorVersion != "" {
				app.MajorUpdatesAvailable++
			}
		}
		out = append(out, *app)
	}
	// Fallback: merge apps that share a common name prefix into one logical app
	// (e.g. Dokploy's "node-stats-app-…", "node-stats-db-…", "node-stats-compose-…"
	// → "node-stats"). No-op for distinctly-named compose projects.
	out = regroupByCommonPrefix(out)
	sort.Slice(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return out
}

// isRebuildUpdate reports whether a container's available update is a same-version
// rebuild (newer digest, identical version label — e.g. a rebuilt postgres:18
// point release) rather than a version bump. Used to count patches separately
// from version updates.
func isRebuildUpdate(c DockerContainer) bool {
	return c.UpdateAvailable && c.RemoteVersion != "" && c.RemoteVersion == c.ImageVersion
}

// prefixGroupingEnabled reports whether the common-name-prefix fallback grouping
// is on (default true; disable with NODE_STATS_APP_PREFIX_GROUPING=false/0/off).
func prefixGroupingEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("NODE_STATS_APP_PREFIX_GROUPING"))) {
	case "false", "0", "off", "no":
		return false
	}
	return true
}

// nameTokens splits an app/project name into its dash/underscore tokens.
func nameTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' })
}

// regroupByCommonPrefix merges applications whose names share a common
// dash/underscore-delimited prefix into one logical app. This is a fallback for
// orchestrators (e.g. Dokploy) that name every service of one logical app like
// "<project>-<service>-<hash>" without a shared compose-project label.
//
// It runs two passes:
//  1. minTokens=1 — merges single-token projects whose services are uniquely
//     named (e.g. dokploy / dokploy-postgres / dokploy-redis → "dokploy";
//     ebcenter-app / ebcenter-db → "ebcenter").
//  2. minTokens=2 — re-merges multi-token projects that pass 1 split because a
//     service runs as several instances with distinct random suffixes, which
//     inflate a deeper prefix's count (e.g. a Dokploy swarm project with
//     db / docsdb / backend×2 / frontend×2 → all under "docs-templater").
//     Restricting pass 2 to ≥2-token prefixes keeps unrelated single-token
//     coincidences apart (e.g. "docs-foo" never absorbs "docs-templater").
//
// Apps with no qualifying shared prefix are left untouched.
func regroupByCommonPrefix(apps []DockerApplication) []DockerApplication {
	if !prefixGroupingEnabled() {
		return apps
	}
	apps = groupByPrefix(apps, 1)
	apps = groupByPrefix(apps, 2)
	return apps
}

// groupByPrefix assigns each application to the longest dash/underscore prefix
// of at least minTokens tokens that is shared by ≥2 applications, merging those
// that land on the same prefix. Apps with no such prefix are kept as-is.
func groupByPrefix(apps []DockerApplication, minTokens int) []DockerApplication {
	if len(apps) < 2 {
		return apps
	}

	toks := make([][]string, len(apps))
	prefixCount := map[string]int{}
	for i, a := range apps {
		toks[i] = nameTokens(a.Project)
		for j := minTokens; j <= len(toks[i]); j++ {
			prefixCount[strings.Join(toks[i][:j], "-")]++
		}
	}

	// Assign each app to the LONGEST qualifying prefix shared by ≥2 apps; else keep it alone.
	groups := map[string][]DockerApplication{}
	order := []string{}
	for i, a := range apps {
		key := "self:" + a.Project
		for j := len(toks[i]); j >= minTokens; j-- {
			pfx := strings.Join(toks[i][:j], "-")
			if prefixCount[pfx] >= 2 {
				key = "pfx:" + pfx
				break
			}
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], a)
	}

	out := make([]DockerApplication, 0, len(order))
	for _, key := range order {
		members := groups[key]
		if len(members) == 1 {
			out = append(out, members[0])
			continue
		}
		out = append(out, mergeApplications(strings.TrimPrefix(key, "pfx:"), members))
	}
	return out
}

// mergeApplications combines several applications into one logical app keyed by
// the shared name prefix, summing usage and unioning ports/volumes/containers.
func mergeApplications(project string, members []DockerApplication) DockerApplication {
	sort.Slice(members, func(i, j int) bool { return members[i].Project < members[j].Project })
	out := DockerApplication{
		Project:     project,
		DisplayName: humanizeProject(project),
		HostID:      members[0].HostID,
	}
	for _, m := range members {
		out.Containers = append(out.Containers, m.Containers...)
		out.TotalContainers += m.TotalContainers
		out.RunningContainers += m.RunningContainers
		out.TotalSizeRw += m.TotalSizeRw
		out.TotalSizeRootFs += m.TotalSizeRootFs
		out.UpdatesAvailable += m.UpdatesAvailable
		out.PatchesAvailable += m.PatchesAvailable
		out.MajorUpdatesAvailable += m.MajorUpdatesAvailable
		sumStats(&out.Stats, m.Stats)
		out.Ports = mergePublicPorts(out.Ports, m.Ports)
		out.Volumes = mergeVolumes(out.Volumes, m.Volumes)
		if out.PublicURL == "" {
			out.PublicURL = m.PublicURL
		}
	}
	// Re-resolve the icon across ALL merged containers so the main service wins
	// (members were grouped by name prefix; the first member is often a "-db").
	out.IconSlug = resolveAppIcon(out.Containers)
	out.IsSingleton = out.TotalContainers == 1
	out.ServiceCount = countServices(out.Containers)
	sort.Slice(out.Containers, func(i, j int) bool { return out.Containers[i].Name < out.Containers[j].Name })
	sort.Slice(out.Ports, func(i, j int) bool {
		a, b := out.Ports[i], out.Ports[j]
		if a.PublicPort != b.PublicPort {
			return a.PublicPort < b.PublicPort
		}
		if a.PrivatePort != b.PrivatePort {
			return a.PrivatePort < b.PrivatePort
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.PublicURL < b.PublicURL
	})
	sort.Slice(out.Volumes, func(i, j int) bool {
		if out.Volumes[i].Destination != out.Volumes[j].Destination {
			return out.Volumes[i].Destination < out.Volumes[j].Destination
		}
		return out.Volumes[i].Source < out.Volumes[j].Source
	})
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

// mergePublicPorts unions ports that are externally reachable — either
// published to the host (PublicPort) or routed by a reverse proxy (PublicURL,
// even when the port isn't host-published) — de-duplicated by
// (PublicPort, Type, PublicURL).
func mergePublicPorts(acc []DockerPort, ports []DockerPort) []DockerPort {
	for _, p := range ports {
		if p.PublicPort == 0 && p.PublicURL == "" {
			continue
		}
		dup := false
		for _, e := range acc {
			if e.PublicPort == p.PublicPort && e.Type == p.Type && e.PublicURL == p.PublicURL {
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

// infraImageTokens matches images/services that are typically SUPPORTING
// services (databases, caches, queues, search, object storage, proxies) rather
// than the user-facing "main" app of a stack. Used to pick which container's
// icon best represents the application.
var infraImageTokens = []string{
	"postgres", "postgresql", "pgvector", "timescaledb", "supabase", "mysql",
	"mariadb", "percona", "mongo", "mongodb", "redis", "valkey", "keydb",
	"dragonfly", "memcached", "rabbitmq", "kafka", "zookeeper", "nats",
	"pulsar", "elasticsearch", "opensearch", "solr", "meilisearch", "typesense",
	"clickhouse", "cassandra", "scylla", "cockroach", "influxdb", "victoriametrics",
	"etcd", "consul", "vault", "minio", "garage", "traefik", "haproxy", "varnish",
	"pgbouncer", "pgadmin", "adminer", "mailhog", "maildev",
}

// infraServiceTokens are common compose service names for supporting services.
var infraServiceTokens = []string{
	"db", "database", "postgres", "mysql", "mariadb", "mongo", "redis", "cache",
	"valkey", "queue", "mq", "broker", "amqp", "rabbitmq", "kafka", "search",
	"elastic", "storage", "s3", "minio", "proxy", "traefik",
}

// isInfraContainer reports whether a container looks like a supporting service
// (DB/cache/queue/search/storage/proxy) rather than the main user-facing app.
func isInfraContainer(c DockerContainer) bool {
	seg := strings.ToLower(lastImageSegment(c.Image))
	for _, tok := range infraImageTokens {
		if seg == tok || strings.HasPrefix(seg, tok) {
			return true
		}
	}
	svc := strings.ToLower(strings.TrimSpace(c.Service))
	for _, tok := range infraServiceTokens {
		if svc == tok {
			return true
		}
	}
	return false
}

// containerHasPublicEntry reports whether a container exposes a host-published
// port or a reverse-proxy route — a signal that it's the user-facing service.
func containerHasPublicEntry(c DockerContainer) bool {
	for _, p := range c.Ports {
		if p.PublicPort != 0 || p.PublicURL != "" {
			return true
		}
	}
	return false
}

// pickMainContainerIndex returns the index of the application's main
// (user-facing) container, scoring non-infra services and externally-reachable
// containers above databases/caches. Returns -1 for no containers; for an
// all-infra stack it returns the best-scoring (first, deterministic) member so
// a database-only stack still gets its database's icon.
func pickMainContainerIndex(containers []DockerContainer) int {
	best, bestScore := -1, -1<<31
	for i, c := range containers {
		score := 0
		if !isInfraContainer(c) {
			score += 100
		}
		if containerHasPublicEntry(c) {
			score += 20
		}
		if publicURL(c.Labels) != "" {
			score += 10
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

// imageDerivedSlug derives an icon slug from a container's image, then its
// service / project name (no label lookup).
func imageDerivedSlug(c DockerContainer) string {
	if s := slugify(lastImageSegment(c.Image)); s != "" {
		return s
	}
	if s := slugify(c.Service); s != "" {
		return s
	}
	return slugify(c.Project)
}

// resolveAppIcon picks an application's icon slug: an explicit nodestats.icon
// label on ANY container wins; otherwise it derives the slug from the main
// (user-facing) container so a stack's app icon — not its database — represents it.
func resolveAppIcon(containers []DockerContainer) string {
	for _, c := range containers {
		if c.Labels != nil {
			if v := strings.TrimSpace(c.Labels["nodestats.icon"]); v != "" {
				return v
			}
		}
	}
	// Dashboard-style icon labels (CasaOS, homepage, ...) carry a ready-to-use
	// URL - far better than guessing a slug from the image name.
	for _, c := range containers {
		if c.Labels != nil {
			if v := strings.TrimSpace(c.Labels["icon"]); strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
				return v
			}
		}
	}
	idx := pickMainContainerIndex(containers)
	if idx < 0 {
		return ""
	}
	return imageDerivedSlug(containers[idx])
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
