// Package web holds the built frontend assets that are embedded into the binary.
package web

import (
	"embed"
	"io/fs"
)

// dist is the Vite build output. It always exists (at least a .gitkeep), so the
// binary compiles before the frontend has been built.
//
//go:embed all:dist
var dist embed.FS

// Dist returns the embedded build output rooted at web/dist.
func Dist() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
