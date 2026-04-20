package httpapi_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestServerStartListenError covers the
// `net.Listen err → return err` branch of Server.Start. Build
// a server with a bogus address (port out of range) so the
// listen call fails before the mux is wired.
func TestServerStartListenError(t *testing.T) {
	t.Parallel()
	s := httpapi.NewWithOptions("127.0.0.1:99999",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		httpapi.Options{})
	if err := s.Start(); err == nil {
		t.Error("Start should fail when address is invalid")
	}
}
