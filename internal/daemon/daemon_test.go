package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
)

// testConfig returns a fully-defaulted config rooted under a temp XDG data
// home, so identity auto-creation (which requires the DEFAULT path) stays
// hermetic and never touches the operator's real ~/.local/share/swartznet.
// The engine is hermetic too: OS-assigned listen port, DHT off.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "xdg"))
	cfg := config.Default()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	// Most daemon tests don't exercise Layer L; opening Bleve per test is
	// slow. The dedicated indexer-wiring test re-enables it.
	cfg.NoIndex = true
	return cfg
}

// eventLog collects ordered events from both code and slog, race-safely.
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (e *eventLog) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, s)
}

func (e *eventLog) list() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.events...)
}

func (e *eventLog) Enabled(context.Context, slog.Level) bool { return true }
func (e *eventLog) Handle(_ context.Context, r slog.Record) error {
	e.add("log:" + r.Message)
	return nil
}
func (e *eventLog) WithAttrs([]slog.Attr) slog.Handler { return e }
func (e *eventLog) WithGroup(string) slog.Handler      { return e }

func TestNewAndStatusRoundTrip(t *testing.T) {
	d, err := New(context.Background(), Options{
		Cfg:     testConfig(t),
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIAddr: "localhost:0",
		Version: "vTEST",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.API == nil {
		t.Fatal("API not started")
	}
	resp, err := http.Get("http://" + d.API.Addr() + "/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestEmptyAPIAddrDisablesAPI(t *testing.T) {
	d, err := New(context.Background(), Options{
		Cfg: testConfig(t),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.API != nil {
		t.Fatal("empty APIAddr must disable the API (empty-path = feature-off)")
	}
}

func TestInvalidConfigAborts(t *testing.T) {
	_, err := New(context.Background(), Options{
		Cfg: config.Config{DataDir: ""},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil || !strings.Contains(err.Error(), "DataDir") {
		t.Fatalf("err = %v, want config rejection", err)
	}
}

func TestAPIBindFailureDegrades(t *testing.T) {
	// Occupy a port so the daemon's bind fails.
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var stderr bytes.Buffer
	d, err := New(context.Background(), Options{
		Cfg:     testConfig(t),
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIAddr: ln.Addr().String(),
		Stderr:  &stderr,
	})
	if err != nil {
		t.Fatalf("bind failure must degrade, not abort: %v", err)
	}
	defer d.Close()
	if d.API != nil {
		t.Fatal("API must be nil after bind failure")
	}
	if !strings.Contains(stderr.String(), "warning: httpapi start failed:") {
		t.Fatalf("stderr = %q, want the degraded-start warning", stderr.String())
	}
}

func TestNilStderrDoesNotPanic(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	d, err := New(context.Background(), Options{
		Cfg:     testConfig(t),
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIAddr: ln.Addr().String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
}

// TestIndexerWiring exercises the Layer-L slot: a daemon with indexing on
// opens the index, /status reports it, /index/stats answers, teardown emits
// indexer.stopped in order, and a --no-index daemon leaves search 503.
func TestIndexerWiring(t *testing.T) {
	ev := &eventLog{}
	cfg := testConfig(t)
	cfg.NoIndex = false // this test wants Layer L on
	d, err := New(context.Background(), Options{
		Cfg:     cfg,
		Log:     slog.New(ev),
		APIAddr: "localhost:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Idx == nil {
		t.Fatal("index not opened")
	}
	addr := d.API.Addr()

	resp, err := http.Get("http://" + addr + "/status")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Local struct {
			Indexed bool `json:"indexed"`
		} `json:"local"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if !st.Local.Indexed {
		t.Fatal("/status local.indexed=false with index wired")
	}

	resp, err = http.Get("http://" + addr + "/index/stats")
	if err != nil {
		t.Fatal(err)
	}
	statsCode := resp.StatusCode
	resp.Body.Close()
	if statsCode != 200 {
		t.Fatalf("/index/stats = %d, want 200", statsCode)
	}

	// Search with no docs: 200, empty local block.
	sresp, err := http.Post("http://"+addr+"/search", "application/json", strings.NewReader(`{"q":"anything"}`))
	if err != nil {
		t.Fatal(err)
	}
	sbody, _ := io.ReadAll(sresp.Body)
	sresp.Body.Close()
	if sresp.StatusCode != 200 || !strings.Contains(string(sbody), `"hits":[]`) {
		t.Fatalf("/search = %d %s", sresp.StatusCode, sbody)
	}

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	events := ev.list()
	engIdx, idxIdx := -1, -1
	for i, e := range events {
		switch e {
		case "log:engine.stopped":
			engIdx = i
		case "log:indexer.stopped":
			idxIdx = i
		}
	}
	if engIdx < 0 || idxIdx < 0 || engIdx > idxIdx {
		t.Fatalf("teardown order: engine.stopped=%d indexer.stopped=%d", engIdx, idxIdx)
	}
}

func TestNoIndexStats503ButSearch200(t *testing.T) {
	d, err := New(context.Background(), Options{
		Cfg:     testConfig(t), // NoIndex true
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIAddr: "localhost:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Idx != nil {
		t.Fatal("NoIndex must not open an index")
	}
	// /index/stats is 503 (index IS the endpoint); /search is 200-empty.
	resp, _ := http.Get("http://" + d.API.Addr() + "/index/stats")
	if resp.StatusCode != 503 {
		t.Fatalf("/index/stats = %d, want 503", resp.StatusCode)
	}
	resp.Body.Close()
	sresp, _ := http.Post("http://"+d.API.Addr()+"/search", "application/json", strings.NewReader(`{"q":"x"}`))
	if sresp.StatusCode != 200 {
		t.Fatalf("/search = %d, want 200 (Layer L off, not 503)", sresp.StatusCode)
	}
	sresp.Body.Close()
}

// TestStatusReportsPubkey is THE §6 defect-absence test: a real daemon (not
// a hand-wired test server) must surface its publisher pubkey on /status.
func TestStatusReportsPubkey(t *testing.T) {
	cfg := testConfig(t)
	d, err := New(context.Background(), Options{
		Cfg:     cfg,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIAddr: "localhost:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Identity == nil {
		t.Fatal("identity not loaded from defaulted config")
	}
	resp, err := http.Get("http://" + d.API.Addr() + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct {
		Publisher struct {
			PubKey string `json:"pubkey"`
		} `json:"publisher"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.Publisher.PubKey != d.Identity.PublicKeyHex() {
		t.Fatalf("/status pubkey = %q, want %q", st.Publisher.PubKey, d.Identity.PublicKeyHex())
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(st.Publisher.PubKey) {
		t.Fatalf("pubkey %q is not 64 lowercase hex chars", st.Publisher.PubKey)
	}
}

func TestIdentityPersistsAcrossRestart(t *testing.T) {
	cfg := testConfig(t)
	newDaemon := func() *Daemon {
		d, err := New(context.Background(), Options{
			Cfg: cfg,
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	d1 := newDaemon()
	pk1 := d1.Identity.PublicKeyHex()
	if err := d1.Close(); err != nil {
		t.Fatal(err)
	}

	d2 := newDaemon()
	pk2 := d2.Identity.PublicKeyHex()
	if err := d2.Close(); err != nil {
		t.Fatal(err)
	}
	if pk1 != pk2 {
		t.Fatalf("pubkey changed across restart: %s vs %s", pk1, pk2)
	}

	// Deleting the key at the default path mints a NEW identity.
	if err := os.Remove(cfg.IdentityPath); err != nil {
		t.Fatal(err)
	}
	d3 := newDaemon()
	pk3 := d3.Identity.PublicKeyHex()
	if err := d3.Close(); err != nil {
		t.Fatal(err)
	}
	if pk3 == pk1 {
		t.Fatal("deleting the key did not mint a new identity")
	}
}

// TestIdentityLoadFailureDegrades pins SPEC §2.8: a bad key file is rejected
// (never regenerated) but the daemon still starts, publisher-less.
func TestIdentityLoadFailureDegrades(t *testing.T) {
	cfg := testConfig(t)
	// Create the identity, then break its permissions.
	d1, err := New(context.Background(), Options{Cfg: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	pk := d1.Identity.PublicKeyHex()
	_ = d1.Close()
	before, err := os.ReadFile(cfg.IdentityPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg.IdentityPath, 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	d2, err := New(context.Background(), Options{
		Cfg:     cfg,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIAddr: "localhost:0",
		Stderr:  &stderr,
	})
	if err != nil {
		t.Fatalf("identity failure must degrade, not abort: %v", err)
	}
	defer d2.Close()
	if d2.Identity != nil {
		t.Fatal("bad key file must not load")
	}
	if !strings.Contains(stderr.String(), "warning: identity load failed:") ||
		!strings.Contains(stderr.String(), "insecure permissions") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	// The rejected file is untouched — never regenerated.
	after, _ := os.ReadFile(cfg.IdentityPath)
	if string(before) != string(after) {
		t.Fatal("rejected key file was modified")
	}
	// /status omits the pubkey.
	resp, err := http.Get("http://" + d2.API.Addr() + "/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "pubkey") {
		t.Fatalf("degraded /status must omit pubkey: %s", body)
	}
	// Restoring the mode restores the ORIGINAL identity (proves no mint).
	if err := os.Chmod(cfg.IdentityPath, 0o600); err != nil {
		t.Fatal(err)
	}
	d3, err := New(context.Background(), Options{Cfg: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer d3.Close()
	if d3.Identity == nil || d3.Identity.PublicKeyHex() != pk {
		t.Fatal("original identity not recovered after chmod 0600")
	}
}

func TestEmptyIdentityPathDisables(t *testing.T) {
	cfg := testConfig(t)
	path := cfg.IdentityPath
	cfg.IdentityPath = ""
	d, err := New(context.Background(), Options{Cfg: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Identity != nil {
		t.Fatal("empty IdentityPath must disable identity")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("disabled identity must not create a key file")
	}
}

// TestNonDefaultIdentityPathIsLoadOnly pins the single enforcement site:
// a configured non-default path never auto-creates.
func TestNonDefaultIdentityPathIsLoadOnly(t *testing.T) {
	cfg := testConfig(t)
	cfg.IdentityPath = filepath.Join(t.TempDir(), "elsewhere.key")
	var stderr bytes.Buffer
	d, err := New(context.Background(), Options{
		Cfg:    cfg,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Identity != nil {
		t.Fatal("non-default path must not auto-create")
	}
	if _, err := os.Stat(cfg.IdentityPath); !os.IsNotExist(err) {
		t.Fatal("key file was created at a non-default path")
	}
	if !strings.Contains(stderr.String(), "load-only") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestUncleanDefaultPathStillAutoCreates pins the path-based rule under
// non-canonical spellings: a /./-spelled default path must count as default.
func TestUncleanDefaultPathStillAutoCreates(t *testing.T) {
	cfg := testConfig(t)
	dir := filepath.Dir(cfg.IdentityPath)
	cfg.IdentityPath = dir + string(filepath.Separator) + "." + string(filepath.Separator) + "identity.key"
	d, err := New(context.Background(), Options{Cfg: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Identity == nil {
		t.Fatal("unclean spelling of the default path must still auto-create")
	}
}

// TestCloseOrder pins the load-bearing teardown contract: the daemon-owned
// background context is cancelled and its goroutines joined strictly BEFORE
// any subsystem (here: the API) is stopped.
func TestCloseOrder(t *testing.T) {
	ev := &eventLog{}
	d, err := New(context.Background(), Options{
		Cfg:     testConfig(t),
		Log:     slog.New(ev),
		APIAddr: "localhost:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.API == nil {
		t.Fatal("API not started")
	}
	addr := d.API.Addr()

	started := make(chan struct{})
	d.goBG(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // force Close to actually wait
		ev.add("bg-exited")
	})
	<-started

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	events := ev.list()
	idx := func(name string) int {
		for i, e := range events {
			if e == name {
				return i
			}
		}
		t.Fatalf("event %q missing from %v", name, events)
		return -1
	}
	begin := idx("log:daemon.close_begin")
	bgExited := idx("bg-exited")
	joined := idx("log:daemon.bg_joined")
	apiStopped := idx("log:httpapi.stopped")
	engStopped := idx("log:engine.stopped")
	done := idx("log:daemon.close_done")
	if !(begin < bgExited && bgExited < joined && joined < apiStopped && apiStopped < engStopped && engStopped < done) {
		t.Fatalf("teardown order wrong: %v", events)
	}

	// The API must actually be down.
	if resp, err := http.Get("http://" + addr + "/status"); err == nil {
		resp.Body.Close()
		t.Fatal("API still serving after Close")
	}
}

// TestGoBGAfterCloseIsNoOp pins the registration/teardown race fix: goBG
// concurrent with or after Close must never spawn a goroutine the join
// cannot see (a bare Add racing Wait is a data race and an unjoined leak).
func TestGoBGAfterCloseIsNoOp(t *testing.T) {
	d, err := New(context.Background(), Options{
		Cfg: testConfig(t),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Hammer registration concurrently with Close; -race guards the Add/Wait pair.
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.goBG(func(ctx context.Context) { <-ctx.Done() })
		}()
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	ran := make(chan struct{})
	d.goBG(func(context.Context) { close(ran) })
	select {
	case <-ran:
		t.Fatal("goBG after Close must be a no-op")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCloseIdempotent(t *testing.T) {
	ev := &eventLog{}
	d, err := New(context.Background(), Options{
		Cfg:     testConfig(t),
		Log:     slog.New(ev),
		APIAddr: "localhost:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("repeated Close must return the cached (nil) error, got %v", err)
	}
	var begins int
	for _, e := range ev.list() {
		if e == "log:daemon.close_begin" {
			begins++
		}
	}
	if begins != 1 {
		t.Fatalf("teardown ran %d times, want once", begins)
	}
}
