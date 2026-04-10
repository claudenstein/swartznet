// Package httpapi is a minimal local-only HTTP server that lets one
// SwartzNet CLI invocation (typically `swartznet search --swarm`) ask
// a long-running `swartznet add` process to run a query. It is the
// simplest viable IPC between two swartznet subcommands.
//
// Scope of M3d:
//
//   - One endpoint: POST /search with a JSON body. Returns a JSON
//     result combining local Bleve hits (always) and swarm hits
//     (when the caller asks for them).
//   - Listens on a configurable address (default "localhost:7654"),
//     binding to loopback only. No auth; loopback-only is the
//     security model.
//
// Future milestones may grow this into a proper REST layer with
// torrent add/remove, status, and a web UI. For now it exists purely
// to make the end-to-end M3 demo runnable.
package httpapi
