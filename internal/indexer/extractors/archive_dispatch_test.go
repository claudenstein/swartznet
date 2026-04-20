package extractors

import "testing"

// TestArchiveDispatchByMIME covers the MIME-prefix branches of
// the archive init's claims() function. The existing dispatch
// test only exercises filename-extension matching; this one
// covers every MIME the dispatcher accepts.
func TestArchiveDispatchByMIME(t *testing.T) {
	t.Parallel()
	mimes := []string{
		"application/zip",
		"application/x-tar",
		"application/gzip",
		"application/x-gzip",
	}
	for _, mime := range mimes {
		// Path has no archive extension — only MIME should drive the match.
		got, _ := Dispatch(Candidate{Path: "no-extension-here", MIME: mime, Size: 1024})
		if got == nil || got.Name() != "archive" {
			t.Errorf("Dispatch(MIME=%q) = %v, want archive extractor", mime, got)
		}
	}
}

// TestArchiveDispatchSkipsOversize covers the >500 MiB short-
// circuit. With Size above the cap, even a clearly-archive path
// must NOT claim the file.
func TestArchiveDispatchSkipsOversize(t *testing.T) {
	t.Parallel()
	got, _ := Dispatch(Candidate{Path: "huge.zip", Size: 600 * 1024 * 1024})
	if got != nil && got.Name() == "archive" {
		t.Errorf("Dispatch should skip archives > 500 MiB; got %s", got.Name())
	}
}
