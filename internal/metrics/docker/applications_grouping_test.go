package docker

import (
	"os"
	"sort"
	"testing"
)

// metricWith builds a DockerMetric from a list of container Project keys (one
// running container each) for grouping tests.
func metricWith(projects ...string) *DockerMetric {
	st := DockerStack{Name: "all"}
	for i, p := range projects {
		st.Containers = append(st.Containers, DockerContainer{
			ID: string(rune('a' + i)), Name: p, Project: p, State: "running",
		})
	}
	return &DockerMetric{Stacks: []DockerStack{st}, DockerAvailable: true}
}

func appByProject(apps []DockerApplication) map[string]DockerApplication {
	m := map[string]DockerApplication{}
	for _, a := range apps {
		m[a.Project] = a
	}
	return m
}

func TestRegroupByCommonPrefix_Dokploy(t *testing.T) {
	os.Unsetenv("NODE_STATS_APP_PREFIX_GROUPING") // default = on
	apps := BuildApplications(metricWith(
		"node-stats-app-zwgbyv", "node-stats-compose-vrlqtf", "node-stats-db-hfndza",
		"ebcenter-app-yxkjot", "ebcenter-db-9zdnjd",
		"dokploy", "dokploy-postgres", "dokploy-redis", "dokploy-traefik",
		"mdata-db-tjmnjq",
		"docs-templater-frontend-3p-app-aaaaaa", "docs-templater-frontend-w-app-bbbbbb",
	))

	got := appByProject(apps)
	want := map[string]int{
		"node-stats":              3, // app + compose + db merged
		"ebcenter":                2,
		"dokploy":                 4, // dokploy + postgres + redis + traefik
		"docs-templater-frontend": 2,
		"mdata-db-tjmnjq":         1, // no sibling → stays a singleton, untouched
	}

	if len(apps) != len(want) {
		keys := make([]string, 0, len(apps))
		for _, a := range apps {
			keys = append(keys, a.Project)
		}
		sort.Strings(keys)
		t.Fatalf("got %d apps %v, want %d", len(apps), keys, len(want))
	}
	for proj, n := range want {
		a, ok := got[proj]
		if !ok {
			t.Errorf("missing merged app %q", proj)
			continue
		}
		if a.TotalContainers != n {
			t.Errorf("app %q: TotalContainers=%d, want %d", proj, a.TotalContainers, n)
		}
		if n > 1 && a.IsSingleton {
			t.Errorf("app %q merged %d containers but IsSingleton=true", proj, n)
		}
	}
	if got["mdata-db-tjmnjq"].IsSingleton != true {
		t.Errorf("mdata-db-tjmnjq should remain a singleton")
	}
}

func TestRegroupByCommonPrefix_Disabled(t *testing.T) {
	os.Setenv("NODE_STATS_APP_PREFIX_GROUPING", "off")
	defer os.Unsetenv("NODE_STATS_APP_PREFIX_GROUPING")
	apps := BuildApplications(metricWith("node-stats-app-x", "node-stats-db-y"))
	if len(apps) != 2 {
		t.Fatalf("disabled grouping should keep 2 apps, got %d", len(apps))
	}
}

func TestRegroupByCommonPrefix_NoSharedPrefix(t *testing.T) {
	os.Unsetenv("NODE_STATS_APP_PREFIX_GROUPING")
	// Distinct compose projects with no shared prefix must not merge.
	apps := BuildApplications(metricWith("grafana", "prometheus", "postgres"))
	if len(apps) != 3 {
		t.Fatalf("unrelated apps must not merge, got %d", len(apps))
	}
}
