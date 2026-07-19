package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// capResp mirrors httpapi.CapabilitiesResponse for decoding in this package.
type capResp struct {
	ShareLocal  int    `json:"share_local"`
	FileHits    bool   `json:"file_hits"`
	ContentHits bool   `json:"content_hits"`
	Publisher   bool   `json:"publisher"`
	Services    string `json:"services"`
}

type aggResp struct {
	Services string `json:"services"`
}

// TestCapabilitiesAggregateLiveMask drives the full wiring engine → adapter →
// httpapi: GET reflects config, /aggregate renders the LIVE mask (not a static
// constant), a PATCH downgrade changes both readouts, and the daemon-owned
// Publisher bit is never clobbered by the operator PATCH.
func TestCapabilitiesAggregateLiveMask(t *testing.T) {
	cfg := testConfig(t) // NoIndex=true → Publishing=false → default mask 0x2ED
	d, err := New(context.Background(), Options{
		Cfg:     cfg,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIAddr: "localhost:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	addr := d.API.Addr()

	getCap := func() capResp {
		resp, err := http.Get("http://" + addr + "/capabilities")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET /capabilities = %d", resp.StatusCode)
		}
		var c capResp
		_ = json.NewDecoder(resp.Body).Decode(&c)
		return c
	}
	getAgg := func() aggResp {
		resp, err := http.Get("http://" + addr + "/aggregate")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var a aggResp
		_ = json.NewDecoder(resp.Body).Decode(&a)
		return a
	}

	// GET reflects config defaults; publisher false under --no-index.
	c := getCap()
	if c.ShareLocal != 2 || !c.FileHits || !c.ContentHits {
		t.Errorf("GET /capabilities defaults wrong: %+v", c)
	}
	if c.Publisher {
		t.Error("Publisher should be false under NoIndex config")
	}
	if c.Services != "00000000000000ed" {
		t.Errorf("initial services = %q, want 00000000000000ed", c.Services)
	}
	// /aggregate renders the same LIVE mask.
	if a := getAgg(); a.Services != "00000000000000ed" {
		t.Errorf("/aggregate services = %q, want live 00000000000000ed", a.Services)
	}

	// Downgrade to share-nothing; both readouts must change live.
	req, _ := http.NewRequest(http.MethodPatch, "http://"+addr+"/capabilities",
		strings.NewReader(`{"share_local":0,"file_hits":false,"content_hits":false}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("PATCH = %d", resp.StatusCode)
	}

	// Live proof: /aggregate now renders 0x2E0 (bits 0..3 cleared), NOT the
	// legacy static 0x2ED.
	if a := getAgg(); a.Services != "00000000000000e0" {
		t.Errorf("/aggregate after downgrade = %q, want live 00000000000000e0", a.Services)
	}
	c2 := getCap()
	if c2.ShareLocal != 0 || c2.FileHits || c2.ContentHits {
		t.Errorf("downgrade not applied: %+v", c2)
	}
	if c2.Publisher {
		t.Error("PATCH must not touch the daemon-owned Publisher bit")
	}
	if c2.Services != "00000000000000e0" {
		t.Errorf("/capabilities services after downgrade = %q, want 00000000000000e0", c2.Services)
	}
}
