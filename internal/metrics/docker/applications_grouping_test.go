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

// Real dokploy case: a swarm "project" deployed as several standalone services
// (no compose/stack label), where some services run as multiple instances with
// distinct random suffixes (backend×2, frontend×2). The duplicate instances
// must not split the project — everything collapses under "docs-templater" — and
// an unrelated compose app on the same host must stay separate.
func TestRegroupByCommonPrefix_SwarmProjectWithDuplicateServices(t *testing.T) {
	os.Unsetenv("NODE_STATS_APP_PREFIX_GROUPING")
	apps := BuildApplications(metricWith(
		"docs-templater-db-otgog7", "docs-templater-docsdb-i9fu69",
		"docs-templater-backend-vjqn9z", "docs-templater-backend-ncmwi4",
		"docs-templater-frontend-wwfhqs", "docs-templater-frontend-3pokhx",
		"board-plane-mvyzj3", // unrelated compose app on the same host
	))

	got := appByProject(apps)
	dt, ok := got["docs-templater"]
	if !ok {
		keys := make([]string, 0, len(apps))
		for _, a := range apps {
			keys = append(keys, a.Project)
		}
		sort.Strings(keys)
		t.Fatalf("expected a merged 'docs-templater' app, got %v", keys)
	}
	if dt.TotalContainers != 6 {
		t.Errorf("docs-templater TotalContainers=%d, want 6 (db+docsdb+backend×2+frontend×2)", dt.TotalContainers)
	}
	if dt.IsSingleton {
		t.Errorf("docs-templater merged 6 services but IsSingleton=true")
	}
	if _, ok := got["board-plane-mvyzj3"]; !ok {
		t.Errorf("unrelated board-plane-mvyzj3 must stay its own app")
	}
	if len(apps) != 2 {
		t.Errorf("got %d apps, want 2 (docs-templater + board-plane-mvyzj3)", len(apps))
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

func TestMergeStacksByCommonPrefix(t *testing.T) {
	os.Unsetenv("NODE_STATS_APP_PREFIX_GROUPING")
	c := &dockerMetricsCollector{}
	mk := func(name string) *DockerStack {
		return &DockerStack{Name: name, Containers: []DockerContainer{{ID: name, Name: name}}, TotalContainers: 1, RunningContainers: 1}
	}
	in := map[string]*DockerStack{}
	for _, n := range []string{
		"node-stats-app-zwgbyv", "node-stats-db-hfndza", "node-stats-compose-vrlqtf",
		"ebcenter-app-yxkjot", "ebcenter-db-9zdnjd",
		"mdata-db-tjmnjq",
	} {
		in[n] = mk(n)
	}

	out := c.mergeStacksByCommonPrefix(in)

	if len(out) != 3 {
		keys := make([]string, 0, len(out))
		for k := range out {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("got %d stacks %v, want 3 (node-stats, ebcenter, mdata-db-tjmnjq)", len(out), keys)
	}
	if out["node-stats"] == nil || out["node-stats"].TotalContainers != 3 {
		t.Errorf("node-stats stack should merge 3 containers, got %+v", out["node-stats"])
	}
	if out["ebcenter"] == nil || out["ebcenter"].TotalContainers != 2 {
		t.Errorf("ebcenter stack should merge 2 containers, got %+v", out["ebcenter"])
	}
	if out["mdata-db-tjmnjq"] == nil {
		t.Errorf("mdata-db-tjmnjq has no sibling → must stay its own stack")
	}
	// No container is duplicated across groups (the old matcher could double-add).
	seen := map[string]int{}
	for _, s := range out {
		for _, ct := range s.Containers {
			seen[ct.ID]++
		}
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("container %q appears %d times across merged stacks (want 1)", id, n)
		}
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
