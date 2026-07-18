package wirecompat

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// clusterNodeConfig mirrors NewCluster's per-node config for restart tests
// that rebuild an engine over an existing DataDir.
func clusterNodeConfig(dataDir string) config.Config {
	return config.Config{
		DataDir:               dataDir,
		ListenPort:            0,
		DisableDHT:            true,
		DisablePortForwarding: true,
		Seed:                  true,
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// engineNew constructs an engine over an existing DataDir (restart tests),
// closing it at cleanup.
func engineNew(t *testing.T, dataDir string) (*engine.Engine, error) {
	t.Helper()
	e, err := engine.New(context.Background(), clusterNodeConfig(dataDir), testLogger())
	if err == nil {
		t.Cleanup(func() { _ = e.Close() })
	}
	return e, err
}
