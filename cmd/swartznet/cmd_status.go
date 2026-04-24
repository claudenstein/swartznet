package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// cmdStatus implements `swartznet status`. It hits the running
// daemon's GET /status endpoint and prints a human-readable
// summary of the local index, swarm peer set, and DHT publisher.
//
// Useful as the first thing the user runs after `swartznet add`
// to verify everything is wired up.
func cmdStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		apiAddr string
		asJSON  bool
	)
	fs.StringVar(&apiAddr, "api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	fs.BoolVar(&asJSON, "json", false, "emit JSON instead of text")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+apiAddr+"/status", nil)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: cannot reach the daemon at %s (%v)\n", apiAddr, err)
		fmt.Fprintln(stderr, "start it with: swartznet add <magnet>")
		return exitRuntime
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "swartznet: api status %d: %s\n", resp.StatusCode, data)
		return exitRuntime
	}

	var out httpapi.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return reportRunErr(err, stderr)
	}

	// Fetch the Aggregate block best-effort — older daemons
	// won't expose /aggregate so a 404 or decode error is
	// silent-skipped rather than fatal.
	agg := fetchAggregateBlock(ctx, apiAddr)

	if asJSON {
		// Combine both payloads into a single JSON object so the
		// --json output stays a single document.
		combined := struct {
			Status    *httpapi.StatusResponse          `json:"status"`
			Aggregate *httpapi.AggregateStatusResponse `json:"aggregate,omitempty"`
		}{
			Status:    &out,
			Aggregate: agg,
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(combined); err != nil {
			return reportRunErr(err, stderr)
		}
		return exitOK
	}
	return emitStatusText(stdout, &out, agg)
}

// fetchAggregateBlock does a best-effort GET /aggregate. Returns
// nil on any failure; the text/JSON renderers handle nil by
// omitting the Aggregate block cleanly.
func fetchAggregateBlock(ctx context.Context, apiAddr string) *httpapi.AggregateStatusResponse {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+apiAddr+"/aggregate", nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var a httpapi.AggregateStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		return nil
	}
	return &a
}

func emitStatusText(w io.Writer, s *httpapi.StatusResponse, agg *httpapi.AggregateStatusResponse) int {
	fmt.Fprintln(w, "SwartzNet daemon status")
	fmt.Fprintln(w, "")

	fmt.Fprintln(w, "Local index (Layer L):")
	if s.Local.Indexed {
		fmt.Fprintf(w, "  enabled, %d documents\n", s.Local.DocCount)
	} else {
		fmt.Fprintln(w, "  not configured")
	}
	fmt.Fprintln(w, "")

	fmt.Fprintln(w, "Swarm search (Layer S, sn_search BEP-10 extension):")
	fmt.Fprintf(w, "  known peers:    %d\n", s.Swarm.KnownPeers)
	fmt.Fprintf(w, "  capable peers:  %d\n", s.Swarm.CapablePeers)
	fmt.Fprintln(w, "")

	// DHT block is omitted from the response when the daemon
	// started with DisableDHT=true; nil here is the explicit
	// "DHT disabled" state.
	if s.DHT != nil {
		fmt.Fprintln(w, "DHT routing table:")
		fmt.Fprintf(w, "  good nodes:     %d\n", s.DHT.GoodNodes)
		fmt.Fprintf(w, "  total nodes:    %d\n", s.DHT.Nodes)
		fmt.Fprintln(w, "")
	}

	fmt.Fprintln(w, "DHT publisher (Layer D, BEP-44 keyword index):")
	if s.Publisher.PubKey != "" {
		fmt.Fprintf(w, "  pubkey:         %s\n", s.Publisher.PubKey)
	}
	fmt.Fprintf(w, "  total keywords: %d\n", s.Publisher.TotalKeywords)
	fmt.Fprintf(w, "  total hits:     %d\n", s.Publisher.TotalHits)
	if len(s.Publisher.Keywords) == 0 {
		fmt.Fprintln(w, "  (no keywords published yet)")
	} else {
		fmt.Fprintln(w, "  per-keyword:")
		for _, k := range s.Publisher.Keywords {
			state := "ok"
			if k.LastError != "" {
				state = "ERR: " + k.LastError
			}
			last := k.LastPublished
			if last == "" {
				last = "never"
			}
			fmt.Fprintf(w, "    %-20s hits=%-4d publishes=%-4d last=%-25s state=%s\n",
				k.Keyword, k.HitsCount, k.PublishCount, last, state)
		}
	}

	if agg != nil {
		emitAggregateBlock(w, agg)
	}
	return exitOK
}

// emitAggregateBlock renders the v0.5 Aggregate-track state
// underneath the main status body. Only visible when the daemon
// exposes /aggregate.
func emitAggregateBlock(w io.Writer, a *httpapi.AggregateStatusResponse) {
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Aggregate (Layer D v0.5, PPMI + B-tree + RIBLT):")
	fmt.Fprintf(w, "  PPMI enabled:         %v\n", a.PPMIEnabled)
	fmt.Fprintf(w, "  known indexers:       %d\n", a.KnownIndexers)
	if a.RecordSourceKind != "" {
		fmt.Fprintf(w, "  record source:        %s\n", a.RecordSourceKind)
	}
	if a.RecordCacheSize > 0 {
		fmt.Fprintf(w, "  record cache size:    %d\n", a.RecordCacheSize)
	}
	if a.ServicesAdvertised != "" {
		fmt.Fprintf(w, "  services advertised:  0x%s\n", a.ServicesAdvertised)
	}
}
