package docker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ComposeView is the read-only "composition" of an application: a synthetic
// compose-like document reconstructed from container metadata, plus the real
// compose file(s) when their host path is reachable from this process.
type ComposeView struct {
	Project       string   `json:"project"`
	Synthetic     string   `json:"synthetic"`
	Real          string   `json:"real,omitempty"`
	RealAvailable bool     `json:"real_available"`
	ConfigFiles   []string `json:"config_files,omitempty"`
}

// BuildSyntheticCompose reconstructs a compose-like YAML from the application's
// containers (image, container name, published ports, user labels). It is NOT
// the original file — env values, volumes and networks are not collected.
func BuildSyntheticCompose(app *DockerApplication) string {
	if app == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Reconstructed from running container metadata — read-only.\n")
	b.WriteString("# Not the original compose file: env values, volumes and networks are omitted.\n")
	fmt.Fprintf(&b, "name: %s\n", app.Project)
	b.WriteString("services:\n")

	containers := append([]DockerContainer(nil), app.Containers...)
	sort.Slice(containers, func(i, j int) bool {
		if serviceKey(containers[i]) != serviceKey(containers[j]) {
			return serviceKey(containers[i]) < serviceKey(containers[j])
		}
		return containers[i].Name < containers[j].Name
	})

	// Disambiguate replicas / task-attempts that share one service name so the
	// reconstructed document never emits duplicate service keys.
	seen := map[string]int{}
	for _, c := range containers {
		key := serviceKey(c)
		seen[key]++
		if seen[key] > 1 {
			key = fmt.Sprintf("%s-%d", key, seen[key])
		}
		fmt.Fprintf(&b, "  %s:\n", key)
		fmt.Fprintf(&b, "    image: %s\n", yamlValue(c.Image))
		fmt.Fprintf(&b, "    container_name: %s\n", yamlValue(c.Name))
		if c.State != "" {
			fmt.Fprintf(&b, "    # state: %s\n", c.State)
		}

		published := publishedPorts(c.Ports)
		if len(published) > 0 {
			b.WriteString("    ports:\n")
			for _, p := range published {
				fmt.Fprintf(&b, "      - \"%s\"\n", p)
			}
		}

		if len(c.Mounts) > 0 {
			b.WriteString("    volumes:\n")
			for _, m := range c.Mounts {
				if m.Type == "tmpfs" {
					continue
				}
				src := m.Source
				if m.Name != "" {
					src = m.Name
				}
				ro := ""
				if !m.RW {
					ro = ":ro"
				}
				fmt.Fprintf(&b, "      - \"%s:%s%s\"\n", src, m.Destination, ro)
			}
		}

		userLabels := userLabelKeys(c.Labels)
		if len(userLabels) > 0 {
			b.WriteString("    labels:\n")
			for _, k := range userLabels {
				fmt.Fprintf(&b, "      %s: %s\n", k, yamlValue(c.Labels[k]))
			}
		}
	}

	return b.String()
}

// ReadRealCompose attempts to read the real compose file(s) referenced by the
// application's containers. Only the paths recorded in the compose labels are
// read (never arbitrary input), and only when reachable from this process.
func ReadRealCompose(app *DockerApplication) (content string, files []string, available bool) {
	if app == nil {
		return "", nil, false
	}

	seen := map[string]struct{}{}
	var paths []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	for _, c := range app.Containers {
		// Authoritative source: the compose project's config_files label, set by
		// every `docker compose` deployment (Dokploy, Portainer, plain compose).
		if cfgs := splitConfigFiles(c.ComposeConfigFiles); len(cfgs) > 0 {
			for _, p := range cfgs {
				add(p)
			}
			continue
		}
		// Fallback (universal, label-driven — no orchestrator-specific paths): the
		// canonical compose file inside the project working_dir label, for the rare
		// deployment that sets working_dir but not config_files.
		if wd := strings.TrimSpace(c.ComposeWorkingDir); wd != "" {
			for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yaml", "compose.yml"} {
				add(filepath.Join(wd, name))
			}
		}
	}
	sort.Strings(paths)

	hostRoot := hostComposeRoot()
	var b strings.Builder
	for _, p := range paths {
		data, readPath, ok := readComposeFile(p, hostRoot)
		if !ok {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "# ── %s ──\n", readPath)
		b.Write(data)
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
		available = true
	}

	return b.String(), paths, available
}

// readComposeFile reads a compose file by its host path, trying the path
// directly first (native install, or an identity bind-mount like the controller
// uses) and then under the HOST_ROOT bind-mount prefix — so a containerised
// node-stats with the host root mounted at /host can still read an orchestrator's
// compose files (e.g. Dokploy's /etc/dokploy/compose/<project>/docker-compose.yml).
// Returns the bytes and the path that actually resolved.
func readComposeFile(p, hostRoot string) (data []byte, readPath string, ok bool) {
	if b, err := os.ReadFile(p); err == nil { // #nosec G304 — path comes from the daemon's compose labels, not user input
		return b, p, true
	}
	if hostRoot != "" && filepath.IsAbs(p) {
		hp := filepath.Join(hostRoot, p)
		if b, err := os.ReadFile(hp); err == nil { // #nosec G304 — derived from compose labels under the host bind-mount
			return b, hp, true
		}
	}
	return nil, "", false
}

// hostComposeRoot returns the host root bind-mount (HOST_ROOT, or /host when
// present) used to resolve absolute compose paths from inside a container.
func hostComposeRoot() string {
	if r := strings.TrimSpace(os.Getenv("HOST_ROOT")); r != "" {
		return filepath.Clean(r)
	}
	if st, err := os.Stat("/host"); err == nil && st.IsDir() {
		return "/host"
	}
	return ""
}

func serviceKey(c DockerContainer) string {
	if c.Service != "" {
		return c.Service
	}
	return c.Name
}

func publishedPorts(ports []DockerPort) []string {
	var out []string
	for _, p := range ports {
		if p.PublicPort == 0 {
			continue
		}
		typ := p.Type
		if typ == "" {
			typ = "tcp"
		}
		out = append(out, fmt.Sprintf("%d:%d/%s", p.PublicPort, p.PrivatePort, typ))
	}
	return out
}

// userLabelKeys returns sorted label keys excluding Docker/orchestrator-internal
// namespaces, so the synthetic compose shows meaningful user labels (traefik,
// homepage, etc.).
func userLabelKeys(labels map[string]string) []string {
	if labels == nil {
		return nil
	}
	var keys []string
	for k := range labels {
		if strings.HasPrefix(k, "com.docker.") ||
			strings.HasPrefix(k, "org.opencontainers.") ||
			strings.HasPrefix(k, "io.kubernetes.") ||
			strings.HasPrefix(k, "desktop.docker.") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func splitConfigFiles(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// yamlValue quotes a scalar when needed for safe YAML output.
func yamlValue(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#{}[],&*?|<>=!%@`\"'\n") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
	}
	return s
}
