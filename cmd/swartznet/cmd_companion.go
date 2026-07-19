package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// cmdCompanion manages the companion content-index over the daemon's HTTP API:
// show status, follow/unfollow a publisher pubkey, or force a re-publish.
func cmdCompanion(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: swartznet companion <status|follow|unfollow|refresh> [flags]")
		return exitUsage
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, "usage: swartznet companion <status|follow|unfollow|refresh> [flags]")
		return exitOK
	case "status":
		return companionStatus(rest, stdout, stderr)
	case "follow":
		return companionFollow("follow", rest, stdout, stderr)
	case "unfollow":
		return companionFollow("unfollow", rest, stdout, stderr)
	case "refresh":
		return companionRefresh(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "swartznet companion: unknown subcommand %q\n", sub)
		fmt.Fprintln(stderr, "usage: swartznet companion <status|follow|unfollow|refresh> [flags]")
		return exitUsage
	}
}

func companionStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("companion status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiAddr := fs.String("api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	asJSON := fs.Bool("json", false, "emit JSON instead of text")
	if err := fs.Parse(args); err != nil {
		return parseErrExit(err)
	}
	raw, code := companionGet(*apiAddr, "/companion", stderr)
	if code != exitOK {
		return code
	}
	if *asJSON {
		var pretty bytes.Buffer
		if json.Indent(&pretty, raw, "", "  ") == nil {
			stdout.Write(pretty.Bytes())
			fmt.Fprintln(stdout)
		} else {
			stdout.Write(raw)
		}
		return exitOK
	}
	var out httpapi.CompanionStatusResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return reportRunErr(err, stderr)
	}
	p := out.Publisher
	fmt.Fprintln(stdout, "Companion publisher:")
	if p.PubKeyHex == "" {
		fmt.Fprintln(stdout, "  (not started — needs an identity, the DHT, and a companion dir)")
	} else {
		fmt.Fprintf(stdout, "  pubkey:    %s\n", p.PubKeyHex)
		fmt.Fprintf(stdout, "  published: %d time(s)\n", p.PublishedCount)
		if p.LastInfoHash != "" {
			fmt.Fprintf(stdout, "  last:      %s\n", p.LastInfoHash)
		}
		if !p.LastRefresh.IsZero() {
			fmt.Fprintf(stdout, "  refreshed: %s\n", p.LastRefresh.Format(time.RFC3339))
		}
		if p.LastError != "" {
			fmt.Fprintf(stdout, "  error:     %s\n", p.LastError)
		}
	}
	fmt.Fprintf(stdout, "\nFollowing %d publisher(s):\n", len(out.Subscriber))
	for _, f := range out.Subscriber {
		label := f.Label
		if label != "" {
			label = " (" + label + ")"
		}
		fmt.Fprintf(stdout, "  %s%s  torrents=%d content=%d\n", f.PubKeyHex, label, f.TorrentsImported, f.ContentImported)
		if f.LastError != "" {
			fmt.Fprintf(stdout, "      last error: %s\n", f.LastError)
		}
	}
	return exitOK
}

func companionFollow(action string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("companion "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiAddr := fs.String("api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	label := fs.String("label", "", "optional human label for the followed publisher (follow only)")
	// Accept the pubkey before OR after flags, and collect ALL positionals so an
	// extra one (a second pubkey) is a usage error rather than silently dropped
	// along with any post-positional flags (which would then hit the default API).
	pos, err := parseFlagsAllowingLeadingPositionals(fs, args)
	if err != nil {
		return parseErrExit(err)
	}
	if len(pos) != 1 {
		fmt.Fprintf(stderr, "usage: swartznet companion %s <pubkey-hex> [--label <name>]\n", action)
		return exitUsage
	}
	pubkey := strings.ToLower(strings.TrimSpace(pos[0]))
	if len(pubkey) != 64 {
		fmt.Fprintln(stderr, "swartznet: pubkey must be 64 hex characters")
		return exitUsage
	}
	body, _ := json.Marshal(map[string]string{"pubkey": pubkey, "label": *label})
	raw, code := companionPost(*apiAddr, "/companion/"+action, body, stderr)
	if code != exitOK {
		return code
	}
	_ = raw
	fmt.Fprintf(stdout, "%sed %s\n", action, pubkey)
	return exitOK
}

func companionRefresh(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("companion refresh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiAddr := fs.String("api-addr", "localhost:7654", "address of the running swartznet HTTP API")
	if err := fs.Parse(args); err != nil {
		return parseErrExit(err)
	}
	_, code := companionPost(*apiAddr, "/companion/refresh", []byte("{}"), stderr)
	if code != exitOK {
		return code
	}
	fmt.Fprintln(stdout, "companion re-publish triggered")
	return exitOK
}

func companionGet(apiAddr, path string, stderr io.Writer) ([]byte, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+apiAddr+path, nil)
	if err != nil {
		return nil, reportRunErr(err, stderr)
	}
	return companionDo(req, apiAddr, stderr)
}

func companionPost(apiAddr, path string, body []byte, stderr io.Writer) ([]byte, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+apiAddr+path, bytes.NewReader(body))
	if err != nil {
		return nil, reportRunErr(err, stderr)
	}
	req.Header.Set("Content-Type", "application/json")
	return companionDo(req, apiAddr, stderr)
}

func companionDo(req *http.Request, apiAddr string, stderr io.Writer) ([]byte, int) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: cannot reach the daemon at %s (%v)\n", apiAddr, err)
		fmt.Fprintln(stderr, "start it with: swartznet add <magnet>")
		return nil, exitRuntime
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "swartznet: api status %d: %s\n", resp.StatusCode, strings.TrimSpace(string(raw)))
		return nil, exitRuntime
	}
	return raw, exitOK
}
