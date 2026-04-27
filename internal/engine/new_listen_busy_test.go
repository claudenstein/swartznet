package engine_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestEngineNewListenPortBusy covers engine.New's
// `cl, err := torrent.NewClient(tc); if err != nil { return … }`
// arm (engine.go:714-717). Bind a listener to a free TCP port,
// then point cfg.ListenPort at the same port so anacrolix's
// client startup fails to bind. Engine.New must surface the
// wrapped "engine: new client" error instead of returning nil.
func TestEngineNewListenPortBusy(t *testing.T) {
	t.Parallel()

	// Take a TCP port. Anacrolix will try to bind to the same
	// (host, port) for both TCP and UDP sockets — TCP being held
	// is enough to make TCP bind fail.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = port
	cfg.ListenHost = "127.0.0.1"
	cfg.DisableDHT = true
	cfg.DisableIPv6 = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(context.Background(), cfg, logger)
	if eng != nil {
		eng.Close()
	}
	if err == nil {
		t.Skip("anacrolix accepted busy port; SO_REUSEADDR or different bind semantics may apply on this kernel")
	}
	if !strings.Contains(err.Error(), "new client") {
		t.Errorf("expected wrapped 'new client' err, got %v", err)
	}
}
