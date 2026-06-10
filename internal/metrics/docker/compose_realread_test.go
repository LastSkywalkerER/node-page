package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadRealCompose_LabelAndWorkingDir verifies the universal compose-file
// resolution: the config_files label is authoritative, and working_dir is a
// label-driven fallback (no orchestrator-specific path hardcoding).
func TestReadRealCompose_LabelAndWorkingDir(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yml")
	body := "services:\n  app:\n    image: nginx:alpine\n"
	if err := os.WriteFile(composePath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. config_files label points straight at the file.
	app := &DockerApplication{Containers: []DockerContainer{{ComposeConfigFiles: composePath}}}
	content, files, ok := ReadRealCompose(app)
	if !ok || !strings.Contains(content, "nginx:alpine") {
		t.Fatalf("config_files path not read: ok=%v content=%q", ok, content)
	}
	if len(files) != 1 || files[0] != composePath {
		t.Fatalf("unexpected files: %v", files)
	}

	// 2. Only working_dir set → resolves the canonical compose filename within it.
	app2 := &DockerApplication{Containers: []DockerContainer{{ComposeWorkingDir: dir}}}
	content2, _, ok2 := ReadRealCompose(app2)
	if !ok2 || !strings.Contains(content2, "nginx:alpine") {
		t.Fatalf("working_dir fallback not read: ok=%v content=%q", ok2, content2)
	}

	// 3. Neither label → nothing to read (no guessing).
	if _, _, ok3 := ReadRealCompose(&DockerApplication{Containers: []DockerContainer{{Name: "x"}}}); ok3 {
		t.Fatal("expected no real compose when no compose labels are present")
	}
}
