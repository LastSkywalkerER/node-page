//go:build embed_dist

// Package webui serves the built React frontend. With the `embed_dist` build
// tag the compiled `dist/` is baked into the binary (self-contained release);
// without it, see noembed.go (the server falls back to on-disk dist for dev).
//
// Building with `-tags embed_dist` requires `internal/webui/dist/` to exist
// (run the frontend build first — scripts/build and the CI workflows do this).
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distRoot embed.FS

// FS returns the embedded dist directory rooted so "index.html" and
// "assets/..." resolve directly, or nil if the sub-tree is unexpectedly absent.
func FS() fs.FS {
	sub, err := fs.Sub(distRoot, "dist")
	if err != nil {
		return nil
	}
	return sub
}
