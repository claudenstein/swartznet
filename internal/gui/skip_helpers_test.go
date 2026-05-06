package gui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/daemon"
)

// skipMissing reports a Skip with a diagnostic that names every
// expected daemon subsystem and whether it wired. Replaces the
// bare `t.Skip("daemon did not wire up X")` pattern: the bare
// form gave no signal about whether the failure was X-specific
// (a real regression to investigate) or environmental (DHT
// unavailable, so everything is nil). Showing the full wiring
// state makes the difference obvious in `go test -v` output and
// in any CI log that surfaces SKIP messages.
//
// `missing` is the subsystem the test needs but found nil; the
// helper formats a single message of the form:
//
//	skipping: missing CompSub — wired{Index=true Eng=true CompPub=false CompSub=false API=false Bootstrap=false}
//
// so a developer reading the log can spot global vs targeted
// wiring failures at a glance. Always calls t.Skipf, never
// t.Fatal — these tests document optional integrations that
// genuinely depend on the host (UDP availability, DHT
// reachability) and must remain robust to absence.
func skipMissing(t *testing.T, d *daemon.Daemon, missing string) {
	t.Helper()
	if d == nil {
		t.Skipf("skipping: missing %s — daemon is nil", missing)
		return
	}
	state := []string{
		fmt.Sprintf("Eng=%t", d.Eng != nil),
		fmt.Sprintf("Index=%t", d.Index != nil),
		fmt.Sprintf("CompPub=%t", d.CompPub != nil),
		fmt.Sprintf("CompSub=%t", d.CompSub != nil),
		fmt.Sprintf("API=%t", d.API != nil),
		fmt.Sprintf("Bootstrap=%t", d.Bootstrap != nil),
	}
	t.Skipf("skipping: missing %s — wired{%s}", missing, strings.Join(state, " "))
}
