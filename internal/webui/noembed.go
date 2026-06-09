//go:build !embed_dist

// Package webui serves the built React frontend. This stub is compiled for dev
// builds (`go build`/air, no `embed_dist` tag) where no embedded frontend is
// present — the server serves from on-disk internal/webui/dist instead (or the
// Vite dev server handles the SPA during development).
package webui

import "io/fs"

// FS returns nil in dev builds, signalling the server to use its on-disk fallback.
func FS() fs.FS { return nil }
