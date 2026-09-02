package engine

import (
	"context"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// DockerNetwork is one Docker network on THIS node the managed Traefik could
// join (GET /gateway/docker-networks — feeds the config card's picker).
type DockerNetwork struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
	// Own marks the node-stats stack's own default network, which the Traefik
	// service is always on — listed for completeness, not selectable.
	Own bool `json:"own"`
	// Containers is how many containers are attached (a hint which networks
	// carry the apps one wants to publish).
	Containers int `json:"containers"`
}

// DockerNetworks lists the joinable bridge/overlay networks of this node's
// Docker daemon (host/none/ingress and the stack's own network are marked or
// dropped). Empty with no error when the daemon is unreachable — the UI then
// falls back to the free-text field.
func DockerNetworks(ctx context.Context) ([]DockerNetwork, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	list, err := cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, err
	}
	project := strings.TrimSpace(os.Getenv("NODE_STATS_PROJECT"))
	if project == "" {
		project = "node-stats"
	}
	own := project + "_default"
	out := make([]DockerNetwork, 0, len(list))
	for _, n := range list {
		switch n.Driver {
		case "host", "null":
			continue
		}
		if n.Name == "none" || n.Name == "host" || n.Name == "ingress" {
			continue
		}
		dn := DockerNetwork{Name: n.Name, Driver: n.Driver, Own: n.Name == own}
		// NetworkList omits attached containers; one inspect per network is
		// cheap (a handful of networks) and makes the picker meaningful.
		if ins, err := cli.NetworkInspect(ctx, n.ID, network.InspectOptions{}); err == nil {
			dn.Containers = len(ins.Containers)
		}
		out = append(out, dn)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Own != out[j].Own {
			return !out[i].Own
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
