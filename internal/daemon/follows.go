package daemon

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
// internal/companion because the format is a daemon-side detail —
// the subscriber worker accepts (pubkey, label) calls from any
// caller, and the CLI/GUI is the source of truth for what gets
// followed.
type followEntry struct {
	PubKey string `json:"pubkey"`
	Label  string `json:"label,omitempty"`
}

// LoadFollowFile reads the follow list at path and registers
// every entry with the given subscriber worker. Returns the
// number of publishers successfully registered and a non-nil
// error when the file existed but couldn't be loaded (open
// failure other than not-exist, or JSON parse failure). A
// missing file is not an error: a fresh install starts with
// an empty list. Per-entry malformed pubkeys are logged to
// stderr and skipped without surfacing as an error — partial
// loads are deliberately permitted so one bad row doesn't
// strand the whole follow list.
//
// The returned error is intended for the caller to surface
// through its structured logger; the human-readable warning
// is also written to stderr for backwards-compat with the
// previous signature.
func LoadFollowFile(w *companion.SubscriberWorker, path string, stderr io.Writer) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		fmt.Fprintf(stderr, "warning: companion follow file: %v\n", err)
		return 0, fmt.Errorf("daemon: open companion follow file %q: %w", path, err)
	}
	defer f.Close()

	var entries []followEntry
	if err := json.NewDecoder(f).Decode(&entries); err != nil {
		fmt.Fprintf(stderr, "warning: companion follow file parse: %v\n", err)
		return 0, fmt.Errorf("daemon: parse companion follow file %q: %w", path, err)
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
	return n, nil
}
