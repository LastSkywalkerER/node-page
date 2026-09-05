package appbackup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckWipableRefusesDangerousPaths(t *testing.T) {
	bad := []string{
		"", "relative/path", "/", "/etc", "/var", "/root", "/home", "/usr",
		"/DATA",               // top-level mount: far more than one app's data
		"/mnt",                // system root
		"/DATA/../etc",        // unclean
		"/DATA/AppData/../..", // climbs out
		"//DATA/AppData",      // double slash
	}
	for _, p := range bad {
		t.Run(p, func(t *testing.T) {
			if err := CheckWipable(p); err == nil {
				t.Fatalf("CheckWipable(%q) = nil, expected refusal", p)
			}
		})
	}
}

func TestCheckWipableAllowsApplicationData(t *testing.T) {
	ok := []string{
		"/DATA/AppData/affine",
		"/DATA/AppData/affine/postgres",
		"/root/affine",
		"/var/lib/docker/volumes/affine_storage/_data",
		"/opt/stacks/n8n",
	}
	for _, p := range ok {
		t.Run(p, func(t *testing.T) {
			if err := CheckWipable(p); err != nil {
				t.Fatalf("CheckWipable(%q) = %v, expected allowed", p, err)
			}
		})
	}
}

func TestWipeContentsEmptiesButKeepsDirectory(t *testing.T) {
	base := t.TempDir()
	// The guard requires >= 2 path segments; t.TempDir() is deep enough.
	target := filepath.Join(base, "data", "app")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sub", "b.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WipeContents(target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("target directory removed: %v", err)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory not empty: %v", entries)
	}
}

func TestWipeContentsMissingDirectoryIsNotAnError(t *testing.T) {
	if err := WipeContents(filepath.Join(t.TempDir(), "a", "absent")); err != nil {
		t.Fatalf("WipeContents on missing dir = %v, want nil", err)
	}
}
