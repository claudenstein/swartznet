#!/bin/bash
# Slice 13 Definition-of-Done: hardening. It mirrors the CI merge gate locally —
# gofmt / go vet / go mod tidy cleanliness and the deterministic wire-compat
# assertions (all contracts/* golden vectors, future-service-bit tolerance,
# reject-code-2 on scope mismatch) all green under -race — and verifies the
# docs/licenses are reconciled with the code. The timing-sensitive
# internal/wirecompat/scenarios stay local-only (excluded from the CI -race run,
# exactly as .github/workflows/test.yml does). Cumulative with dod-slice0..12.
set -u
ROOT=/home/kartofel/Claude/swartznet
GO=/usr/local/go/bin/go
WORK=$(mktemp -d)
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1"; }
trap 'rm -rf "$WORK"' EXIT INT TERM
cd "$ROOT"
run_go() { if $GO test -race "$2" -run "$3" -count=1 >"$WORK/$1.log" 2>&1; then ok "$4"; else fail "$4 — $(tail -4 "$WORK/$1.log")"; fi; }

# ======================================================================
# Part 1 — the CI merge gate is green (gofmt / vet / tidy)
# ======================================================================
GFMT=$($GO run cmd/gofmt 2>/dev/null; /usr/local/go/bin/gofmt -l -s cmd/ internal/ contracts/ 2>/dev/null)
[ -z "$GFMT" ] && ok "gofmt -s clean across cmd/ internal/ contracts/" || fail "gofmt would reformat: $GFMT"
$GO vet ./... >"$WORK/vet.log" 2>&1 && ok "go vet ./... clean (incl. GUI package)" || fail "go vet — $(tail -3 "$WORK/vet.log")"
cp go.mod "$WORK/go.mod.bak"; cp go.sum "$WORK/go.sum.bak"
$GO mod tidy >/dev/null 2>&1
if diff -q go.mod "$WORK/go.mod.bak" >/dev/null && diff -q go.sum "$WORK/go.sum.bak" >/dev/null; then
  ok "go.mod / go.sum tidy (no drift)"
else
  fail "go mod tidy changed go.mod/go.sum"; cp "$WORK/go.mod.bak" go.mod; cp "$WORK/go.sum.bak" go.sum
fi

# ======================================================================
# Part 2 — the deterministic wire-compat assertions (the CI -race gate)
# ======================================================================
run_go golden ./contracts/... \
  'Golden|Frozen|KeyOrder|Vector|SaltGolden' \
  "every contracts/* codec matches its frozen golden byte-vectors (bencode/dhtschema/ltepwire/record/riblt/sign/snagg/token)"
run_go tolerance ./contracts/ltepwire/ \
  'TestAnnounced|Services|Unknown|Future' \
  "capability mask: unknown/future service bits tolerated (never rejected)"
run_go rejectcode ./internal/swarmsearch/ \
  'Scope|Reject' \
  "sn_search reject code 2 (unsupported_scope) on a scope the peer does not share"
run_go silence ./internal/swarmsearch/ \
  'TestVanillaPeerNeverReceivesFrames|TestBannedPeerNeverMintsToken' \
  "vanilla-silence (deterministic, CI): a non-advertising peer receives ZERO sn_search frames (no token minted)"

# ======================================================================
# Part 3 — docs + licenses reconciled with the code (§6 doc-drift fixes)
# ======================================================================
# THIRD_PARTY_LICENSES lists ledongthuc/pdf as BSD-3-Clause (not MIT).
awk '/ledongthuc\/pdf/{f=1} f&&/BSD-3-Clause/{print "found"; exit}' THIRD_PARTY_LICENSES | grep -q found \
  && ok "THIRD_PARTY_LICENSES: ledongthuc/pdf is BSD-3-Clause" || fail "pdf license not BSD-3-Clause"
# docs/07 no longer mandates DHT sharding (the code does oldest-hit eviction).
grep -q 'oldest-hit eviction, not' docs/07-bep-dht-keyword-index-draft.md \
  && ok "docs/07 reconciled: oversize handling is eviction, sharding reserved" || fail "docs/07 still mandates sharding"
! grep -qE 'MUST shard by appending' docs/07-bep-dht-keyword-index-draft.md \
  && ok "docs/07 no longer says publishers MUST shard" || fail "docs/07 still says MUST shard"
# docs/06 reject-code-2 matches the code constant.
grep -q 'RejectUnsupportedScope = 2' contracts/ltepwire/wire.go \
  && grep -q 'code 2' docs/06-bep-sn_search-draft.md \
  && ok "docs/06 reject-code-2 (unsupported_scope) matches the code" || fail "reject-code-2 drift"

echo
echo "PASS=$PASS FAIL=$FAIL"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
