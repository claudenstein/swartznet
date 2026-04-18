package trust_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/trust"
)

const (
	fakeKey1 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	fakeKey2 = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

func TestLoadOrCreateEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")
	s, err := trust.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if len(s.List()) != 0 {
		t.Errorf("expected empty list, got %d entries", len(s.List()))
	}
	if s.IsTrusted(fakeKey1) {
		t.Error("unknown key should not be trusted")
	}
}

func TestAddListRemovePersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	s, err := trust.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}

	if err := s.Add(fakeKey1, "Alice"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(fakeKey2, "Bob"); err != nil {
		t.Fatalf("Add Bob: %v", err)
	}

	if !s.IsTrusted(fakeKey1) {
		t.Error("fakeKey1 should be trusted")
	}
	if s.Label(fakeKey1) != "Alice" {
		t.Errorf("label mismatch: got %q", s.Label(fakeKey1))
	}

	// Reload from disk and verify persistence.
	s2, err := trust.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !s2.IsTrusted(fakeKey1) || !s2.IsTrusted(fakeKey2) {
		t.Error("keys not persisted across reload")
	}
	if s2.Label(fakeKey2) != "Bob" {
		t.Errorf("Bob label not persisted: got %q", s2.Label(fakeKey2))
	}

	// Remove fakeKey1.
	if err := s.Remove(fakeKey1); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if s.IsTrusted(fakeKey1) {
		t.Error("fakeKey1 should no longer be trusted after Remove")
	}

	// Reload after remove, confirm removal persisted.
	s3, err := trust.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("reload after remove: %v", err)
	}
	if s3.IsTrusted(fakeKey1) {
		t.Error("removal did not persist")
	}
}

func TestAddRejectsBadKey(t *testing.T) {
	t.Parallel()
	s, _ := trust.LoadOrCreate("")
	if err := s.Add("tooshort", "x"); err == nil {
		t.Error("expected error for short pubkey")
	}
	if err := s.Add("not-hex-not-hex-not-hex-not-hex-not-hex-not-hex-not-hex-not-hexn", "x"); err == nil {
		t.Error("expected error for non-hex pubkey (exactly 64 chars of non-hex)")
	}
}

func TestInMemoryStore(t *testing.T) {
	t.Parallel()
	// Empty path => in-memory only.
	s, err := trust.LoadOrCreate("")
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if err := s.Add(fakeKey1, "Memory Alice"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !s.IsTrusted(fakeKey1) {
		t.Error("added key should be trusted")
	}
}

func TestLoadSkipsMalformedEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")
	content := `[
		{"pubkey": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "label": "ok"},
		{"pubkey": "tooshort", "label": "bad"}
	]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := trust.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if len(s.List()) != 1 {
		t.Errorf("expected 1 valid entry, got %d", len(s.List()))
	}
}

// TestCaseInsensitiveLookup verifies that Add / IsTrusted /
// Label / Remove normalise their pubkey argument to lowercase,
// so a caller that supplies uppercase hex still interoperates
// with the rest of the codebase (which always goes through
// signing.PubKeyHex and therefore always emits lowercase).
func TestCaseInsensitiveLookup(t *testing.T) {
	t.Parallel()
	s, err := trust.LoadOrCreate("")
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	// Add with uppercase.
	upper := "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"
	lower := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := s.Add(upper, "alice"); err != nil {
		t.Fatalf("Add(upper): %v", err)
	}
	// Lookup via lowercase should find it.
	if !s.IsTrusted(lower) {
		t.Error("IsTrusted(lower) = false after Add(upper); want true")
	}
	// Lookup via uppercase should also find it.
	if !s.IsTrusted(upper) {
		t.Error("IsTrusted(upper) = false after Add(upper); want true")
	}
	// Stored form should be lowercase.
	entries := s.List()
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].PubKeyHex != lower {
		t.Errorf("stored pubkey = %q, want lowercase %q", entries[0].PubKeyHex, lower)
	}
	if got := s.Label(upper); got != "alice" {
		t.Errorf("Label(upper) = %q, want 'alice'", got)
	}
	// Remove with mixed case should clear it.
	mixed := "ABcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := s.Remove(mixed); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if s.IsTrusted(lower) {
		t.Error("IsTrusted(lower) = true after Remove(mixed); want false")
	}
}

// TestLoadNormalisesUppercaseOnDisk verifies that a pre-existing
// on-disk trust file with uppercase-hex entries loads with the
// keys lowered, so subsequent IsTrusted/Label checks work.
func TestLoadNormalisesUppercaseOnDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	body := `[{"pubkey":"ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789","label":"loaded-upper"}]`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := trust.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	lower := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if !s.IsTrusted(lower) {
		t.Error("uppercase on-disk entry not reachable via lowercase lookup after load")
	}
	entries := s.List()
	if len(entries) != 1 || entries[0].PubKeyHex != lower {
		t.Errorf("stored pubkey not normalised: %+v", entries)
	}
}
