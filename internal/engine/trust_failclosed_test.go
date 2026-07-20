package engine

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/reputation"
)

// TestFlagFailsClosedWhenTrustDegraded pins the fail-closed fix: when
// trust.json is CONFIGURED but fails to load (corrupt/unreadable), the
// engine cannot check the trusted-publisher exemption, so FlagHit must
// demote nobody and report it honestly — never silently demote a publisher
// that might be trusted.
func TestFlagFailsClosedWhenTrustDegraded(t *testing.T) {
	dir := t.TempDir()
	trustPath := filepath.Join(dir, "trust.json")
	if err := os.WriteFile(trustPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		DataDir:        filepath.Join(dir, "data"),
		ListenPort:     0,
		DisableDHT:     true,
		BloomPath:      filepath.Join(dir, "known-good.bloom"),
		ReputationPath: filepath.Join(dir, "reputation.json"),
		TrustPath:      trustPath,
	}
	e, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })

	if !e.trustDegraded() {
		t.Fatal("trust should be degraded after loading a corrupt trust.json")
	}

	ih := strings.Repeat("dd", 20) // 40 hex
	pk := reputation.PubKeyHex(strings.Repeat("ab", 32))
	e.SourceTracker().Record(ih, pk)

	res, ok := e.FlagHit(ih)
	if !ok {
		t.Fatal("FlagHit should succeed (tracker is configured)")
	}
	if res.Flagged != 0 || res.Attribution != "trust-unavailable" {
		t.Fatalf("flag must fail closed: got Flagged=%d Attribution=%q", res.Flagged, res.Attribution)
	}
	// No reputation may have changed — the attributed publisher is not demoted.
	if tr := e.ReputationTracker(); tr != nil {
		if snap := tr.Snapshot(); len(snap) != 0 {
			t.Fatalf("no reputation should have changed, got %d records", len(snap))
		}
	}
	// The attribution is preserved so a retry after trust.json is fixed can act.
	if got := e.SourceTracker().Sources(ih); len(got) != 1 {
		t.Fatalf("attribution should be preserved on fail-closed, got %v", got)
	}
}

// TestFlagStillWorksWhenTrustGenuinelyOff confirms the degraded path does
// NOT trip when trust is simply unconfigured (empty TrustPath): flagging an
// attributed, non-trusted publisher still demotes normally.
func TestFlagStillWorksWhenTrustGenuinelyOff(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		DataDir:        filepath.Join(dir, "data"),
		ListenPort:     0,
		DisableDHT:     true,
		ReputationPath: filepath.Join(dir, "reputation.json"),
		// TrustPath empty → trust off, NOT degraded.
	}
	e, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })

	if e.trustDegraded() {
		t.Fatal("trust should not be degraded when TrustPath is empty (feature off)")
	}
	ih := strings.Repeat("ee", 20)
	pk := reputation.PubKeyHex(strings.Repeat("cd", 32))
	e.SourceTracker().Record(ih, pk)

	res, ok := e.FlagHit(ih)
	if !ok {
		t.Fatal("FlagHit should succeed")
	}
	if res.Flagged != 1 || res.Attribution != "targeted" {
		t.Fatalf("flag with trust off should demote the attributed indexer: got Flagged=%d Attribution=%q", res.Flagged, res.Attribution)
	}
}
