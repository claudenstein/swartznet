// Package web embeds SwartzNet's static web UI assets into the
// httpapi binary so the daemon can serve them without depending
// on a working directory or external file paths.
//
// The package only exists to host the //go:embed directive and
// expose the resulting fs.FS so internal/httpapi can register it
// against the http.ServeMux. The actual files (index.html,
// static/style.css, static/app.js) live alongside this Go file
// and are bundled at build time.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html static/*
var rawAssets embed.FS

// Assets returns the embedded asset file system, ready to hand
// to http.FileServer. Errors only on a programmer bug — the
// embed.FS root is determined at compile time.
func Assets() fs.FS {
	return rawAssets
}
