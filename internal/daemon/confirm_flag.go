package daemon

import (
	"encoding/hex"
	"fmt"

	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/reputation"
)

// Confirm is the ONE shared confirm path — CLI, web, and (later) GUI all
// reach this single method, so reputation feedback can never differ across
// surfaces (the §6 inconsistency). It adds the infohash to the known-good
// Bloom and boosts its attributed indexers. Errors map to HTTP status.
func (d *Daemon) Confirm(ihHex string) (httpapi.ConfirmResult, error) {
	ih, err := decodeInfohash(ihHex)
	if err != nil {
		return httpapi.ConfirmResult{}, err
	}
	confirmed, ok := d.Eng.ConfirmHit(ih, ihHex)
	if !ok {
		return httpapi.ConfirmResult{}, httpapi.ErrBloomNotConfigured
	}
	d.Log.Info("httpapi.confirm", "infohash", ihHex, "indexers", confirmed)
	return httpapi.ConfirmResult{InfoHash: ihHex, IndexersConfirmed: confirmed}, nil
}

// Flag is the ONE shared flag path: it demotes only the attributed,
// non-trusted indexers (trusted publishers are exempt — D21), forgets the
// attribution so a second flag can't double-dock, and reports honestly when
// nobody was demoted (fail-closed on zero attribution).
func (d *Daemon) Flag(ihHex string) (httpapi.FlagResult, error) {
	if _, err := decodeInfohash(ihHex); err != nil {
		return httpapi.FlagResult{}, err
	}
	res, ok := d.Eng.FlagHit(ihHex)
	if !ok {
		return httpapi.FlagResult{}, httpapi.ErrTrackerNotConfigured
	}
	d.Log.Info("httpapi.flag", "infohash", ihHex, "indexers", res.Flagged, "attribution", res.Attribution)
	return httpapi.FlagResult{InfoHash: ihHex, IndexersFlagged: res.Flagged, Attribution: res.Attribution}, nil
}

// decodeInfohash validates a 40-hex infohash, returning the httpapi bad-hash
// error so the handler answers 400.
func decodeInfohash(ihHex string) ([20]byte, error) {
	var ih [20]byte
	b, err := hex.DecodeString(ihHex)
	if err != nil || len(b) != 20 {
		return ih, httpapi.ErrBadInfohash
	}
	copy(ih[:], b)
	return ih, nil
}

var _ = fmt.Sprintf // keep fmt available for future error context

// reputationView adapts the engine's reputation tracker to
// admission.ReputationView. A nil tracker scores the neutral prior so
// admission still denies unknown pubkeys deterministically.
type reputationView struct {
	eng *engine.Engine
}

func (v reputationView) Score(pub [32]byte) float64 {
	t := v.eng.ReputationTracker()
	if t == nil {
		return 0.5
	}
	return t.Score(reputation.PubKey(pub))
}

func (v reputationView) IsSeeded(pub [32]byte) bool {
	t := v.eng.ReputationTracker()
	return t != nil && t.IsSeeded(reputation.PubKey(pub))
}

func (v reputationView) MarkSeeded(pub [32]byte, label string) {
	if t := v.eng.ReputationTracker(); t != nil {
		t.MarkSeeded(reputation.PubKey(pub), label)
	}
}
