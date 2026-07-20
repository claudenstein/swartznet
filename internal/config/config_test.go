package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultNonEmpty(t *testing.T) {
	c := Default()
	if c.DataDir == "" || c.IndexDir == "" || c.IdentityPath == "" {
		t.Fatalf("Default() has empty paths: %+v", c)
	}
	if c.ListenPort != 42069 {
		t.Fatalf("ListenPort = %d, want 42069", c.ListenPort)
	}
	if filepath.Base(c.IdentityPath) != "identity.key" {
		t.Fatalf("IdentityPath leaf = %q, want identity.key", c.IdentityPath)
	}
}

func TestDefaultPathsShareRoot(t *testing.T) {
	c := Default()
	root := ResolveShareRoot()
	for _, p := range []string{c.DataDir, c.IndexDir, c.IdentityPath, c.TrustPath, c.BloomPath, c.ReputationPath, c.SeedListPath} {
		if !strings.HasPrefix(p, root) {
			t.Errorf("path %q does not share root %q", p, root)
		}
	}
}

// TestValidateNeverTouchesIdentity pins the Slice-0 §5 rule after the
// IdentityPath field landed: Validate's create-set stays exactly two dirs.
func TestValidateNeverTouchesIdentity(t *testing.T) {
	tmp := t.TempDir()
	c := Config{
		DataDir:      filepath.Join(tmp, "data"),
		IdentityPath: filepath.Join(tmp, "id", "identity.key"),
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "id")); !os.IsNotExist(err) {
		t.Fatal("Validate created the identity parent dir; the loader owns it")
	}
}

func TestResolveShareRoot(t *testing.T) {
	t.Run("xdg wins", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")
		if got := ResolveShareRoot(); got != "/tmp/xdg-test/swartznet" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("home fallback", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/tmp/home-test")
		got := ResolveShareRoot()
		if !strings.Contains(got, filepath.Join(".local", "share", "swartznet")) {
			t.Fatalf("got %q, want ~/.local/share/swartznet", got)
		}
	})
	t.Run("homeless last resort", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "")
		if got := ResolveShareRoot(); got != "./swartznet-state" {
			t.Fatalf("got %q, want ./swartznet-state", got)
		}
	})
}

func TestValidatePortBounds(t *testing.T) {
	tmp := t.TempDir()
	for _, tc := range []struct {
		port int
		ok   bool
	}{
		{0, true}, {1, true}, {65535, true},
		{-1, false}, {65536, false}, {70000, false},
	} {
		c := Config{DataDir: filepath.Join(tmp, "data"), ListenPort: tc.port}
		err := c.Validate()
		if tc.ok && err != nil {
			t.Errorf("port %d: unexpected error %v", tc.port, err)
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("port %d: want error", tc.port)
			} else if !strings.Contains(err.Error(), "out of range") {
				t.Errorf("port %d: error %q lacks 'out of range'", tc.port, err)
			}
		}
	}
}

func TestValidateEmptyDataDir(t *testing.T) {
	tmp := t.TempDir()
	c := Config{DataDir: "", IndexDir: filepath.Join(tmp, "sub", "index")}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "DataDir must not be empty") {
		t.Fatalf("err = %v", err)
	}
	// A rejected config must leave the filesystem untouched.
	if _, statErr := os.Stat(filepath.Join(tmp, "sub")); !os.IsNotExist(statErr) {
		t.Fatalf("rejected Validate created IndexDir parent")
	}
}

func TestValidateCreatesExactlyTwo(t *testing.T) {
	tmp := t.TempDir()
	c := Config{
		DataDir:  filepath.Join(tmp, "a", "data"),
		IndexDir: filepath.Join(tmp, "b", "idx", "leaf"),
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(c.DataDir)
	if err != nil || !st.IsDir() {
		t.Fatalf("DataDir not created: %v", err)
	}
	if st.Mode().Perm()&^0o755 != 0 {
		t.Errorf("DataDir mode %v exceeds 0755", st.Mode().Perm())
	}
	parent, err := os.Stat(filepath.Dir(c.IndexDir))
	if err != nil || !parent.IsDir() {
		t.Fatalf("IndexDir parent not created: %v", err)
	}
	// The IndexDir leaf must NOT exist — Bleve insists on creating it.
	if _, err := os.Stat(c.IndexDir); !os.IsNotExist(err) {
		t.Fatalf("IndexDir leaf was created; it must be left to Bleve")
	}
}

func TestValidateEmptyIndexDir(t *testing.T) {
	tmp := t.TempDir()
	c := Config{DataDir: filepath.Join(tmp, "data")}
	if err := c.Validate(); err != nil {
		t.Fatalf("empty IndexDir must be valid (indexing off): %v", err)
	}
}

func TestValidateDataDirCreateFailure(t *testing.T) {
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c := Config{DataDir: filepath.Join(blocker, "data")}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "create DataDir") {
		t.Fatalf("err = %v, want 'create DataDir'", err)
	}
}

func TestValidateIndexDirParentCreateFailure(t *testing.T) {
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c := Config{
		DataDir:  filepath.Join(tmp, "data"),
		IndexDir: filepath.Join(blocker, "sub", "index"),
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "parent of IndexDir") {
		t.Fatalf("err = %v, want 'parent of IndexDir'", err)
	}
}

// TestUnsafeGateProductionBranch exercises the production (non-test) branch of
// the single unsafe gate by threading inTest=false explicitly.
func TestUnsafeGateProductionBranch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		regtest bool
		env     string
		inTest  bool
		ok      bool
	}{
		{"no flags, prod, no env", false, "", false, true},
		{"regtest, prod, no env", true, "", false, false},
		{"regtest, prod, env 1", true, "1", false, true},
		{"regtest, prod, env yes", true, "yes", false, false},
		{"regtest, prod, env true", true, "true", false, false},
		{"regtest, in test", true, "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SWARTZNET_UNSAFE", tc.env)
			c := Config{Regtest: tc.regtest}
			err := c.checkUnsafe(tc.inTest)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("want rejection")
				}
				if !strings.Contains(err.Error(), "SWARTZNET_UNSAFE") {
					t.Fatalf("rejection %q must name SWARTZNET_UNSAFE", err)
				}
			}
		})
	}
}

func TestValidateAuthorizesUnderTest(t *testing.T) {
	tmp := t.TempDir()
	c := Config{DataDir: filepath.Join(tmp, "data"), Regtest: true, DHTInsecure: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("testing.Testing() must authorize regtest in test binaries: %v", err)
	}
}

// TestNoSecondGateName pins the §6 fix: the legacy second env gate name must
// not exist anywhere in the rebuilt tree.
func TestNoSecondGateName(t *testing.T) {
	banned := "SWARTZNET_" + "ALLOW_REGTEST" // split so this file doesn't match itself
	root := moduleRoot(t)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "research" || name == "dist" || name == "legacy" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), banned) {
			t.Errorf("%s contains the retired gate name %s", path, banned)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test dir")
		}
		dir = parent
	}
}

func TestValidateLayerDMode(t *testing.T) {
	for _, m := range []string{"", "legacy", "composite", "aggregatePPMI"} {
		c := Default()
		c.DataDir = t.TempDir()
		c.IndexDir = ""
		c.LayerDMode = m
		if err := c.Validate(); err != nil {
			t.Errorf("LayerDMode %q rejected: %v", m, err)
		}
	}
	c := Default()
	c.DataDir = t.TempDir()
	c.IndexDir = ""
	c.LayerDMode = "bogus"
	if err := c.Validate(); err == nil {
		t.Error("bogus LayerDMode accepted")
	}
}
