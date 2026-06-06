package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateMkdirDataDirFailure covers the wrapped MkdirAll
// error branch: the requested DataDir is under a regular file,
// so MkdirAll cannot create the leaf and Validate must return
// the wrapped "cannot create DataDir" error.
func TestValidateMkdirDataDirFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("file-not-dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := Default()
	c.DataDir = filepath.Join(blocker, "data")
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate should fail when DataDir is under a regular file")
	}
	if !strings.Contains(err.Error(), "create DataDir") {
		t.Errorf("error = %q, want it to mention 'create DataDir'", err)
	}
}

// TestValidateMkdirIndexDirParentFailure covers the wrapped
// MkdirAll error for IndexDir's parent. Same trick: parent path
// is a regular file.
func TestValidateMkdirIndexDirParentFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("file-not-dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := Default()
	c.DataDir = t.TempDir()
	// IndexDir's parent (blocker/nested) doesn't exist and can't be
	// created because its own parent (blocker) is a regular file.
	c.IndexDir = filepath.Join(blocker, "nested", "index.bleve")
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate should fail when IndexDir's parent cannot be created")
	}
	if !strings.Contains(err.Error(), "parent of IndexDir") {
		t.Errorf("error = %q, want it to mention 'parent of IndexDir'", err)
	}
}

// TestValidateRegtestGuard covers the fail-closed guard on the
// production-dangerous Regtest / DHTInsecure knobs.
//
// Because this very binary is a `go test` binary, testing.Testing()
// is true, so Validate must ACCEPT the dangerous flags here without
// any env opt-in — otherwise the testlab harness (which always sets
// Regtest=true) could never spawn an engine. The env-gated path is
// covered separately by TestAllowRegtestOutsideTests, which drives
// the decision through a seam that does not consult testing.Testing.
func TestValidateRegtestGuard(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		regtest     bool
		dhtInsecure bool
	}{
		{"clean", false, false},
		{"regtest", true, false},
		{"dht-insecure", false, true},
		{"both", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := Default()
			c.DataDir = t.TempDir()
			c.IndexDir = filepath.Join(t.TempDir(), "index")
			c.Regtest = tc.regtest
			c.DHTInsecure = tc.dhtInsecure
			if err := c.Validate(); err != nil {
				t.Fatalf("Validate() under a test binary must accept Regtest/DHTInsecure, got %v", err)
			}
		})
	}
}

// TestAllowRegtestOutsideTests exercises the decision the guard
// makes for a non-test ("production") binary: dangerous knobs are
// rejected unless the operator opts in via the env var. We can't
// flip testing.Testing() for this process, so we test the pure
// decision helper that Validate delegates to.
func TestAllowRegtestOutsideTests(t *testing.T) {
	cases := []struct {
		name        string
		regtest     bool
		dhtInsecure bool
		inTest      bool
		env         string
		wantErr     bool
	}{
		{"prod-clean", false, false, false, "", false},
		{"prod-regtest-no-optin", true, false, false, "", true},
		{"prod-dht-insecure-no-optin", false, true, false, "", true},
		{"prod-regtest-optin", true, false, false, "1", false},
		{"prod-dht-insecure-optin", false, true, false, "1", false},
		{"prod-regtest-bad-optin", true, false, false, "yes", true},
		{"test-regtest-no-optin", true, false, true, "", false},
		{"test-dht-insecure-no-optin", false, true, true, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(allowRegtestEnv, tc.env)
			c := Default()
			c.Regtest = tc.regtest
			c.DHTInsecure = tc.dhtInsecure
			err := c.dangerousFlagsRejected(tc.inTest)
			if tc.wantErr && err == nil {
				t.Fatalf("dangerousFlagsRejected(%v) = nil, want error", tc.inTest)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("dangerousFlagsRejected(%v) = %v, want nil", tc.inTest, err)
			}
		})
	}
}

// TestSwartznetShareRootHomelessFallback covers the third return
// in swartznetShareRoot: no XDG_DATA_HOME and no detectable home
// directory. We force this by clearing every env var the stdlib
// userHomeDir consults on Linux — HOME and the common user-DB
// fallbacks. If the runtime still resolves a home (e.g. from
// /etc/passwd), we skip the test rather than fail.
func TestSwartznetShareRootHomelessFallback(t *testing.T) {
	// Note: t.Setenv requires Go 1.17+ and is automatically reverted.
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "")

	got := swartznetShareRoot()
	if got == "./swartznet-state" {
		// Hit the documented homeless fallback — pass.
		return
	}
	// Otherwise, the OS still resolved a home (Linux user-DB,
	// macOS, etc.). We can't force the fallback portably, so
	// document the skip rather than spuriously fail.
	t.Skipf("homeless fallback unreachable on this runtime; got %q", got)
}
