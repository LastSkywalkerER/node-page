package appbackup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A restore deletes the application's current data before putting the snapshot
// back, which makes this the most destructive code in the project. Every path
// therefore passes a guard that fails closed: anything it does not recognise as
// clearly an application data directory is refused rather than emptied.

// systemRoots can never be wiped, nor can anything be wiped that would take one
// of them with it.
var systemRoots = []string{
	"/", "/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/lib32", "/lib64",
	"/libx32", "/media", "/mnt", "/opt", "/proc", "/root", "/run", "/sbin",
	"/srv", "/sys", "/tmp", "/usr", "/var",
}

// CheckWipable reports why a path must not be emptied, or nil when it may be.
func CheckWipable(p string) error {
	if p == "" {
		return fmt.Errorf("empty path")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%q is not absolute", p)
	}
	clean := filepath.Clean(p)
	if clean != p && clean+"/" != p {
		// A path that does not survive Clean carries "..", a double slash or a
		// trailing oddity — never trust it with a recursive delete.
		return fmt.Errorf("%q is not a clean path (resolved to %q)", p, clean)
	}
	for _, root := range systemRoots {
		if clean == root {
			return fmt.Errorf("%q is a system directory", clean)
		}
	}
	// Depth: /a is one segment, /a/b is two. A single segment under / is either
	// a system root (caught above) or a top-level mount like /DATA whose whole
	// contents are far more than one application's data.
	if len(strings.Split(strings.Trim(clean, "/"), "/")) < 2 {
		return fmt.Errorf("%q is a top-level directory; refusing to wipe a whole mount", clean)
	}
	if strings.Contains(clean, "..") {
		return fmt.Errorf("%q contains ..", clean)
	}
	return nil
}

// WipeContents empties a directory without removing the directory itself, so a
// docker named volume keeps existing and a bind mount keeps its inode, owner
// and mode. Missing directories are not an error: there is simply nothing to
// clear before the restore writes.
func WipeContents(p string) error {
	if err := CheckWipable(p); err != nil {
		return err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(p, e.Name())); err != nil {
			return fmt.Errorf("remove %s: %w", filepath.Join(p, e.Name()), err)
		}
	}
	return nil
}
