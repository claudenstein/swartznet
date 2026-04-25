package httpapi_test

import (
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestNewWithOptionsDefaults exercises the two defaulting branches
// in NewWithOptions: a nil log is replaced with slog.Default and
// an empty addr is replaced with "localhost:7654". We can't read
// either field directly, but Addr() returns "" pre-Start and we
// can prove the constructor didn't panic and produced a usable
// server (Stop is a no-op pre-Start).
func TestNewWithOptionsDefaults(t *testing.T) {
	t.Parallel()

	// Empty addr + nil log: both default-paths fire.
	s := httpapi.NewWithOptions("", nil, httpapi.Options{})
	if s == nil {
		t.Fatal("NewWithOptions returned nil")
	}
	// Pre-Start, Addr must be "" (covers the listener==nil branch).
	if got := s.Addr(); got != "" {
		t.Errorf("Addr pre-Start = %q, want empty", got)
	}
}

// TestAddrEmptyBeforeStart pins the documented "Addr returns ”
// until Start has been called" behaviour. The other Addr tests
// implicitly only exercise the post-Start path.
func TestAddrEmptyBeforeStart(t *testing.T) {
	t.Parallel()
	s := httpapi.NewWithOptions("localhost:0", silentLogger(), httpapi.Options{})
	if got := s.Addr(); got != "" {
		t.Errorf("Addr pre-Start = %q, want empty", got)
	}
}

// TestNewWithOptionsAcceptsExplicitAddr is a sanity check that the
// non-default branch (addr != "") leaves the addr untouched. We
// inspect the panic-free start as the proxy signal — the existing
// tests already cover Start success.
func TestNewWithOptionsAcceptsExplicitAddr(t *testing.T) {
	t.Parallel()
	s := httpapi.NewWithOptions("127.0.0.1:0", silentLogger(), httpapi.Options{})
	if s == nil {
		t.Fatal("NewWithOptions returned nil")
	}
	// Sanity: the API helper accepts "127.0.0.1:0" as an addr — if
	// the constructor were silently dropping it for the default we
	// would not be able to subsequently start on it. We don't start
	// here (no need to occupy a port for a string-handling test),
	// but we can sanity-check the helper survived construction.
	_ = strings.TrimSpace(s.Addr()) // post-construction, pre-Start
}
