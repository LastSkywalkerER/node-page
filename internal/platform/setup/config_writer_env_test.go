package setup

import (
	"os"
	"path/filepath"
	"testing"
)

// NODE_STATS_ENV_FILE relocates the runtime .env (dokploy-style redeploys
// wipe the compose project dir) — reads and writes must follow it, and the
// parent directory must be created on first write.
func TestConfigWriterHonorsEnvFileOverride(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "data", ".env")
	t.Setenv("NODE_STATS_ENV_FILE", target)

	cw := NewConfigWriter()
	if cw.GetConfigPath() != target {
		t.Fatalf("envPath = %q, want %q", cw.GetConfigPath(), target)
	}

	cv, err := cw.ReadCurrentConfig()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	cv.JWTSecret = "test-secret-jwt"
	cv.RefreshSecret = "test-secret-refresh"
	if err := cw.WriteConfigFile(cv); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("env file not written at override path: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("env file empty")
	}
}
