// HTTPS anchor fallback — SPEC.md §3.5 last-ditch path.
//
// When channels A/B/C all return nothing (no PPMIs fetched, no
// crawl candidates admitted, no endorsements received), the
// subscriber fetches the current anchor pubkey list from a
// project-operated HTTPS endpoint and retries channel A against
// the updated list.
//
// The endpoint serves ONLY pubkeys, never records — compromising
// it cannot poison the local index, only delay bootstrap. This
// is the one HTTPS dependency in the entire Aggregate design;
// see PROPOSAL.md §3.5 for the threat-model rationale.

package daemon

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultBootstrapURL is the project-operated HTTPS endpoint
// fetched by FallbackToHTTPS. Empty during development; real
// releases populate this with the project's bootstrap host.
var DefaultBootstrapURL = ""

// MaxAnchorBootstrapBytes caps the HTTPS response to prevent a
// hostile endpoint from exhausting memory with a huge payload.
// A well-formed response is a JSON array of ~5 hex strings ≈ 500
// bytes; 64 KiB is an overkill ceiling that still catches abuse.
const MaxAnchorBootstrapBytes = 64 * 1024

// bootstrapAnchorsResponse is the JSON shape served at
// DefaultBootstrapURL. Future fields MUST be optional so older
// subscribers can continue to parse what they need.
type bootstrapAnchorsResponse struct {
	Version int      `json:"version"`
	Anchors []string `json:"anchors"` // 64-char hex pubkeys
	// Comment is optional — real deployments may describe the
	// release window or rotation schedule. Clients ignore it.
	Comment string `json:"comment,omitempty"`
}

// HTTPSFallbackClient is the interface FallbackToHTTPS uses to
// reach the endpoint. Abstracted so tests can inject a fake
// transport without standing up a real HTTP server. Production
// uses http.Client{Timeout: ...}.
type HTTPSFallbackClient interface {
	Get(ctx context.Context, url string) ([]byte, error)
}

// httpGetClient adapts a *http.Client to HTTPSFallbackClient.
type httpGetClient struct{ c *http.Client }

func (g httpGetClient) Get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: bootstrap endpoint %d %s",
			resp.StatusCode, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, MaxAnchorBootstrapBytes+1))
}

// NewHTTPSFallbackClient returns a production HTTPSFallbackClient
// with a sane default timeout. Callers that want more control
// should construct their own adapter.
func NewHTTPSFallbackClient(timeout time.Duration) HTTPSFallbackClient {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return httpGetClient{c: &http.Client{Timeout: timeout}}
}

// FallbackToHTTPS is the last-ditch channel: GET the bootstrap
// endpoint, parse its anchors list, and replace the Bootstrap's
// anchor set with the fetched pubkeys. Returns the count of
// anchors newly added to the bootstrap.
//
// This routine does NOT fetch PPMIs itself — after replacing the
// anchor list, the caller SHOULD call b.RunAnchors(ctx) again.
// That split lets tests inject a URL + client and observe the
// post-fetch state without hitting the network.
//
// Safe to call repeatedly (idempotent re-anchor). Returns an
// error if the URL is empty, the HTTP get fails, or the response
// is malformed.
func (b *Bootstrap) FallbackToHTTPS(ctx context.Context, url string, client HTTPSFallbackClient) (int, error) {
	if url == "" {
		return 0, errors.New("daemon: FallbackToHTTPS requires a URL")
	}
	if client == nil {
		client = NewHTTPSFallbackClient(0)
	}

	body, err := client.Get(ctx, url)
	if err != nil {
		return 0, fmt.Errorf("daemon: HTTPS bootstrap get: %w", err)
	}
	if len(body) > MaxAnchorBootstrapBytes {
		return 0, fmt.Errorf("daemon: bootstrap response %d bytes exceeds cap %d",
			len(body), MaxAnchorBootstrapBytes)
	}

	var resp bootstrapAnchorsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("daemon: decode bootstrap response: %w", err)
	}

	// Parse each hex anchor, dedupe against existing keys, extend.
	b.mu.Lock()
	existing := make(map[[32]byte]struct{}, len(b.anchorKeys))
	for _, k := range b.anchorKeys {
		existing[k] = struct{}{}
	}
	b.mu.Unlock()

	added := 0
	for _, h := range resp.Anchors {
		if h == "" {
			continue
		}
		raw, err := hex.DecodeString(h)
		if err != nil || len(raw) != 32 {
			continue // skip malformed entries silently
		}
		var pub [32]byte
		copy(pub[:], raw)
		if _, ok := existing[pub]; ok {
			continue
		}
		existing[pub] = struct{}{}
		b.mu.Lock()
		b.anchorKeys = append(b.anchorKeys, pub)
		b.mu.Unlock()
		added++
	}
	return added, nil
}
