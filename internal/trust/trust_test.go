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
