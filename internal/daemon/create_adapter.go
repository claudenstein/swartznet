package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/identity"
)

// createTorrent implements httpapi's CreateTorrent collaborator: it builds a
// .torrent from a daemon-side path, optionally signs it with the node identity,
// and optionally seeds the content in place through the RUNNING engine (so a
// web-UI create appears in the Downloads list immediately, exactly like the
// native GUI's in-process create). httpapi imports no engine/identity types; the
// translation lives here.
func createTorrent(eng *engine.Engine, id *identity.Identity, version string, p httpapi.CreateTorrentParams) (httpapi.CreateTorrentResult, error) {
	// Cheap pre-flight: fail fast on a missing output directory BEFORE hashing the
	// (possibly large) content, so a typo'd output path doesn't waste a long hash.
	if dir := filepath.Dir(p.Output); dir != "" && dir != "." {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return httpapi.CreateTorrentResult{}, fmt.Errorf("output directory does not exist: %s", dir)
		}
	}

	opts := engine.CreateTorrentOptions{
		Root:      p.Root,
		Trackers:  p.Trackers,
		Private:   p.Private,
		Comment:   p.Comment,
		CreatedBy: "swartznet " + version,
	}
	if p.Sign {
		// Fail closed: if the caller asked to sign but no identity is loaded, do
		// NOT silently produce an unsigned torrent.
		if id == nil {
			return httpapi.CreateTorrentResult{}, fmt.Errorf("cannot sign: no identity loaded")
		}
		s := id.Signer()
		opts.SignWith = &s
	}

	ihHex, raw, err := engine.CreateTorrentFile(opts, p.Output)
	if err != nil {
		return httpapi.CreateTorrentResult{}, err
	}
	res := httpapi.CreateTorrentResult{InfoHash: ihHex}
	if p.Seed {
		if _, err := eng.AddTorrentBytesSeedFrom(raw, p.Root); err != nil {
			// The .torrent WAS created and written; a seed-leg failure is a partial
			// success, not a create failure. Report it as a warning on the (valid)
			// result rather than an error that would discard the infohash + return
			// a misleading 400.
			res.SeedError = err.Error()
		} else {
			res.Seeded = true
		}
	}
	return res, nil
}
