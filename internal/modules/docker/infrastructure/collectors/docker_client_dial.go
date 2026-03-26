package collectors

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	"github.com/charmbracelet/log"
	"github.com/docker/docker/client"
)

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
