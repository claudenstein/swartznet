package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func postCreate(t *testing.T, addr string, body CreateTorrentRequest) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "http://"+addr+"/torrents/create", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:7654") // same-origin for the CSRF guard
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestCreateTorrentEndpoint(t *testing.T) {
	t.Parallel()

	// nil collaborator -> 503
	addr := startSearchServer(t, Options{})
	resp := postCreate(t, addr, CreateTorrentRequest{Root: "/x", Output: "/x.torrent"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("nil collaborator: status = %d, want 503", resp.StatusCode)
	}

	// wired collaborator
	addr2 := startSearchServer(t, Options{CreateTorrent: func(p CreateTorrentParams) (CreateTorrentResult, error) {
		if p.Root == "bad" {
			return CreateTorrentResult{}, errors.New("boom")
		}
		return CreateTorrentResult{InfoHash: "deadbeef", Seeded: p.Seed}, nil
	}})

	// empty root/output -> 400
	r := postCreate(t, addr2, CreateTorrentRequest{Root: "", Output: ""})
	r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty paths: status = %d, want 400", r.StatusCode)
	}

	// success -> 200 with infohash, seeded echoed
	r = postCreate(t, addr2, CreateTorrentRequest{Root: "/data/movie", Output: "/data/movie.torrent", Seed: true})
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("success: status = %d, want 200", r.StatusCode)
	}
	var res CreateTorrentResult
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.InfoHash != "deadbeef" || !res.Seeded || res.Output != "/data/movie.torrent" {
		t.Fatalf("result = %+v, want OK deadbeef seeded output", res)
	}

	// collaborator error -> 400 with the message
	r2 := postCreate(t, addr2, CreateTorrentRequest{Root: "bad", Output: "/x.torrent"})
	r2.Body.Close()
	if r2.StatusCode != http.StatusBadRequest {
		t.Fatalf("collaborator error: status = %d, want 400", r2.StatusCode)
	}
}

// TestCreateTorrentPartialSeedFailure pins the review fix: a create that
// succeeded but whose seed leg failed is a partial success — 200 with the valid
// infohash + a seed_error warning, NOT a 400 that discards the created torrent.
func TestCreateTorrentPartialSeedFailure(t *testing.T) {
	t.Parallel()
	addr := startSearchServer(t, Options{CreateTorrent: func(p CreateTorrentParams) (CreateTorrentResult, error) {
		return CreateTorrentResult{InfoHash: "cafe", SeedError: "seed add rejected"}, nil
	}})
	r := postCreate(t, addr, CreateTorrentRequest{Root: "/x", Output: "/x.torrent", Seed: true})
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("partial success: status = %d, want 200", r.StatusCode)
	}
	var res CreateTorrentResult
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.InfoHash != "cafe" || res.Seeded || res.SeedError == "" {
		t.Fatalf("result = %+v, want OK cafe not-seeded with a seed_error", res)
	}
}
