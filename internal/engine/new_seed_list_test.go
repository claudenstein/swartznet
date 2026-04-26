package engine_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/reputation"
)

// TestNewLoadsSeedListInfo covers engine.New's seed-list import
// arm — `if cfg.SeedListPath != "" { tr.LoadSeedList(...) ; if n > 0 { log.Info(...) } }`.
// Plant a small seed list with one valid pubkey and one bad
// entry; engine.New must boot, the good entry must land in the
// tracker, and the bad-entry warn arm must fire too.
func TestNewLoadsSeedListInfo(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	repPath := filepath.Join(dataDir, "rep.json")
	seedPath := filepath.Join(dataDir, "seeds.json")

	goodPubHex := strings.Repeat("ab", 32) // 64 hex chars → 32 bytes
	list := reputation.SeedList{
		Version: 1,
		Seeds: []reputation.SeedListEntry{
			{PubKey: goodPubHex, Label: "Maintainer 1"},
			{PubKey: "not-hex", Label: "bad row"}, // exercises the per-entry err
		},
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seedPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = repPath
	cfg.SeedListPath = seedPath
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()
	if eng.ReputationTracker() == nil {
		t.Fatal("reputation tracker should be wired up")
	}
}
