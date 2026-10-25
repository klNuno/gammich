// Package staticfs embeds the built SvelteKit SPA.
//
// The "dist" directory is populated by the build pipeline:
//
//	pnpm --dir web build && cp -r web/build/. internal/staticfs/dist/
//
// A placeholder index.html ships with the repo so `go build` always works.
package staticfs

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil
	}
	return sub
}
