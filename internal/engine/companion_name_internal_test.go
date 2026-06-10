package engine

import "testing"

// TestUnsafeCompanionName covers the defence-in-depth name guard
// FetchCompanionTorrent applies before joining the untrusted
// info.Name into DataDir. anacrolix's storage layer already rejects
// non-sub-paths when it opens storage, but the path the fetcher
// RETURNS is built by hand — so the guard must hold on its own.
func TestUnsafeCompanionName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		unsafe bool
	}{
		{"index.json.gz", false},
		{"companion-2026-06.bin", false},
		{"with space.gz", false},
		{"", true},
		{".", true},
		{"..", true},
		{"../escape", true},
		{"a/b", true},
		{`a\b`, true},
		{"/abs", true},
	}
	for _, tc := range cases {
		if got := unsafeCompanionName(tc.name); got != tc.unsafe {
			t.Errorf("unsafeCompanionName(%q) = %v, want %v", tc.name, got, tc.unsafe)
		}
	}
}
