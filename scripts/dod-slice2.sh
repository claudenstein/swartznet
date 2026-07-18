#!/bin/bash
# Slice 2 Definition-of-Done: add + download + seed, driven against TWO real
# dist/swartznet binaries on loopback. Cumulative with dod-slice0/1.
set -u
BIN=/home/kartofel/Claude/swartznet/dist/swartznet
WORK=$(mktemp -d)
export XDG_DATA_HOME="$WORK/xdg"
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1"; }
# Non-default ports so a stray production daemon can't interfere.
SEED_BT=42170
LEECH_BT=42171

# --- Build a fixture .torrent with python3 (no 'create' until Slice 3) ------
SEED_DATA="$WORK/seed-data"
mkdir -p "$SEED_DATA"
python3 - "$SEED_DATA" <<'EOF'
import hashlib, os, sys
root = sys.argv[1]
# Deterministic LCG payload, 96 KiB, 32 KiB pieces.
s = 0xdeadbeef
out = bytearray()
for _ in range(96 * 1024):
    s = (s * 1664525 + 1013904223) & 0xFFFFFFFF
    out.append((s >> 16) & 0xFF)
payload = bytes(out)
open(os.path.join(root, "fixture.bin"), "wb").write(payload)
piece_len = 32 * 1024
pieces = b"".join(hashlib.sha1(payload[i:i+piece_len]).digest() for i in range(0, len(payload), piece_len))
def benc(x):
    if isinstance(x, int): return b"i%de" % x
    if isinstance(x, bytes): return b"%d:%s" % (len(x), x)
    if isinstance(x, str): return benc(x.encode())
    if isinstance(x, dict):
        return b"d" + b"".join(benc(k) + benc(v) for k, v in sorted(x.items())) + b"e"
    raise TypeError(x)
info = {"length": len(payload), "name": "fixture.bin", "piece length": piece_len, "pieces": pieces}
open(os.path.join(root, "fixture.torrent"), "wb").write(benc({"info": info}))
ih = hashlib.sha1(benc(info)).hexdigest()
open(os.path.join(root, "infohash"), "w").write(ih)
EOF
IH=$(cat "$SEED_DATA/infohash")
echo "$IH" | grep -qE '^[0-9a-f]{40}$' && ok "fixture built (infohash $IH)" || fail "fixture build"

# --- Seeder run 1: add the .torrent (session records it) --------------------
"$BIN" add --no-dht --port $SEED_BT --api-addr localhost:0 \
  --data-dir "$WORK/seedA" --index-dir "$WORK/seedA-idx" "$SEED_DATA/fixture.torrent" \
  >"$WORK/s1.out" 2>"$WORK/s1.err" &
SPID=$!
for i in $(seq 1 100); do grep -q "Fetching metadata" "$WORK/s1.out" && break; sleep 0.05; done
kill -INT $SPID; wait $SPID; RC=$?
[ "$RC" = "130" ] && ok "seeder first run exits 130" || fail "seeder first run rc=$RC"
[ -f "$WORK/seedA/session.json" ] && ok "session.json written" || fail "session.json written"
grep -q "$IH" "$WORK/seedA/session.json" && ok "session records the torrent" || fail "session records the torrent"

# --- Seeder run 2: restore + VerifyData shows a REAL percentage -------------
# Place the payload where default storage expects it, so the restore rehash
# proves "non-zero % on a fresh seed before any peer connects".
cp "$SEED_DATA/fixture.bin" "$WORK/seedA/fixture.bin"
"$BIN" add --no-dht --port $SEED_BT --api-addr localhost:0 \
  --data-dir "$WORK/seedA" --index-dir "$WORK/seedA-idx" "$IH" \
  >"$WORK/s2.out" 2>"$WORK/s2.err" &
SPID=$!
SADDR=""
for i in $(seq 1 100); do
  SADDR=$(sed -n 's/^HTTP API listening on //p' "$WORK/s2.out" | head -1)
  [ -n "$SADDR" ] && break
  sleep 0.05
done
seeding=""
for i in $(seq 1 200); do
  seeding=$(curl -s "http://$SADDR/torrents" | python3 -c '
import json,sys
d=json.load(sys.stdin)
for t in d["torrents"]:
    if t["status"]=="seeding" and t["progress"]==1.0:
        print("yes"); break
' 2>/dev/null)
  [ "$seeding" = "yes" ] && break
  sleep 0.1
done
[ "$seeding" = "yes" ] && ok "restored seed verifies to 100% with zero peers" || fail "seed never reached seeding: $(curl -s http://$SADDR/torrents)"
"$BIN" status --api-addr "$SADDR" | grep -qE "seeding +100.0%" && ok "status shows real percentage" || fail "status shows real percentage"

# --- Leech: magnet with x.pe hint completes over loopback -------------------
MAGNET="magnet:?xt=urn:btih:$IH&dn=fixture.bin&x.pe=127.0.0.1:$SEED_BT"
timeout 60 "$BIN" add --no-dht --port $LEECH_BT --api-addr localhost:0 \
  --data-dir "$WORK/leech" --index-dir "$WORK/leech-idx" "$MAGNET" \
  >"$WORK/l.out" 2>"$WORK/l.err" &
LPID=$!
done_dl=""
for i in $(seq 1 300); do
  if [ -f "$WORK/leech/fixture.bin" ]; then
    if cmp -s "$WORK/leech/fixture.bin" "$SEED_DATA/fixture.bin"; then done_dl=yes; break; fi
  fi
  sleep 0.1
done
[ "$done_dl" = "yes" ] && ok "magnet transfer completed byte-identically over loopback" || fail "leech never completed"
grep -q "✓ file complete: fixture.bin" "$WORK/l.out" && ok "file-complete event printed" || fail "file-complete event printed"
kill -INT $LPID; wait $LPID

# --- PATCH /config/rate-limit merge semantics (the §6 fix) ------------------
curl -s -X PATCH -H 'Origin: http://localhost:7654' -d '{"upload_bps":555}' "http://$SADDR/config/rate-limit" >/dev/null
MERGED=$(curl -s -X PATCH -H 'Origin: http://localhost:7654' -d '{"download_bps":1000}' "http://$SADDR/config/rate-limit")
echo "$MERGED" | python3 -c '
import json,sys
d=json.load(sys.stdin)
assert d["download_bps"]==1000 and d["upload_bps"]==555, d
' && ok "PATCH rate-limit merges (upload untouched)" || fail "rate-limit merge: $MERGED"

# --- /config/queue + files listing ------------------------------------------
curl -s -X PATCH -H 'Origin: http://localhost:7654' -d '{"max_active_downloads":2}' "http://$SADDR/config/queue" \
  | grep -q '"max_active_downloads":2' && ok "queue cap set" || fail "queue cap set"
"$BIN" files --api-addr "$SADDR" "$IH" | grep -q "fixture.bin" && ok "files lists the fixture" || fail "files lists the fixture"
"$BIN" files --api-addr "$SADDR" "$IH" 0 none >/dev/null 2>&1 && ok "files sets priority" || fail "files sets priority"
"$BIN" files --api-addr "$SADDR" "$IH" | grep -qE "^0 +none" && ok "priority change visible" || fail "priority change visible"

# --- Downloads section in status text ---------------------------------------
"$BIN" status --api-addr "$SADDR" | grep -q "Downloads:" && ok "status renders Downloads section" || fail "status Downloads section"

kill -INT $SPID; wait $SPID; RC=$?
[ "$RC" = "130" ] && ok "seeder clean exit 130" || fail "seeder exit rc=$RC"

echo
echo "PASS=$PASS FAIL=$FAIL"
rm -rf "$WORK"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
