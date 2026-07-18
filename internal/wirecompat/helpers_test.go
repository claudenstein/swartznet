package wirecompat

import (
	"io"
	"log/slog"

	"github.com/swartznet/swartznet/internal/config"
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
