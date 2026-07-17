// Package web embeds the SwartzNet web UI assets served by httpapi. The file
// layout — index.html at the root plus a static/ tree — is the frozen
// contract; the page content grows slice by slice.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html static/*
var rawAssets embed.FS

// Assets returns the embedded web UI file tree.
func Assets() fs.FS { return rawAssets }
