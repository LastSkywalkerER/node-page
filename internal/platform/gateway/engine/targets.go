package engine

import (
	"context"
	"sort"
	"strings"

	hosts "system-stats/internal/cluster/hosts"
	docker "system-stats/internal/metrics/docker"
)

// dockerTargetSource derives target suggestions from every host's docker
// applications: each container port published on the host is reachable from
// the gateway node as <host ipv4>:<public port>.
type dockerTargetSource struct {
	docker docker.Service
	hosts  hosts.Repository
}

// NewDockerTargetSource adapts the docker service + host registry.
func NewDockerTargetSource(d docker.Service, h hosts.Repository) TargetSource {
	return &dockerTargetSource{docker: d, hosts: h}
}

func (s *dockerTargetSource) Targets(ctx context.Context) ([]Target, error) {
	all, err := s.hosts.GetAllHosts(ctx)
	if err != nil {
		return nil, err
	}
	var out []Target
	for _, h := range all {
		if h.IPv4 == "" {
			continue
		}
		apps, err := s.docker.GetApplicationsByHost(ctx, h.ID)
		if err != nil {
			continue
		}
		name := h.Name
		if h.DisplayName != "" {
			name = h.DisplayName
		}
		seen := map[int]bool{}
		for _, app := range apps {
			appName := app.DisplayName
			if appName == "" {
				appName = app.Project
			}
			for _, c := range app.Containers {
				for _, p := range c.Ports {
					if p.PublicPort <= 0 || seen[p.PublicPort] {
						continue
					}
					// Only IPv4/any bindings are reachable via the host's IPv4.
					if p.IP != "" && p.IP != "0.0.0.0" && strings.Contains(p.IP, ":") {
						continue
					}
					if strings.EqualFold(p.Type, "udp") {
						continue
					}
					seen[p.PublicPort] = true
					out = append(out, Target{
						HostID:      h.ID,
						HostName:    name,
						HostMAC:     strings.ToLower(h.MacAddress),
						IPv4:        h.IPv4,
						App:         appName,
						Container:   strings.TrimPrefix(c.Name, "/"),
						Port:        p.PublicPort,
						PrivatePort: p.PrivatePort,
						Image:       c.Image,
					})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HostName != out[j].HostName {
			return out[i].HostName < out[j].HostName
		}
		if out[i].App != out[j].App {
			return out[i].App < out[j].App
		}
		return out[i].Port < out[j].Port
	})
	return out, nil
}
