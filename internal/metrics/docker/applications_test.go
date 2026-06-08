package docker

import (
	"strings"
	"testing"
)

// mkContainer builds a container the way the collector does: the grouping key
// (Project) and Service are resolved from labels at collection time.
func mkContainer(name string, state string, labels map[string]string) DockerContainer {
	project, service, _ := resolveProject(labels, name)
	return DockerContainer{
		ID:      name,
		Name:    name,
		State:   state,
		Labels:  labels,
		Project: project,
		Service: service,
	}
}

func TestResolveProject(t *testing.T) {
	tests := []struct {
		name          string
		labels        map[string]string
		container     string
		wantProject   string
		wantService   string
		wantSingleton bool
	}{
		{
			name:        "compose project",
			labels:      map[string]string{"com.docker.compose.project": "board-plane", "com.docker.compose.service": "admin"},
			container:   "board-plane-admin-1",
			wantProject: "board-plane", wantService: "admin", wantSingleton: false,
		},
		{
			name:        "swarm stack trims namespace prefix from service",
			labels:      map[string]string{"com.docker.stack.namespace": "mystack", "com.docker.swarm.service.name": "mystack_web"},
			container:   "mystack_web.1.abc",
			wantProject: "mystack", wantService: "web", wantSingleton: false,
		},
		{
			name:        "standalone swarm service groups by service name",
			labels:      map[string]string{"com.docker.swarm.service.name": "ammocrypt-db-gslw8g"},
			container:   "ammocrypt-db-gslw8g.1.qkjraqus24ww4ncug39g7ii09",
			wantProject: "ammocrypt-db-gslw8g", wantService: "ammocrypt-db-gslw8g", wantSingleton: false,
		},
		{
			name:        "plain standalone container is its own singleton",
			labels:      nil,
			container:   "dokploy-traefik",
			wantProject: "dokploy-traefik", wantService: "", wantSingleton: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProject, gotService, gotSingleton := resolveProject(tt.labels, tt.container)
			if gotProject != tt.wantProject || gotService != tt.wantService || gotSingleton != tt.wantSingleton {
				t.Fatalf("resolveProject(%v, %q) = (%q, %q, %v); want (%q, %q, %v)",
					tt.labels, tt.container, gotProject, gotService, gotSingleton,
					tt.wantProject, tt.wantService, tt.wantSingleton)
			}
		})
	}
}

// findApp returns the application with the given project key, or nil.
func findApp(apps []DockerApplication, project string) *DockerApplication {
	for i := range apps {
		if apps[i].Project == project {
			return &apps[i]
		}
	}
	return nil
}

func TestBuildApplications_GroupsSwarmServiceReplicas(t *testing.T) {
	// Two task-attempts of one standalone swarm service (dokploy) must collapse
	// into a single application — not two singletons.
	svc := map[string]string{"com.docker.swarm.service.name": "app-navigate-bus"}
	m := &DockerMetric{Stacks: []DockerStack{{Containers: []DockerContainer{
		mkContainer("app-navigate-bus.1.aaa", "running", svc),
		mkContainer("app-navigate-bus.1.bbb", "exited", svc),
	}}}}

	apps := BuildApplications(m)
	if len(apps) != 1 {
		t.Fatalf("got %d applications, want 1: %+v", len(apps), apps)
	}
	app := apps[0]
	if app.Project != "app-navigate-bus" {
		t.Fatalf("project = %q, want app-navigate-bus", app.Project)
	}
	if app.IsSingleton {
		t.Fatalf("swarm service should not be a singleton")
	}
	if app.TotalContainers != 2 || app.RunningContainers != 1 {
		t.Fatalf("containers = %d total / %d running, want 2/1", app.TotalContainers, app.RunningContainers)
	}
	if app.ServiceCount != 1 {
		t.Fatalf("service count = %d, want 1 (one service, two replicas)", app.ServiceCount)
	}
}

func TestBuildApplications_Mixed(t *testing.T) {
	compose := func(svc string) map[string]string {
		return map[string]string{"com.docker.compose.project": "board-plane", "com.docker.compose.service": svc}
	}
	m := &DockerMetric{Stacks: []DockerStack{{Containers: []DockerContainer{
		// compose project with two services
		mkContainer("board-plane-admin-1", "running", compose("admin")),
		mkContainer("board-plane-api-1", "running", compose("api")),
		// standalone swarm service
		mkContainer("redis-x.1.aaa", "running", map[string]string{"com.docker.swarm.service.name": "redis-x"}),
		// plain standalone
		mkContainer("dokploy-traefik", "running", nil),
		// one-off `compose run` must be skipped entirely
		mkContainer("board-plane-migrate-run-1", "exited", map[string]string{
			"com.docker.compose.project": "board-plane",
			"com.docker.compose.service": "migrate",
			"com.docker.compose.oneoff":  "True",
		}),
	}}}}

	apps := BuildApplications(m)
	if len(apps) != 3 {
		t.Fatalf("got %d applications, want 3: %+v", len(apps), apps)
	}

	bp := findApp(apps, "board-plane")
	if bp == nil {
		t.Fatal("missing board-plane application")
	}
	if bp.IsSingleton || bp.TotalContainers != 2 || bp.ServiceCount != 2 {
		t.Fatalf("board-plane = singleton:%v total:%d services:%d, want false/2/2", bp.IsSingleton, bp.TotalContainers, bp.ServiceCount)
	}

	redis := findApp(apps, "redis-x")
	if redis == nil || redis.IsSingleton {
		t.Fatalf("redis-x should be a non-singleton swarm service: %+v", redis)
	}

	traefik := findApp(apps, "dokploy-traefik")
	if traefik == nil || !traefik.IsSingleton {
		t.Fatalf("dokploy-traefik should be a singleton: %+v", traefik)
	}

	if findApp(apps, "board-plane-migrate-run-1") != nil {
		t.Fatal("one-off compose run container must not form an application")
	}
}

func TestBuildSyntheticCompose_DisambiguatesReplicas(t *testing.T) {
	svc := map[string]string{"com.docker.swarm.service.name": "web"}
	app := &DockerApplication{
		Project: "web",
		Containers: []DockerContainer{
			mkContainer("web.1.aaa", "running", svc),
			mkContainer("web.1.bbb", "running", svc),
		},
	}
	out := BuildSyntheticCompose(app)
	// Both replicas share service "web"; the second must be disambiguated so the
	// YAML has no duplicate service key.
	if !strings.Contains(out, "\n  web:\n") {
		t.Fatalf("expected a 'web' service block:\n%s", out)
	}
	if !strings.Contains(out, "\n  web-2:\n") {
		t.Fatalf("expected a disambiguated 'web-2' service block:\n%s", out)
	}
}
