package web

import (
	"io/fs"
	"testing"
)

// TestAssetsContainsEveryModule guards the //go:embed glob: the web client is a
// tree of ES modules under static/ (including static/pages/), and a glob that
// silently drops a nested file would ship a broken SPA that only fails in a
// browser. Assert every asset the client loads is present in the embedded FS.
func TestAssetsContainsEveryModule(t *testing.T) {
	want := []string{
		"index.html",
		"static/style.css",
		"static/app.js",
		"static/api.js",
		"static/util.js",
		"static/pages/downloads.js",
		"static/pages/search.js",
		"static/pages/status.js",
		"static/pages/companion.js",
		"static/pages/settings.js",
	}
	assets := Assets()
	for _, name := range want {
		f, err := assets.Open(name)
		if err != nil {
			t.Errorf("embedded asset %q missing: %v", name, err)
			continue
		}
		info, err := fs.Stat(assets, name)
		if err == nil && info.Size() == 0 {
			t.Errorf("embedded asset %q is empty", name)
		}
		f.Close()
	}
}

// TestIndexHtmlLoadsModuleEntry catches a shell that forgot to load the app
// entry point (the SPA would render a blank page).
func TestIndexHtmlLoadsModuleEntry(t *testing.T) {
	b, err := fs.ReadFile(Assets(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, want := range []string{`type="module"`, `/static/app.js`, `id="view"`, `id="tabs"`} {
		if !contains(html, want) {
			t.Errorf("index.html missing %q", want)
		}
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
