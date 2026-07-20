package gui

import (
	"reflect"
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestSplitList(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{",, ,", nil},
		{"http://a/announce", []string{"http://a/announce"}},
		{"http://a/announce http://b/announce", []string{"http://a/announce", "http://b/announce"}},
		{"http://a/announce, http://b/announce", []string{"http://a/announce", "http://b/announce"}},
		{" udp://x:6969\nhttp://y/announce\t", []string{"udp://x:6969", "http://y/announce"}},
	} {
		if got := splitList(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitList(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestDeriveTorrentPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"/a/b/movie.mkv", "/a/b/movie.mkv.torrent"},
		{"/a/b/folder", "/a/b/folder.torrent"},
		{"/a/b/folder/", "/a/b/folder.torrent"}, // trailing sep trimmed
		{"  /a/b/x  ", "/a/b/x.torrent"},
	} {
		if got := deriveTorrentPath(tc.in); got != tc.want {
			t.Errorf("deriveTorrentPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestShowCreateDialogNilSafe pins the guard: the create dialog must no-op (not
// panic) when there is no daemon/engine wired, mirroring the other tab actions.
func TestShowCreateDialogNilSafe(t *testing.T) {
	test.NewApp()
	t.Cleanup(func() { test.NewApp() })
	dl := &downloadsTab{} // d == nil
	dl.showCreateDialog() // must return early, no panic
}
