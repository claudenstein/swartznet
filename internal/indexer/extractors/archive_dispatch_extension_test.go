package extractors

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// TestArchiveClaimsByExtensionWithUnknownMIME covers the
// `switch lower { case ".zip", ".tar", ".tgz": return true }`
// arm in the archive registrar's claims function. The Dispatch
// path normally fills in MIME from filepath.Ext before the
// extension switch runs, so the switch is only reachable when a
// caller hands Dispatch a non-empty MIME that doesn't match the
// MIME-prefix arm above. Force that by passing MIME=" " (any
// non-empty, non-archive string) and an archive-typed path.
func TestArchiveClaimsByExtensionWithUnknownMIME(t *testing.T) {
	t.Parallel()
	cases := []string{"x.zip", "x.tar", "x.tgz", "x.tar.gz"}
	for _, p := range cases {
		got, _ := Dispatch(Candidate{Path: p, MIME: "application/octet-stream", Size: 1024})
		if got == nil || got.Name() != "archive" {
			t.Errorf("Dispatch(%q, octet-stream) = %v, want archive extractor", p, got)
		}
	}
}

// TestZipMemberNamesSkipsEmptyName covers the
// `if f.Name == "" { continue }` arm in zipMemberNames. The
// archive/zip writer accepts an empty-name entry; on read, the
// resulting *zip.File has Name == "" and must be filtered out
// before joining the names slice.
func TestZipMemberNamesSkipsEmptyName(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// Empty name first, then a real file so the chunks output is non-empty.
	for _, name := range []string{"", "real.txt"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if name != "" {
			if _, err := w.Write([]byte("data")); err != nil {
				t.Fatalf("zip write: %v", err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	names, err := zipMemberNames(buf.Bytes())
	if err != nil {
		t.Fatalf("zipMemberNames: %v", err)
	}
	for _, n := range names {
		if n == "" {
			t.Errorf("empty-name entry leaked into names slice: %q", names)
		}
	}
	if !contains(names, "real.txt") {
		t.Errorf("real entry missing from names slice: %v", names)
	}
}

func contains(xs []string, target string) bool {
	for _, x := range xs {
		if strings.Contains(x, target) {
			return true
		}
	}
	return false
}
