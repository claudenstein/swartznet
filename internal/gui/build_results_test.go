package gui

import (
	"errors"
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestBuildResultsArms covers buildResults at search.go:193-251.
// Six arms reachable: local resp / local err, swarm resp / swarm
// err, dht resp / dht err. Plus the no-layers parts append. Card
// callbacks reference st.confirmHit/flagHit but aren't fired.
func TestBuildResultsArms(t *testing.T) {
	t.Parallel()
	st := &searchTab{
		resultBox:  container.NewVBox(),
		emptyState: container.NewVBox(),
		statusLbl:  widget.NewLabel(""),
	}

	// All three non-nil with hits.
	st.buildResults("ubuntu",
		&indexer.SearchResponse{
			Total: 1,
			Hits: []indexer.SearchHit{
				{Name: "ubuntu", InfoHash: "0123456789abcdef0123456789abcdef01234567", DocType: "torrent"},
			},
		}, nil,
		&swarmsearch.QueryResponse{
			Asked: 3, Responded: 2,
			Hits: []swarmsearch.MergedHit{
				{Name: "ubuntu", InfoHash: "0123456789abcdef0123456789abcdef01234567"},
			},
		}, nil,
		&dhtindex.LookupResponse{
			IndexersAsked: 5, IndexersResponded: 3,
			Hits: []dhtindex.LookupHit{
				{Name: "ubuntu", InfoHash: "0123456789abcdef0123456789abcdef01234567"},
			},
		}, nil,
	)

	// All three err arms.
	st.buildResults("ubuntu",
		nil, errors.New("local boom"),
		nil, errors.New("swarm boom"),
		nil, errors.New("dht boom"),
	)

	// No layers → "No search layers enabled" parts append.
	st.buildResults("ubuntu", nil, nil, nil, nil, nil, nil)
}
