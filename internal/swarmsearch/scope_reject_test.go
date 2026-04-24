package swarmsearch_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestHandleQueryScopeRejectC0 closes wire-compat matrix row 8.4-B:
// a C1 client asking a C0 responder for scope "c" must get a
// RejectUnsupportedScope (code 2) back.
//
// "C0" means ContentHits=0 in our Capabilities struct. The test
// also checks the two related paths that share the same policy:
//
//   - scope "c" on a C0 node → reject
//   - scope "f" on an F0 node (FileHits=0) → reject
//   - scope "n" on any node → serve (never rejected)
//   - empty scope on any node → serve ("responder's choice")
//
// Without this behavior a C1 querier would silently get only
// name hits when asking for content hits, breaking the spec
// claim that scope is a negotiated contract, not a hint.
func TestHandleQueryScopeRejectC0(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		caps       swarmsearch.Capabilities
		scope      string
		wantReject bool
	}{
		// 8.4-B proper: C1→C0 with scope "c" → reject 2.
		{
			name: "c_scope_on_c0_rejects",
			caps: swarmsearch.Capabilities{
				ShareLocal:  2,
				FileHits:    1,
				ContentHits: 0,
			},
			scope:      "c",
			wantReject: true,
		},
		// Mixed scope "nc" also rejects — we can't honour the 'c'.
		{
			name: "nc_scope_on_c0_rejects",
			caps: swarmsearch.Capabilities{
				ShareLocal:  2,
				FileHits:    1,
				ContentHits: 0,
			},
			scope:      "nc",
			wantReject: true,
		},
		// Sibling case: 'f' on an F0 node must also reject.
		{
			name: "f_scope_on_f0_rejects",
			caps: swarmsearch.Capabilities{
				ShareLocal:  2,
				FileHits:    0,
				ContentHits: 0,
			},
			scope:      "f",
			wantReject: true,
		},
		// Name-only query is always safe.
		{
			name: "n_scope_on_c0_serves",
			caps: swarmsearch.Capabilities{
				ShareLocal:  2,
				FileHits:    1,
				ContentHits: 0,
			},
			scope:      "n",
			wantReject: false,
		},
		// Empty scope is "responder's choice" and never rejects.
		{
			name: "empty_scope_on_c0_serves",
			caps: swarmsearch.Capabilities{
				ShareLocal:  2,
				FileHits:    1,
				ContentHits: 0,
			},
			scope:      "",
			wantReject: false,
		},
		// C1 node serves scope "c" normally.
		{
			name: "c_scope_on_c1_serves",
			caps: swarmsearch.Capabilities{
				ShareLocal:  2,
				FileHits:    1,
				ContentHits: 1,
			},
			scope:      "c",
			wantReject: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
			p.SetCapabilities(tc.caps)
			p.SetSearcher(nopSearcherScope{})

			body, err := swarmsearch.EncodeQuery(swarmsearch.Query{
				TxID:  42,
				Q:     "photon",
				Scope: tc.scope,
				Limit: 5,
			})
			if err != nil {
				t.Fatal(err)
			}

			var got []byte
			reply := func(b []byte) error {
				got = b
				return nil
			}
			p.HandleMessage("198.51.100.7:6881", body, reply)

			if len(got) == 0 {
				t.Fatal("handler never invoked reply — expected either Reject or Result")
			}

			if tc.wantReject {
				rj, err := swarmsearch.DecodeReject(got)
				if err != nil {
					t.Fatalf("expected Reject, got undecodable payload: %v", err)
				}
				if rj.Code != swarmsearch.RejectUnsupportedScope {
					t.Errorf("Reject.Code = %d, want RejectUnsupportedScope (%d)",
						rj.Code, swarmsearch.RejectUnsupportedScope)
				}
				if rj.TxID != 42 {
					t.Errorf("Reject.TxID = %d, want 42", rj.TxID)
				}
			} else {
				// Must NOT be a reject — i.e. decoding as a Result
				// succeeds, or at minimum decoding as a Reject fails.
				if _, err := swarmsearch.DecodeResult(got); err != nil {
					t.Errorf("expected Result payload, decoding as Result failed: %v", err)
				}
			}
		})
	}
}

type nopSearcherScope struct{}

func (nopSearcherScope) SearchLocal(_ string, _ int) (int, []swarmsearch.LocalHit, error) {
	return 0, nil, nil
}
