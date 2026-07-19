#!/bin/bash
# Slice 12 Definition-of-Done (frozen-contract + offline-CLI portion). The
# Aggregate index's load-bearing cross-implementation anchors — the signed
# SNAGG B-tree byte format and the PPMI pointer value — are pinned by golden
# vectors (run -race); the offline `aggregate build|inspect|find` tooling is
# driven against the real dist/swartznet binary. The live aggregatePPMI/
# composite backends + admission seeds + crawler are the remaining opt-in,
# off-by-default work (ship default stays LayerDMode=legacy). Cumulative with
# dod-slice0..11.
set -u
ROOT=/home/kartofel/Claude/swartznet
BIN="$ROOT/dist/swartznet"
GO=/usr/local/go/bin/go
WORK=$(mktemp -d)
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1"; }
trap 'rm -rf "$WORK"' EXIT INT TERM
cd "$ROOT"
run_go() { if $GO test -race "$2" -run "$3" -count=1 >"$WORK/$1.log" 2>&1; then ok "$4"; else fail "$4 — $(tail -4 "$WORK/$1.log")"; fi; }

# ======================================================================
# Part 1 — frozen byte contracts (golden vectors, -race)
# ======================================================================
run_go snagg ./contracts/snagg/ \
  'TestEncodeRecordKeyOrder|TestBuildOpenFindRoundTrip|TestSingleLeafTreeHasRoot|TestOpenRejectsBadTrailerSig|TestFindDropsRecordWithBadSig|TestGoldenFingerprint|TestGoldenTrailerLayout|TestMagicAndConstantsFrozen' \
  "SNAGG frozen format: magic/trailer/fingerprint golden, key order, build→open→find→verify, bad-sig rejection"
run_go ppmi ./contracts/dhtschema/ \
  'TestPPMISaltGolden|TestPPMIEncodeGolden|TestPPMIRoundTripWithCommit|TestPPMIFieldWidthValidation' \
  "PPMI frozen: SHA256(\"snet.index\") salt golden, minimal-item wire golden, field-width validation"
run_go aggcli ./cmd/swartznet/ \
  'TestAggregateBuildInspectFindRoundTrip|TestAggregateBuildRefusesHighPoW|TestAggregateBuildRequiresOut|TestAggregateBuildRejectsBadRecords|TestAggregateInspectFailsOnGarbage|TestAggregateFindVerifyFailsOnTamperedFingerprint' \
  "aggregate CLI: build→inspect→find round-trip, pow>40 refused, bad-record reject, inspect integrity gate, --verify fingerprint"
run_go seam ./internal/dhtindex/ \
  'TestAggregatePPMISelfLookup|TestAggregatePPMIReadOnlyInert|TestAggregatePPMIRetract|TestCompositeDualWriteLegacyReadable|TestCompositeSecondaryErrorTolerated' \
  "RecordBackend seam: aggregatePPMI self-lookup over its SNAGG tree; composite dual-write → legacy-only read still returns hits; secondary errors tolerated"
run_go ppmidht ./internal/dhtindex/ \
  'TestPPMIClusterRoundTrip|TestPutFailsClosedOnZeroNodes' \
  "PPMI BEP-44 primitive: real 2-node DHT round-trip at SHA1(pubkey||SHA256(\"snet.index\")); PutPPMI fails closed on zero nodes"
run_go aggtorrent ./internal/dhtindex/ \
  'TestWrapSnaggTorrentPieceLengthAligns|TestWrapSnaggTorrentRejectsMisaligned|TestOpenVerifiedTreeCommitBinding' \
  "aggregate distribution core: SNAGG tree wraps to a piece-aligned torrent; OpenVerifiedTree binds the fetched tree to the PPMI commit + rejects a corrupt trailer"
run_go aggdist ./internal/dhtindex/ \
  'TestAggregateDistributeResolveRoundTrip|TestAggregateResolveToleratesFailure|TestAggregateResolveRejectsMismatchedCommit|TestAggregateRetractToEmptyDropsSeed|TestAggregateNoPublisherIsLocalOnly' \
  "aggregate distribution orchestration: node A distributes-on-refresh, node B resolves-on-lookup through the real wrap/open/commit seam; a failing/unknown/unresolved publisher degrades to no-hits (never a query error); retract-to-empty drops the stale seed; nil-publisher stays local-only"
run_go aggadapter ./internal/engine/ \
  'TestAggTreePublisherWritesSeedsPuts|TestAggTreeResolverFetchesAndVerifies|TestAggTreeResolverSkipsSelf' \
  "engine live adapters: TreePublisher writes+seeds+puts the PPMI pointer (infohash==seed, commit==fingerprint), drops the stale seed on republish + RetractTree; TreeResolver fetches+commit-verifies, rejects a mismatched commit / malformed pointer, and SKIPS a self-lookup (never tears down the node's own seed)"
run_go seammode ./internal/engine/ 'TestLayerDCompositeModeBuilds|TestLayerDAggregateModeBuilds' \
  "engine selects composite / aggregatePPMI LayerDMode with zero app change (ship default stays legacy)"
run_go cfgmode ./internal/config/ 'TestValidateLayerDMode' \
  "config.Validate accepts legacy|composite|aggregatePPMI, rejects unknown modes"
run_go admission ./internal/admission/ \
  'TestUnknownCandidateDenied|TestThreeFreshEndorsersDoNotClearBar|TestSeededCandidateAdmitted|TestAnchorAdmittedSeededAndCapExempt' \
  "deny-by-default admission (§6 fix: 3 fresh Sybils don't clear the bar; seeds/anchors admitted + cap-exempt)"
run_go seeds ./internal/reputation/ \
  'TestTrackerLoadSeedList|TestTrackerSeededBypassesThreshold|TestLoadSeedListNormalizesUppercaseHex|TestLoadSeedListUnsupportedVersion' \
  "seeds.json → MarkSeeded (lowercase-normalized, version-gated) applies the seed bonus"

# ======================================================================
# Part 2 — the offline aggregate tooling against the real binary
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg"
mkdir -p "$XDG_DATA_HOME"
python3 - > "$WORK/recs.jsonl" <<'PY'
import json
for i in range(12):
    kw = ["ubuntu","debian","fedora","arch"][i%4]
    print(json.dumps({"kw":kw,"ih":f"{i:040x}","t":1700000000+i}))
PY

"$BIN" aggregate build --in "$WORK/recs.jsonl" --out "$WORK/idx.snagg" --seq 7 >"$WORK/build.txt" 2>&1
grep -q 'records:     12' "$WORK/build.txt" && grep -q 'fingerprint:' "$WORK/build.txt" \
  && ok "aggregate build signs+packs 12 records into a SNAGG file" || fail "build: $(cat "$WORK/build.txt")"
[ "$(stat -c '%a' "$WORK/idx.snagg")" = "644" ] && ok "output file mode is 0644" || fail "wrong file mode"

FP_BUILD=$(grep 'fingerprint:' "$WORK/build.txt" | awk '{print $2}')
"$BIN" aggregate inspect "$WORK/idx.snagg" >"$WORK/inspect.txt" 2>&1
grep -q 'records:        12' "$WORK/inspect.txt" && grep -q 'sequence:       7' "$WORK/inspect.txt" \
  && ok "aggregate inspect prints trailer metadata" || fail "inspect: $(cat "$WORK/inspect.txt")"
FP_INSPECT=$(grep 'fingerprint:' "$WORK/inspect.txt" | awk '{print $2}')
[ -n "$FP_BUILD" ] && [ "$FP_BUILD" = "$FP_INSPECT" ] && ok "build + inspect fingerprints agree" || fail "fingerprint mismatch"

"$BIN" aggregate find --verify "$WORK/idx.snagg" ubu >"$WORK/find.txt" 2>&1
grep -q '3 records' "$WORK/find.txt" && ok "aggregate find --verify returns the 3 ubuntu records" || fail "find: $(cat "$WORK/find.txt")"

# inspect fails fast on a garbage file (the integrity gate).
head -c 49152 /dev/zero > "$WORK/garbage.snagg"
"$BIN" aggregate inspect "$WORK/garbage.snagg" >/dev/null 2>&1; [ $? -eq 1 ] \
  && ok "aggregate inspect fails (exit 1) on a bad-magic file" || fail "inspect did not reject garbage"

# --pow-bits > 40 refused with exit 2.
"$BIN" aggregate build --in "$WORK/recs.jsonl" --out "$WORK/x.snagg" --pow-bits 41 >/dev/null 2>&1; [ $? -eq 2 ] \
  && ok "aggregate build refuses --pow-bits > 40 (exit 2)" || fail "pow cap not enforced"

# --help discoverability.
"$BIN" --help 2>&1 | grep -q 'aggregate <build|inspect|find>' \
  && ok "--help lists the aggregate command" || fail "--help missing aggregate"

echo
echo "PASS=$PASS FAIL=$FAIL"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
