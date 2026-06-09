package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestContainerMatchScore(t *testing.T) {
	swarm := container.Summary{
		Names:  []string{"/docs-templater-backend-vjqn9z.1.NEWTASK"},
		Labels: map[string]string{"com.docker.swarm.service.name": "docs-templater-backend-vjqn9z"},
	}
	compose := container.Summary{
		Names:  []string{"/board-plane-mvyzj3-api-1"},
		Labels: map[string]string{"com.docker.compose.project": "board-plane-mvyzj3", "com.docker.compose.service": "api"},
	}

	// Stale stored name (old task id) must NOT match by name, but the stable
	// swarm-service label must.
	ref := ContainerLogRef{
		Name:         "docs-templater-backend-vjqn9z.1.OLDTASK",
		SwarmService: "docs-templater-backend-vjqn9z",
		Project:      "docs-templater-backend-vjqn9z",
		Service:      "docs-templater-backend-vjqn9z",
	}
	if s := containerMatchScore(swarm, ref); s != 4 {
		t.Errorf("swarm-service match score = %d, want 4 (label match, name is stale)", s)
	}

	// Exact name match is strongest.
	if s := containerMatchScore(compose, ContainerLogRef{Name: "board-plane-mvyzj3-api-1"}); s != 5 {
		t.Errorf("exact name score = %d, want 5", s)
	}
	// Compose project+service match.
	if s := containerMatchScore(compose, ContainerLogRef{Project: "board-plane-mvyzj3", Service: "api"}); s != 4 {
		t.Errorf("compose match score = %d, want 4", s)
	}
	// No identity → no match.
	if s := containerMatchScore(compose, ContainerLogRef{Project: "other", Service: "api"}); s != 0 {
		t.Errorf("non-match score = %d, want 0", s)
	}
}
