package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/httpapi"
)

// companionAdapter satisfies httpapi.CompanionController by
// delegating to a running publisher and subscriber worker.
// Either may be nil — methods on the missing leg return a clear
// error or empty struct so the GUI can still render half the
// view.
//
// The adapter also owns the on-disk follow file: every Follow /
// Unfollow call mutates the in-memory worker state AND rewrites
// the JSON file under a mutex so a daemon restart preserves the
// follow list.
type companionAdapter struct {
	pub        *companion.Publisher
	sub        *companion.SubscriberWorker
	followPath string
	followMu   sync.Mutex
}

// newCompanionAdapter wires the adapter to a running publisher
// and subscriber. Either may be nil. followPath may be empty,
// in which case Follow / Unfollow still mutate the in-memory
// worker but do not persist.
func newCompanionAdapter(pub *companion.Publisher, sub *companion.SubscriberWorker, followPath string) *companionAdapter {
	return &companionAdapter{pub: pub, sub: sub, followPath: followPath}
}

func (a *companionAdapter) PublisherStatus() httpapi.CompanionPublisherStatus {
	if a.pub == nil {
		return httpapi.CompanionPublisherStatus{}
	}
	st := a.pub.Status()
	return httpapi.CompanionPublisherStatus{
		LastRefresh:    st.LastRefresh,
		LastInfoHash:   st.LastInfoHash,
		LastError:      st.LastError,
		PublishedCount: st.PublishedCount,
		PubKeyHex:      st.PubKeyHex,
	}
}

func (a *companionAdapter) RefreshNow() error {
	if a.pub == nil {
		return errors.New("companion publisher not configured")
	}
	return a.pub.RefreshNow()
}

func (a *companionAdapter) SubscriberStatus() []httpapi.CompanionFollowStatus {
	if a.sub == nil {
		return nil
	}
	follows := a.sub.Following()
	out := make([]httpapi.CompanionFollowStatus, 0, len(follows))
	for pub, label := range follows {
		res := a.sub.LastSync(pub)
		row := httpapi.CompanionFollowStatus{
			PubKeyHex:        hex.EncodeToString(pub[:]),
			Label:            label,
			TorrentsImported: res.TorrentsImported,
			ContentImported:  res.ContentImported,
			GeneratedAt:      res.GeneratedAt,
		}
		if res.Err != nil {
			row.LastError = res.Err.Error()
		}
		var zero [20]byte
		if res.PointerInfoHash != zero {
			row.PointerInfoHash = hex.EncodeToString(res.PointerInfoHash[:])
		}
		// LastSyncAt: there is no separate timestamp on
		// SyncResult, so we use GeneratedAt as a stand-in for
		// "the snapshot we synced is from this time" — the
		// actual wall-clock at sync time can be added later if
		// the GUI asks for it.
		if res.GeneratedAt > 0 {
			row.LastSyncAt = time.Unix(res.GeneratedAt, 0).UTC()
		}
		out = append(out, row)
	}
	return out
}

func (a *companionAdapter) Follow(pubkey [32]byte, label string) error {
	if a.sub == nil {
		return errors.New("companion subscriber not configured")
	}
	a.sub.Follow(pubkey, label)
	return a.persistFollows()
}

func (a *companionAdapter) Unfollow(pubkey [32]byte) error {
	if a.sub == nil {
		return errors.New("companion subscriber not configured")
	}
	a.sub.Unfollow(pubkey)
	return a.persistFollows()
}

// persistFollows serialises the current follow list to the
// configured on-disk path. Atomic write via tempfile + rename so
// a partial write does not corrupt the file. Skips silently
// when followPath is empty.
func (a *companionAdapter) persistFollows() error {
	if a.followPath == "" || a.sub == nil {
		return nil
	}
	a.followMu.Lock()
	defer a.followMu.Unlock()

	follows := a.sub.Following()
	entries := make([]followEntry, 0, len(follows))
	for pub, label := range follows {
		entries = append(entries, followEntry{
			PubKey: hex.EncodeToString(pub[:]),
			Label:  label,
		})
	}
	body, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal follows: %w", err)
	}
	tmp := a.followPath + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, a.followPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
