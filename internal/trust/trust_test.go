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

// TestAddRelabelOverwrites pins the idempotent-overwrite arm of
// Add: a second Add for the same pubkey replaces the label
// rather than appending a duplicate entry.
func TestAddRelabelOverwrites(t *testing.T) {
	t.Parallel()
	s, _ := trust.LoadOrCreate("")
	if err := s.Add(fakeKey1, "old"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(fakeKey1, "new"); err != nil {
		t.Fatalf("Add relabel: %v", err)
	}
	entries := s.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after relabel, got %d", len(entries))
	}
	if got := s.Label(fakeKey1); got != "new" {
		t.Errorf("Label after relabel = %q, want %q", got, "new")
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

// TestAddErrorMessageFrozen pins the exact wording and the
// length interpolation of the rejection error, which the CLI
// surfaces verbatim.
func TestAddErrorMessageFrozen(t *testing.T) {
	t.Parallel()
	s, _ := trust.LoadOrCreate("")
	err := s.Add("tooshort", "x")
	if err == nil {
		t.Fatal("expected error for short pubkey")
	}
	want := "trust: pubkey must be 64 hex characters, got 8"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
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

// TestRemoveIdempotent pins that removing an absent key succeeds
// (no error) and persists without complaint.
func TestRemoveIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")
	s, err := trust.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if err := s.Remove(fakeKey1); err != nil {
		t.Errorf("Remove of absent key should be a no-op success, got %v", err)
	}
}

// TestListReturnsSortedCopy pins the sorted-ascending-by-lowercase
// order and that mutating the returned slice does not affect the
// store.
func TestListReturnsSortedCopy(t *testing.T) {
	t.Parallel()
	s, _ := trust.LoadOrCreate("")
	// Add out of order.
	if err := s.Add(fakeKey2, "Bob"); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(fakeKey1, "Alice"); err != nil {
		t.Fatal(err)
	}
	got := s.List()
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].PubKeyHex != fakeKey1 || got[1].PubKeyHex != fakeKey2 {
		t.Errorf("not sorted ascending: %q then %q", got[0].PubKeyHex, got[1].PubKeyHex)
	}
	// Mutate the copy; the store must be unaffected.
	got[0].Label = "tampered"
	if s.Label(fakeKey1) != "Alice" {
		t.Error("List returned a shared reference; store mutated by caller")
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

// TestSaveFormatIsPrettyArrayWithOmitEmpty pins the on-disk
// format: a pretty-printed 2-space-indented JSON array, with an
// empty label omitted from the object.
func TestSaveFormatIsPrettyArrayWithOmitEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")
	s, err := trust.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	// One labelled, one unlabelled (label omitted from JSON).
	if err := s.Add(fakeKey1, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(fakeKey2, "Bob"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	want := "[\n" +
		"  {\n" +
		"    \"pubkey\": \"" + fakeKey1 + "\"\n" +
		"  },\n" +
		"  {\n" +
		"    \"pubkey\": \"" + fakeKey2 + "\",\n" +
		"    \"label\": \"Bob\"\n" +
		"  }\n" +
		"]"
	if string(body) != want {
		t.Errorf("on-disk format mismatch:\n got %q\nwant %q", string(body), want)
	}
}
