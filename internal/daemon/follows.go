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

// maxFollowFileBytes caps how much of the follow file we will
// read before failing closed. The file is local config, but a
// hard bound keeps an accidentally-huge file (or a symlink
// pointing at one) from ballooning memory at startup. Each entry
// is ~100 bytes, so 1 MiB still allows ~10k follows.
const maxFollowFileBytes = 1 << 20

// LoadFollowFile reads the follow list at path and registers
// every entry with the given subscriber worker. Returns the
// number of publishers successfully registered and a non-nil
// error when the file existed but couldn't be loaded (open
// failure other than not-exist, JSON parse failure, or a file
// over maxFollowFileBytes, which is rejected wholesale — fail
// closed). A missing file is not an error: a fresh install
// starts with an empty list. Per-entry malformed pubkeys are
// logged to stderr and skipped without surfacing as an error —
// partial loads are deliberately permitted so one bad row
// doesn't strand the whole follow list.
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

	// Bound the read and fail closed over the cap — a follow file
	// that large is corrupt or hostile, and half-loading it would
	// silently drop an arbitrary suffix of the user's follow list.
	data, err := io.ReadAll(io.LimitReader(f, maxFollowFileBytes+1))
	if err != nil {
		fmt.Fprintf(stderr, "warning: companion follow file read: %v\n", err)
		return 0, fmt.Errorf("daemon: read companion follow file %q: %w", path, err)
	}
	if len(data) > maxFollowFileBytes {
		fmt.Fprintf(stderr, "warning: companion follow file exceeds %d bytes; ignoring\n", maxFollowFileBytes)
		return 0, fmt.Errorf("daemon: companion follow file %q exceeds %d bytes", path, maxFollowFileBytes)
	}

	var entries []followEntry
	if err := json.Unmarshal(data, &entries); err != nil {
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
