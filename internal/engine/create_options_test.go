package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestCreateTorrentRespectsNameOverride covers the
// opts.Name != "" branch of CreateTorrent. The default name
// would be filepath.Base(srcPath); when Name is supplied, that
// override wins.
func TestCreateTorrentRespectsNameOverride(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "raw.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}

	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{
		Root: srcPath,
		Name: "custom-display-name",
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "custom-display-name" {
		t.Errorf("Name = %q, want \"custom-display-name\"", info.Name)
	}
}

// TestCreateTorrentRespectsCreatedByOverride covers the
// opts.CreatedBy != "" branch. The default is "SwartzNet"; when
// supplied it wins.
func TestCreateTorrentRespectsCreatedByOverride(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "raw.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatal(err)
	}

	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{
		Root:      srcPath,
		CreatedBy: "my-bespoke-tool",
	})
	if err != nil {
		t.Fatal(err)
	}
	if mi.CreatedBy != "my-bespoke-tool" {
		t.Errorf("CreatedBy = %q, want \"my-bespoke-tool\"", mi.CreatedBy)
	}
}
