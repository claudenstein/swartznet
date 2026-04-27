package main

import (
	"io"
	"testing"
)

// TestTorrentFileFlag covers the torrentFileFlag.String + Set
// methods used by the --torrent repeated flag.
func TestTorrentFileFlag(t *testing.T) {
	t.Parallel()
	var flags torrentFileFlag
	if got := flags.String(); got != "[]" {
		t.Errorf("empty String() = %q, want \"[]\"", got)
	}
	if err := flags.Set("a.torrent"); err != nil {
		t.Fatal(err)
	}
	if err := flags.Set("b.torrent"); err != nil {
		t.Fatal(err)
	}
	if len(flags) != 2 {
		t.Errorf("len(flags) = %d, want 2", len(flags))
	}
	if flags[0] != "a.torrent" || flags[1] != "b.torrent" {
		t.Errorf("flag contents = %v, want [a.torrent b.torrent]", []string(flags))
	}
}

// TestNewLogger sweeps SWARTZNET_LOG values and confirms
// newLogger never returns nil and never panics.
func TestNewLogger(t *testing.T) {
	for _, lvl := range []string{"debug", "info", "warn", "error", ""} {
		t.Run(lvl, func(t *testing.T) {
			t.Setenv("SWARTZNET_LOG", lvl)
			lg := newLogger(io.Discard)
			if lg == nil {
				t.Errorf("newLogger(%q) returned nil", lvl)
			}
		})
	}
}
