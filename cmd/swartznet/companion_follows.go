package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/swartznet/swartznet/internal/companion"
)

// followEntry is one row of the on-disk follow list. The file is
// a single JSON array of these. Lives here rather than inside
// internal/companion because the format is a CLI-side detail —
// the subscriber worker accepts (pubkey, label) calls from any
// caller, and the GUI is the source of truth for what gets
// followed.
type followEntry struct {
	PubKey string `json:"pubkey"`
	Label  string `json:"label,omitempty"`
}

// loadFollowFile reads the follow list at path and registers
// every entry with the given subscriber worker. Returns the
// number of publishers successfully registered. Missing files
// are not an error (a fresh install starts with an empty list).
// Malformed entries are logged to stderr but do not abort the
// load.
func loadFollowFile(w *companion.SubscriberWorker, path string, stderr io.Writer) int {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0
		}
		fmt.Fprintf(stderr, "warning: companion follow file: %v\n", err)
		return 0
	}
	defer f.Close()

	var entries []followEntry
	if err := json.NewDecoder(f).Decode(&entries); err != nil {
		fmt.Fprintf(stderr, "warning: companion follow file parse: %v\n", err)
		return 0
	}

	var n int
	for i, e := range entries {
		raw, err := hex.DecodeString(e.PubKey)
		if err != nil || len(raw) != 32 {
			fmt.Fprintf(stderr, "warning: companion follow entry %d: bad pubkey %q\n", i, e.PubKey)
			continue
		}
		var pub [32]byte
		copy(pub[:], raw)
		w.Follow(pub, e.Label)
		n++
	}
	return n
}
