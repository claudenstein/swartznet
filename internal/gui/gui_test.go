package gui

import (
	"errors"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/searchmux"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// newTestSearchTab builds a search tab under the Fyne test driver with no
// daemon (buildResults never touches it).
func newTestSearchTab(t *testing.T) *searchTab {
	t.Helper()
	test.NewApp()
	t.Cleanup(func() { test.NewApp() })
	return newSearchTab(nil, nil)
}

func cardCount(st *searchTab) int {
	n := 0
	for _, o := range st.resultBox.Objects {
		if _, ok := o.(*widget.Card); ok {
			n++
		}
	}
	return n
}

// TestSearchRendersPerLayerCards is the DoD surface: buildResults renders one
// card per hit across all three native response types, never merging them, and
// writes a per-layer L/S/D status line.
func TestSearchRendersPerLayerCards(t *testing.T) {
	st := newTestSearchTab(t)
	st.swarmChk.SetChecked(true)
	st.dhtChk.SetChecked(true)

	res := searchmux.Result{
		Local: &indexer.SearchResponse{Total: 2, Hits: []indexer.SearchHit{
			{DocType: "torrent", InfoHash: strings.Repeat("a", 40), Name: "ubuntu", Score: 1.5, SizeBytes: 4096, SignedBy: strings.Repeat("b", 64)},
			{DocType: "content", InfoHash: strings.Repeat("a", 40), FilePath: "notes.txt", Score: 0.9},
		}},
		Swarm: &swarmsearch.QueryResponse{Asked: 3, Responded: 2, Hits: []swarmsearch.MergedHit{
			{InfoHash: strings.Repeat("c", 40), Name: "debian", Score: 50, Seeders: 7, Sources: []string{"p1"}},
		}},
		DHT: &dhtindex.LookupResponse{IndexersAsked: 2, IndexersResponded: 1, Hits: []dhtindex.LookupHit{
			{InfoHash: strings.Repeat("d", 40), Name: "fedora", Score: 0.7, Seeders: 3, Sources: []string{"i1"}},
		}},
	}
	st.buildResults(res)

	if got := cardCount(st); got != 4 { // 2 local + 1 swarm + 1 dht
		t.Fatalf("card count = %d, want 4 (2L+1S+1D)", got)
	}
	status := st.statusLbl.Text
	for _, want := range []string{"Local: 2 hits", "Swarm: 1 hits", "DHT: 1 hits", "indexers=1/2"} {
		if !strings.Contains(status, want) {
			t.Errorf("status %q missing %q", status, want)
		}
	}
}

// TestSearchLayerErrorRendersInline: a Layer-D error renders inline in the
// status line, never as a fatal panic — presentation mirrors §5.9.
func TestSearchLayerErrorRendersInline(t *testing.T) {
	st := newTestSearchTab(t)
	st.dhtChk.SetChecked(true)
	st.buildResults(searchmux.Result{
		Local:  &indexer.SearchResponse{Total: 0, Hits: nil},
		DHTErr: errors.New("dht unreachable"),
	})
	if !strings.Contains(st.statusLbl.Text, "DHT: error: dht unreachable") {
		t.Errorf("status = %q, want inline DHT error", st.statusLbl.Text)
	}
}

// TestSearchSwarmDHTOmittedWhenUnchecked: a layer that wasn't requested is not
// rendered even if the Result carries it.
func TestSearchLayersGatedOnChecks(t *testing.T) {
	st := newTestSearchTab(t) // swarm + dht unchecked
	st.buildResults(searchmux.Result{
		Local: &indexer.SearchResponse{Total: 1, Hits: []indexer.SearchHit{{DocType: "torrent", InfoHash: strings.Repeat("a", 40), Name: "x"}}},
		Swarm: &swarmsearch.QueryResponse{Hits: []swarmsearch.MergedHit{{InfoHash: strings.Repeat("c", 40)}}},
		DHT:   &dhtindex.LookupResponse{Hits: []dhtindex.LookupHit{{InfoHash: strings.Repeat("d", 40)}}},
	})
	if cardCount(st) != 1 {
		t.Errorf("unchecked swarm/dht layers rendered: %d cards, want 1", cardCount(st))
	}
	if strings.Contains(st.statusLbl.Text, "Swarm") || strings.Contains(st.statusLbl.Text, "DHT") {
		t.Errorf("status shows unchecked layers: %q", st.statusLbl.Text)
	}
}

// TestAboutLicenseIsApache pins the §6 fix: first-party license is Apache-2.0,
// not MIT.
func TestAboutLicenseIsApache(t *testing.T) {
	if !strings.Contains(licenseLine, "Apache-2.0") {
		t.Errorf("license %q must state Apache-2.0 for first-party code", licenseLine)
	}
	if strings.Contains(licenseLine, "MIT") {
		t.Errorf("license %q must NOT claim MIT (the §6 defect)", licenseLine)
	}
}

func TestParseSearchLimit(t *testing.T) {
	cases := map[string]int{"": 20, "  ": 20, "0": 20, "-5": 20, "abc": 20, "7": 7, " 15 ": 15}
	for in, want := range cases {
		if got := parseSearchLimit(in); got != want {
			t.Errorf("parseSearchLimit(%q) = %d, want %d", in, got, want)
		}
	}
}
