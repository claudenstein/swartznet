// Smoke-check the embedded web UI carries the v0.5 Aggregate
// card. Since the JS is not unit-tested in a browser, this guards
// against regressions where a refactor silently drops the card's
// render block or the /aggregate fetch from app.js.

package web

import (
	"io/fs"
	"strings"
	"testing"
)

func readEmbedded(t *testing.T, path string) string {
	t.Helper()
	b, err := fs.ReadFile(Assets(), path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// The Aggregate card must render a visible title and fetch the
// /aggregate endpoint. We assert on the source string content —
// cheap, deterministic, and immune to stylistic refactors that
// don't touch these names.
func TestAggregateCardPresentInBundle(t *testing.T) {
	js := readEmbedded(t, "static/app.js")

	wants := []string{
		"/aggregate",            // fetch path
		"Aggregate (v0.5)",      // card title
		"PPMI enabled",          // row label
		"record source",         // row label
		"cache size",            // row label
		"agg.ppmi_enabled",      // field read
		"agg.known_indexers",    // field read
		"agg.record_source_kind",// field read
		"agg.record_cache_size", // field read
	}
	for _, w := range wants {
		if !strings.Contains(js, w) {
			t.Errorf("embedded app.js missing substring %q — Aggregate card regression?", w)
		}
	}
}

// Sanity: older cards still present. Catches accidental removal.
func TestStatusCardsPresentInBundle(t *testing.T) {
	js := readEmbedded(t, "static/app.js")
	for _, w := range []string{
		"Local index (L)",
		"Swarm peers (S)",
		"DHT publisher (D)",
	} {
		if !strings.Contains(js, w) {
			t.Errorf("embedded app.js missing card title %q", w)
		}
	}
}

// /aggregate fetch must use .catch(() => null) so an older daemon
// returning 404 doesn't break the whole status refresh.
func TestAggregateFetchTolerantOfMissingEndpoint(t *testing.T) {
	js := readEmbedded(t, "static/app.js")
	// The pattern we care about is on one logical line in the
	// Promise.all array: getJSON('/aggregate').catch(() => null)
	// Look for the fragment to prove the tolerance is there.
	if !strings.Contains(js, "getJSON('/aggregate').catch(") {
		t.Errorf("embedded app.js must call getJSON('/aggregate').catch(...) to tolerate older daemons")
	}
}
