package daemon

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/httpapi"
)

// maxFollowFileBytes bounds the companion follow file (~10k follows). An
// oversize file fails closed wholesale rather than silently dropping a suffix.
const maxFollowFileBytes = 1 << 20

// followEntry is one row in the companion follow file — a single JSON array.
type followEntry struct {
	PubKey string `json:"pubkey"`
	Label  string `json:"label,omitempty"`
}

// LoadFollowFile loads a companion follow file into the worker. A missing file
// is a fresh install (0, nil). An oversize file fails closed. Per-entry: a
// pubkey that is not exactly 32 bytes is warned and skipped (one bad row does
// not strand the list). Returns the number of follows loaded.
func LoadFollowFile(w *companion.SubscriberWorker, path string, log *slog.Logger) (int, error) {
	if w == nil || path == "" {
		return 0, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("daemon: open companion follow file %q: %w", path, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxFollowFileBytes+1))
	if err != nil {
		return 0, fmt.Errorf("daemon: read companion follow file %q: %w", path, err)
	}
	if int64(len(raw)) > maxFollowFileBytes {
		return 0, fmt.Errorf("daemon: companion follow file %q exceeds %d bytes", path, maxFollowFileBytes)
	}
	var entries []followEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return 0, fmt.Errorf("daemon: parse companion follow file %q: %w", path, err)
	}
	count := 0
	for i, e := range entries {
		decoded, err := hex.DecodeString(e.PubKey)
		if err != nil || len(decoded) != 32 {
			if log != nil {
				log.Warn("daemon.companion.bad_follow_entry", "index", i, "pubkey", e.PubKey)
			}
			continue
		}
		var pub [32]byte
		copy(pub[:], decoded)
		w.Follow(pub, e.Label)
		count++
	}
	return count, nil
}

// companionAdapter satisfies httpapi.CompanionController. Either leg may be nil
// (each gate is independent), so every method degrades gracefully.
type companionAdapter struct {
	pub        *companion.Publisher
	sub        *companion.SubscriberWorker
	followPath string
	followMu   sync.Mutex
}

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
		return fmt.Errorf("companion publisher not configured: %w", httpapi.ErrCompanionUnavailable)
	}
	return a.pub.RefreshNow()
}

func (a *companionAdapter) SubscriberStatus() []httpapi.CompanionFollowStatus {
	if a.sub == nil {
		return nil
	}
	following := a.sub.Following()
	out := make([]httpapi.CompanionFollowStatus, 0, len(following))
	for pub, label := range following {
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
		if res.PointerInfoHash != ([20]byte{}) {
			row.PointerInfoHash = hex.EncodeToString(res.PointerInfoHash[:])
		}
		if res.GeneratedAt > 0 {
			row.LastSyncAt = time.Unix(res.GeneratedAt, 0).UTC()
		}
		out = append(out, row)
	}
	return out
}

// FollowPublisher follows a companion publisher through the PERSISTING
// controller (writes the atomic follow file), so a follow added by ANY frontend
// — including the native GUI — survives a restart. The GUI must call this rather
// than CompSub.Follow directly, which only mutates the in-memory follow set.
func (d *Daemon) FollowPublisher(pubkey [32]byte, label string) error {
	if d.compController == nil {
		return fmt.Errorf("companion controller not configured")
	}
	return d.compController.Follow(pubkey, label)
}

// UnfollowPublisher unfollows through the persisting controller (see
// FollowPublisher). Removes the publisher from the follow file too.
func (d *Daemon) UnfollowPublisher(pubkey [32]byte) error {
	if d.compController == nil {
		return fmt.Errorf("companion controller not configured")
	}
	return d.compController.Unfollow(pubkey)
}

func (a *companionAdapter) Follow(pubkey [32]byte, label string) error {
	if a.sub == nil {
		return fmt.Errorf("companion subscriber not configured: %w", httpapi.ErrCompanionUnavailable)
	}
	a.sub.Follow(pubkey, label)
	return a.persistFollows()
}

func (a *companionAdapter) Unfollow(pubkey [32]byte) error {
	if a.sub == nil {
		return fmt.Errorf("companion subscriber not configured: %w", httpapi.ErrCompanionUnavailable)
	}
	a.sub.Unfollow(pubkey)
	return a.persistFollows()
}

// persistFollows atomically writes the CURRENT worker follow-set to disk
// (tmpfile + rename). A crash mid-write cannot corrupt the list. In-memory
// mutation already happened, so a persist error leaves the follow live.
func (a *companionAdapter) persistFollows() error {
	if a.followPath == "" || a.sub == nil {
		return nil
	}
	a.followMu.Lock()
	defer a.followMu.Unlock()
	following := a.sub.Following()
	entries := make([]followEntry, 0, len(following))
	for pub, label := range following {
		entries = append(entries, followEntry{PubKey: hex.EncodeToString(pub[:]), Label: label})
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal follows: %w", err)
	}
	tmp := a.followPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, a.followPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
