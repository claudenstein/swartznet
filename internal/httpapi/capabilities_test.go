package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

// fakeCaps is an in-package CapabilitiesController double.
type fakeCaps struct {
	sharing   SharingPrefs
	publisher bool
}

func (f *fakeCaps) Sharing() SharingPrefs     { return f.sharing }
func (f *fakeCaps) SetSharing(p SharingPrefs) { f.sharing = p }
func (f *fakeCaps) Publisher() bool           { return f.publisher }

// liveMask mimics ltepwire.Announced for the operator bits + a fixed publisher
// bit, computed inline so the httpapi test imports no contract package.
func (f *fakeCaps) liveMask() uint64 {
	var m uint64
	switch f.sharing.ShareLocal {
	case 2:
		m |= 1 << 0
	case 1:
		m |= 1 << 1
	}
	if f.sharing.FileHits {
		m |= 1 << 2
	}
	if f.sharing.ContentHits {
		m |= 1 << 3
	}
	if f.publisher {
		m |= 1 << 4
	}
	// build bits 5,6,7,9 always on (0x2E0)
	m |= 0x2E0
	return m
}

func capServer(t *testing.T, caps *fakeCaps) string {
	t.Helper()
	s := NewWithOptions("localhost:0", slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		Capabilities:     caps,
		ServicesReporter: caps.liveMask,
	})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})
	return s.Addr()
}

func getCaps(t *testing.T, addr string) CapabilitiesResponse {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /capabilities = %d, want 200", resp.StatusCode)
	}
	var out CapabilitiesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func patchCaps(t *testing.T, addr, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, "http://"+addr+"/capabilities", strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestGetCapabilitiesReflectsState(t *testing.T) {
	addr := capServer(t, &fakeCaps{sharing: SharingPrefs{ShareLocal: 2, FileHits: true, ContentHits: true}, publisher: true})
	got := getCaps(t, addr)
	if got.ShareLocal != 2 || !got.FileHits || !got.ContentHits {
		t.Errorf("sharing not reflected: %+v", got)
	}
	if !got.Publisher {
		t.Error("publisher fact not reflected")
	}
	if got.Services != "00000000000002fd" {
		t.Errorf("services = %q, want 00000000000002fd", got.Services)
	}
}

// TestPatchDoesNotClobberPublisher is the §6 defect-#2 fix: a PATCH that omits
// publisher (there is no such field) leaves the daemon-owned bit untouched.
func TestPatchDoesNotClobberPublisher(t *testing.T) {
	caps := &fakeCaps{sharing: SharingPrefs{ShareLocal: 2, FileHits: true, ContentHits: true}, publisher: true}
	addr := capServer(t, caps)
	resp := patchCaps(t, addr, `{"share_local":0}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PATCH = %d: %s", resp.StatusCode, b)
	}
	var out CapabilitiesResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.ShareLocal != 0 {
		t.Errorf("share_local not applied: %d", out.ShareLocal)
	}
	if !out.FileHits || !out.ContentHits {
		t.Errorf("preserve-unset failed: file/content changed: %+v", out)
	}
	if !out.Publisher {
		t.Error("PATCH clobbered the daemon-owned Publisher bit (the §6 defect)")
	}
	if !caps.publisher {
		t.Error("underlying publisher fact was mutated")
	}
	// Services dropped bit 0 (share_local 2→0) but kept bits 2,3 (file/content
	// still on), bit 4 (publisher), and the build bits: 0x2E0|4|8|0x10 = 0x2FC.
	if out.Services != "00000000000002fc" {
		t.Errorf("services after downgrade = %q, want 00000000000002fc", out.Services)
	}
}

func TestPatchMergePreservesUnsetFields(t *testing.T) {
	caps := &fakeCaps{sharing: SharingPrefs{ShareLocal: 2, FileHits: true, ContentHits: true}}
	addr := capServer(t, caps)
	resp := patchCaps(t, addr, `{"file_hits":false}`)
	defer resp.Body.Close()
	var out CapabilitiesResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.ShareLocal != 2 || out.FileHits || !out.ContentHits {
		t.Errorf("merge changed the wrong fields: %+v", out)
	}
}

func TestPatchClampsShareLocal(t *testing.T) {
	caps := &fakeCaps{sharing: SharingPrefs{ShareLocal: 2}}
	addr := capServer(t, caps)
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"share_local":9}`, 2},
		{`{"share_local":-3}`, 0},
	} {
		resp := patchCaps(t, addr, tc.body)
		var out CapabilitiesResponse
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if out.ShareLocal != tc.want {
			t.Errorf("%s → share_local %d, want %d", tc.body, out.ShareLocal, tc.want)
		}
	}
}

func TestPostCapabilitiesAlias(t *testing.T) {
	caps := &fakeCaps{sharing: SharingPrefs{ShareLocal: 2, FileHits: true}}
	addr := capServer(t, caps)
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/capabilities", strings.NewReader(`{"share_local":1}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST alias = %d, want 200", resp.StatusCode)
	}
	if caps.sharing.ShareLocal != 1 {
		t.Errorf("POST alias did not apply: %d", caps.sharing.ShareLocal)
	}
}

func TestCapabilitiesUnconfigured503(t *testing.T) {
	s := NewWithOptions("localhost:0", slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})
	resp, err := http.Get("http://" + s.Addr() + "/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /capabilities unconfigured = %d, want 503", resp.StatusCode)
	}
}

func TestPatchCapabilitiesBadJSON400(t *testing.T) {
	addr := capServer(t, &fakeCaps{})
	resp := patchCaps(t, addr, `{not json`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PATCH bad json = %d, want 400", resp.StatusCode)
	}
}

// TestPatchCapabilitiesCrossOriginRejected confirms the state-changing route
// is behind the CSRF guard.
func TestPatchCapabilitiesCrossOriginRejected(t *testing.T) {
	addr := capServer(t, &fakeCaps{})
	req, _ := http.NewRequest(http.MethodPatch, "http://"+addr+"/capabilities", strings.NewReader(`{"share_local":1}`))
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin PATCH = %d, want 403", resp.StatusCode)
	}
}
