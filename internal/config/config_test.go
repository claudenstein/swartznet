package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultNonEmpty(t *testing.T) {
	t.Parallel()
	c := Default()

	if c.DataDir == "" {
		t.Error("DataDir is empty")
	}
	if c.IndexDir == "" {
		t.Error("IndexDir is empty")
	}
	if c.IdentityPath == "" {
		t.Error("IdentityPath is empty")
	}
	if c.PublisherManifest == "" {
		t.Error("PublisherManifest is empty")
	}
	if c.ReputationPath == "" {
		t.Error("ReputationPath is empty")
	}
	if c.SeedListPath == "" {
		t.Error("SeedListPath is empty")
	}
	if c.BloomPath == "" {
		t.Error("BloomPath is empty")
	}
	if c.CompanionDir == "" {
		t.Error("CompanionDir is empty")
	}
	if c.CompanionFollowFile == "" {
		t.Error("CompanionFollowFile is empty")
	}
	if c.ListenPort != 42069 {
		t.Errorf("ListenPort = %d, want 42069", c.ListenPort)
	}
	if !c.Seed {
		t.Error("Seed should default to true")
	}
	if c.NoUpload {
		t.Error("NoUpload should default to false")
	}
	if c.DisableDHT {
		t.Error("DisableDHT should default to false")
	}
	if c.Regtest {
		t.Error("Regtest should default to false")
	}
}

func TestDefaultPathsShareRoot(t *testing.T) {
	t.Parallel()
	c := Default()
	// All persistent paths should share the same swartznet root directory.
	// DataDir, IndexDir, CompanionDir are directories (apply filepath.Dir);
	// the rest are file paths (also apply filepath.Dir to get their parent).
	root := filepath.Dir(c.DataDir)
	for _, p := range []string{
		filepath.Dir(c.IndexDir),
		filepath.Dir(c.IdentityPath),
		filepath.Dir(c.PublisherManifest),
		filepath.Dir(c.ReputationPath),
		filepath.Dir(c.SeedListPath),
		filepath.Dir(c.BloomPath),
		filepath.Dir(c.CompanionDir),
		filepath.Dir(c.CompanionFollowFile),
	} {
		if p != root {
			t.Errorf("path root %q != expected %q", p, root)
		}
	}
}

func TestValidateHappy(t *testing.T) {
	t.Parallel()
	c := Default()
	c.DataDir = t.TempDir()
	c.IndexDir = filepath.Join(t.TempDir(), "idx")
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateEmptyDataDir(t *testing.T) {
	t.Parallel()
	c := Default()
	c.DataDir = ""
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for empty DataDir")
	}
}

func TestValidatePortBounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		port int
		ok   bool
	}{
		{"zero", 0, true},
		{"low", 1, true},
		{"max", 65535, true},
		{"negative", -1, false},
		{"too high", 65536, false},
		{"way too high", 70000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.DataDir = t.TempDir()
			c.ListenPort = tc.port
			err := c.Validate()
			if tc.ok && err != nil {
				t.Fatalf("port %d should be valid: %v", tc.port, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("port %d should be invalid", tc.port)
			}
		})
	}
}

func TestValidateCreatesDataDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nested", "data")
	c := Default()
	c.DataDir = dir
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("DataDir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("DataDir is not a directory")
	}
}

func TestValidateCreatesIndexDirParent(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	c := Default()
	c.DataDir = filepath.Join(base, "data")
	c.IndexDir = filepath.Join(base, "nested", "index.bleve")
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// Parent of IndexDir should exist; the leaf (index.bleve) is Bleve's job.
	parent := filepath.Dir(c.IndexDir)
	if _, err := os.Stat(parent); err != nil {
		t.Fatalf("IndexDir parent not created: %v", err)
	}
}

func TestValidateEmptyIndexDir(t *testing.T) {
	t.Parallel()
	c := Default()
	c.DataDir = t.TempDir()
	c.IndexDir = "" // empty IndexDir should be OK (disabled)
	if err := c.Validate(); err != nil {
		t.Fatalf("empty IndexDir should be valid: %v", err)
	}
}

func TestSwartznetShareRootXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")
	got := swartznetShareRoot()
	want := "/tmp/xdg-test/swartznet"
	if got != want {
		t.Errorf("swartznetShareRoot() = %q, want %q", got, want)
	}
}

func TestSwartznetShareRootHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	got := swartznetShareRoot()
	if !strings.Contains(got, ".local/share/swartznet") {
		t.Errorf("swartznetShareRoot() = %q, want path containing .local/share/swartznet", got)
	}
}
