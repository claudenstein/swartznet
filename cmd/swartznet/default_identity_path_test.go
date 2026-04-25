package main

import (
	"os"
	"strings"
	"testing"
)

// TestDefaultIdentityPath — the helper returns the XDG-style
// identity path under the user's home dir. Locks the
// "/.local/share/swartznet/identity.key" suffix so the daemon
// and the CLI keep agreeing on where the key lives.
func TestDefaultIdentityPath(t *testing.T) {
	t.Parallel()
	got, err := defaultIdentityPath()
	if err != nil {
		t.Fatalf("defaultIdentityPath: %v", err)
	}
	want := "/.local/share/swartznet/identity.key"
	if !strings.HasSuffix(got, want) {
		t.Errorf("defaultIdentityPath() = %q, want suffix %q", got, want)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if !strings.HasPrefix(got, home) {
		t.Errorf("defaultIdentityPath() = %q, want prefix %q", got, home)
	}
}
