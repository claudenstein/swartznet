package extractors

import "testing"

// TestMP4DispatchSkipsOversize covers the
// `if c.Size > 64*1024*1024*1024 { return false }` arm in mp4
// init's claims. Even with a clearly-mp4 path, files >64 GiB
// must NOT be claimed.
func TestMP4DispatchSkipsOversize(t *testing.T) {
	t.Parallel()
	got, _ := Dispatch(Candidate{Path: "huge.mp4", Size: 70 * 1024 * 1024 * 1024})
	if got != nil && got.Name() == "mp4" {
		t.Errorf("Dispatch should skip MP4 > 64 GiB; got %s", got.Name())
	}
}
