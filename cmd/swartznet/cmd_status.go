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

// cmdStatus is a thin HTTP client over a running daemon's API. It never
// constructs a daemon of its own.
func cmdStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiAddr := fs.String("api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	asJSON := fs.Bool("json", false, "emit JSON instead of text")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+*apiAddr+"/status", nil)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: cannot reach the daemon at %s (%v)\n", *apiAddr, err)
		fmt.Fprintln(stderr, "start it with: swartznet add <magnet>")
		return exitRuntime
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(stderr, "swartznet: api status %d: %s\n", resp.StatusCode, body)
		return exitRuntime
	}

	var st httpapi.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return reportRunErr(err, stderr)
	}

	// Best-effort downloads fetch: any error silently omits the section, so
	// the thin client stays compatible with a controller-less daemon.
	downloads := fetchDownloads(*apiAddr)

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		// The envelope keeps the legacy CLI shape: {"status": ..., "aggregate":
		// ...} with aggregate omitted; the /aggregate fetch arrives with its slice.
		if err := enc.Encode(statusEnvelope{Status: st, Downloads: downloads}); err != nil {
			return reportRunErr(err, stderr)
		}
		return exitOK
	}
	emitStatusText(stdout, st)
	if downloads != nil {
		emitDownloadsText(stdout, downloads)
	}
	return exitOK
}

type statusEnvelope struct {
	Status    httpapi.StatusResponse    `json:"status"`
	Downloads *httpapi.TorrentsResponse `json:"downloads,omitempty"`
}

func fetchDownloads(apiAddr string) *httpapi.TorrentsResponse {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+apiAddr+"/torrents", nil)
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
	var out httpapi.TorrentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil
	}
	return &out
}

func emitDownloadsText(w io.Writer, d *httpapi.TorrentsResponse) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Downloads:")
	if len(d.Torrents) == 0 {
		fmt.Fprintln(w, "  (no torrents)")
		return
	}
	for _, t := range d.Torrents {
		name := t.Name
		if name == "" {
			// Defensive: the JSON comes from whatever --api-addr points at.
			if len(t.InfoHash) >= 16 {
				name = t.InfoHash[:16] + "…"
			} else {
				name = t.InfoHash
			}
		}
		fmt.Fprintf(w, "  %-12s %6.1f%%  %9s  %3d/%-3d  %s\n",
			t.Status, t.Progress*100, humanBytes(t.Size), t.ActivePeers, t.TotalPeers, name)
	}
}

// emitStatusText renders the human status view. Sections grow as their
// subsystems land; the DHT section renders only when the daemon reports the
// DHT block at all (absent block = DHT disabled, distinct from zero nodes).
func emitStatusText(w io.Writer, st httpapi.StatusResponse) {
	fmt.Fprintln(w, "SwartzNet daemon status")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Local index (Layer L):")
	if st.Local.Indexed {
		fmt.Fprintf(w, "  enabled, %d documents\n", st.Local.DocCount)
	} else {
		fmt.Fprintln(w, "  not configured")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Swarm search (Layer S, sn_search BEP-10 extension):")
	fmt.Fprintf(w, "  known peers:    %d\n", st.Swarm.KnownPeers)
	fmt.Fprintf(w, "  capable peers:  %d\n", st.Swarm.CapablePeers)
	fmt.Fprintln(w)
	if st.DHT != nil {
		fmt.Fprintln(w, "DHT routing table:")
		fmt.Fprintf(w, "  good nodes:     %d\n", st.DHT.GoodNodes)
		fmt.Fprintf(w, "  total nodes:    %d\n", st.DHT.Nodes)
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "DHT publisher (Layer D, BEP-44 keyword index):")
	if st.Publisher.PubKey != "" {
		fmt.Fprintf(w, "  pubkey:         %s\n", st.Publisher.PubKey)
	}
	fmt.Fprintf(w, "  total keywords: %d\n", st.Publisher.TotalKeywords)
	fmt.Fprintf(w, "  total hits:     %d\n", st.Publisher.TotalHits)
	if len(st.Publisher.Keywords) == 0 {
		fmt.Fprintln(w, "  (no keywords published yet)")
	} else {
		fmt.Fprintln(w, "  per-keyword:")
		for _, k := range st.Publisher.Keywords {
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
}
