package engine

import (
	"context"
	"net"

	"github.com/charmbracelet/log"

	docker "system-stats/internal/metrics/docker"
	"system-stats/internal/platform/gateway"
)

// NewProxyRouteSource adapts the replicated gateway route table into the docker
// collector's proxy-route enrichment, so Applications cards on EVERY node show
// the gateway's public URLs for their containers — the rendered Traefik file
// lives only on the gateway node, but the routes table is replicated.
func NewProxyRouteSource(logger *log.Logger, repo gateway.Repository, cfgStore ConfigStore) docker.ProxyRouteSource {
	return func(ctx context.Context) []docker.ProxyRoute {
		cfg, err := LoadConfig(ctx, cfgStore)
		if err != nil || !cfg.Enabled {
			return nil
		}
		rows, err := repo.List(ctx)
		if err != nil {
			if logger != nil {
				logger.Debug("gateway: proxy route source", "error", err)
			}
			return nil
		}
		httpPort, httpsPort := cfg.HTTPPort, cfg.HTTPSPort
		if cfg.Mode != gateway.ModeManaged {
			httpPort, httpsPort = 0, 0 // external proxy: assume defaults
		}
		var out []docker.ProxyRoute
		for _, r := range rows {
			// Passthrough / wildcard routes don't map to one container.
			if !r.Enabled || r.IsPassthrough() || r.IsWildcard() {
				continue
			}
			pr := docker.ProxyRoute{
				Host:         r.Domain,
				Scheme:       "http",
				UpstreamHost: r.TargetHost,
				UpstreamPort: r.TargetPort,
				UpstreamIsIP: net.ParseIP(r.TargetHost) != nil,
				PublicPort:   httpPort,
			}
			if r.TLS {
				pr.Scheme, pr.PublicPort = "https", httpsPort
			}
			out = append(out, pr)
		}
		return out
	}
}
