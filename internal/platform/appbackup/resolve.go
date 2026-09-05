package appbackup

import (
	"path/filepath"
	"sort"
	"strings"

	docker "system-stats/internal/metrics/docker"
)

// Turning a discovered application into a job is pure projection over data the
// docker collector already stores: the compose config-file labels and each
// container's mounts (docker.DockerMount carries the host path and size).

// skipMountPrefixes are kernel/runtime mounts that are never application data.
// A container that bind-mounts the docker socket or /proc must not drag them
// into a snapshot — or, far worse, into a restore's wipe list.
var skipMountPrefixes = []string{"/proc", "/sys", "/dev", "/run", "/var/run"}

// skipMountExact are the small host files images conventionally mount for
// clock and locale. Backing them up is noise; restoring them is wrong.
var skipMountExact = map[string]bool{
	"/etc/localtime":   true,
	"/etc/timezone":    true,
	"/etc/hostname":    true,
	"/etc/resolv.conf": true,
	"/etc/hosts":       true,
}

// ResolvePaths reduces an application's containers to the deduplicated set of
// data locations worth preserving, each annotated with the services that mount
// it. Paths nested inside another kept path are dropped: restic would store
// them twice and a restore would wipe the parent anyway.
func ResolvePaths(app *docker.DockerApplication) []BackupPath {
	if app == nil {
		return nil
	}
	bySource := map[string]*BackupPath{}
	for _, c := range app.Containers {
		svc := c.Service
		if svc == "" {
			svc = strings.TrimPrefix(c.Name, "/")
		}
		for _, m := range c.Mounts {
			if !keepMount(m) {
				continue
			}
			src := filepath.Clean(m.Source)
			p, ok := bySource[src]
			if !ok {
				kind := m.Type
				if kind == "" {
					kind = "bind"
				}
				p = &BackupPath{Kind: kind, Name: m.Name, Source: src, Size: m.Size}
				bySource[src] = p
			}
			if m.Size > p.Size {
				p.Size = m.Size
			}
			if !contains(p.Services, svc) {
				p.Services = append(p.Services, svc)
			}
		}
	}

	var sources []string
	for s := range bySource {
		sources = append(sources, s)
	}
	sort.Strings(sources)

	var out []BackupPath
	for _, s := range sources {
		nested := false
		for i := range out {
			if strings.HasPrefix(s, out[i].Source+"/") {
				// Fold the nested mount's services into its parent so the UI
				// still shows who uses it.
				for _, svc := range bySource[s].Services {
					if !contains(out[i].Services, svc) {
						out[i].Services = append(out[i].Services, svc)
					}
				}
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		p := *bySource[s]
		out = append(out, p)
	}
	for i := range out {
		sort.Strings(out[i].Services)
	}
	return out
}

func keepMount(m docker.DockerMount) bool {
	if m.Type == "tmpfs" || m.Source == "" {
		return false
	}
	if !filepath.IsAbs(m.Source) {
		return false
	}
	src := filepath.Clean(m.Source)
	if skipMountExact[src] {
		return false
	}
	for _, p := range skipMountPrefixes {
		if src == p || strings.HasPrefix(src, p+"/") {
			return false
		}
	}
	return true
}

// ResolveProject extracts the compose project directory and config files from
// an application's container labels, which is where every `docker compose`
// deployment records them.
func ResolveProject(app *docker.DockerApplication) (dir string, files []string) {
	if app == nil {
		return "", nil
	}
	seen := map[string]bool{}
	for _, c := range app.Containers {
		if dir == "" {
			dir = strings.TrimSpace(c.ComposeWorkingDir)
		}
		for _, f := range strings.Split(c.ComposeConfigFiles, ",") {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			files = append(files, f)
		}
	}
	sort.Strings(files)
	if dir == "" && len(files) > 0 {
		dir = filepath.Dir(files[0])
	}
	return dir, files
}

// ServiceState is one compose service as offered to the operator: what it runs
// now and what it could move to.
type ServiceState struct {
	Service string `json:"service"`
	// Image is the reference the container was created from.
	Image string `json:"image"`
	// Repo and Tag are Image split for the version picker.
	Repo string `json:"repo"`
	Tag  string `json:"tag"`
	// Containers lists the container names of this service.
	Containers []string `json:"containers"`
	// UpdateAvailable mirrors the collector's own detection.
	UpdateAvailable bool `json:"update_available"`
}

// ResolveServices projects an application's containers into per-service state.
func ResolveServices(app *docker.DockerApplication) []ServiceState {
	if app == nil {
		return nil
	}
	idx := map[string]*ServiceState{}
	var order []string
	for _, c := range app.Containers {
		svc := c.Service
		if svc == "" {
			svc = strings.TrimPrefix(c.Name, "/")
		}
		s, ok := idx[svc]
		if !ok {
			repo, tag := SplitImageRef(c.Image)
			s = &ServiceState{Service: svc, Image: c.Image, Repo: repo, Tag: tag}
			idx[svc] = s
			order = append(order, svc)
		}
		s.Containers = append(s.Containers, strings.TrimPrefix(c.Name, "/"))
		if c.UpdateAvailable {
			s.UpdateAvailable = true
		}
	}
	sort.Strings(order)
	out := make([]ServiceState, 0, len(order))
	for _, svc := range order {
		s := idx[svc]
		sort.Strings(s.Containers)
		out = append(out, *s)
	}
	return out
}

// SplitImageRef splits "ghcr.io/owner/name:1.2.3" into repo and tag, leaving a
// digest reference's tag empty. A colon in the registry's host:port is not a
// tag separator, hence the last-slash check.
func SplitImageRef(ref string) (repo, tag string) {
	if i := strings.LastIndex(ref, "@"); i > 0 {
		return ref[:i], ""
	}
	i := strings.LastIndex(ref, ":")
	if i < 0 || i < strings.LastIndex(ref, "/") {
		return ref, "latest"
	}
	return ref[:i], ref[i+1:]
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
