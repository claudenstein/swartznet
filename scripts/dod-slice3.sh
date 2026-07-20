#!/bin/bash
# Slice 3 Definition-of-Done: create + infohash-preserving signing, driven
# against the real dist/swartznet binary. Cumulative with dod-slice0/1/2.
set -u
BIN=/home/kartofel/Claude/swartznet/dist/swartznet
WORK=$(mktemp -d)
export XDG_DATA_HOME="$WORK/xdg"
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1"; }
PIDS=""
cleanup() {
  for p in $PIDS; do kill -0 "$p" 2>/dev/null && kill "$p" 2>/dev/null; done
  rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

# --- Fixture content --------------------------------------------------------
mkdir -p "$WORK/content"
python3 -c 'open("'"$WORK"'/content/book.txt","w").write("swartznet dod fixture\n"*512)'

# --- create (plain) ---------------------------------------------------------
"$BIN" create -o "$WORK/plain.torrent" "$WORK/content/book.txt" >"$WORK/c1.out" 2>"$WORK/c1.err"
[ $? -eq 0 ] && ok "create exits 0" || fail "create exit: $(cat "$WORK/c1.err")"
grep -q "✓ Created $WORK/plain.torrent" "$WORK/c1.out" && ok "created line" || fail "created line"
IH_PLAIN=$(sed -n 's/^  InfoHash: //p' "$WORK/c1.out")
echo "$IH_PLAIN" | grep -qE '^[0-9a-f]{40}$' && ok "InfoHash printed ($IH_PLAIN)" || fail "InfoHash printed: $IH_PLAIN"

# --- create --sign ----------------------------------------------------------
"$BIN" create -o "$WORK/signed.torrent" --sign "$WORK/content/book.txt" >"$WORK/c2.out" 2>"$WORK/c2.err"
[ $? -eq 0 ] && ok "create --sign exits 0" || fail "create --sign: $(cat "$WORK/c2.err")"
PUBKEY=$(sed -n 's/^Signing with identity //p' "$WORK/c2.out")
echo "$PUBKEY" | grep -qE '^[0-9a-f]{64}$' && ok "signing identity printed" || fail "signing identity: $PUBKEY"
IH_SIGNED=$(sed -n 's/^  InfoHash: //p' "$WORK/c2.out")
[ "$IH_PLAIN" = "$IH_SIGNED" ] && ok "signed/unsigned twins share ONE infohash" || fail "twin infohash: $IH_PLAIN vs $IH_SIGNED"

# --- byte-level twin properties (python bencode) ----------------------------
python3 - "$WORK/plain.torrent" "$WORK/signed.torrent" <<'EOF' && ok "info dict byte-identical; snet fields present" || fail "byte-level twin check"
import hashlib, sys
def bdecode_dict_raw(b):
    # Minimal top-level dict splitter capturing raw value bytes.
    assert b[:1] == b"d" and b[-1:] == b"e"
    i, out = 1, {}
    def parse(i):
        if b[i:i+1] == b"i":
            j = b.index(b"e", i); return b[i:j+1], j+1
        if b[i:i+1] in (b"d", b"l"):
            depth, j = 0, i
            while True:
                c = b[j:j+1]
                if c in (b"d", b"l"): depth += 1; j += 1
                elif c == b"i": j = b.index(b"e", j) + 1
                elif c == b"e": depth -= 1; j += 1;
                else:
                    k = b.index(b":", j); n = int(b[j:k]); j = k + 1 + n
                if depth == 0: return b[i:j], j
        k = b.index(b":", i); n = int(b[i:k]); j = k + 1 + n
        return b[i:j], j
    while i < len(b) - 1:
        key, i = parse(i)
        klen = int(key.split(b":")[0]); kname = key[len(str(klen))+1:]
        val, i = parse(i)
        out[kname.decode()] = val
    return out
plain = bdecode_dict_raw(open(sys.argv[1], "rb").read())
signed = bdecode_dict_raw(open(sys.argv[2], "rb").read())
assert plain["info"] == signed["info"], "info bytes differ"
assert "snet.pubkey" in signed and "snet.sig" in signed, "snet fields missing"
assert "snet.pubkey" not in plain, "plain has snet fields"
assert signed["snet.pubkey"].startswith(b"32:"), "pubkey not a 32-byte string"
assert signed["snet.sig"].startswith(b"64:"), "sig not a 64-byte string"
assert hashlib.sha1(plain["info"]).hexdigest() == hashlib.sha1(signed["info"]).hexdigest()
EOF

# --- add the signed torrent → SignedBy set ----------------------------------
timeout 60 "$BIN" add --no-dht --port 0 --api-addr localhost:0 --data-dir "$WORK/nodeA" \
  --index-dir "$WORK/nodeA-idx" "$WORK/signed.torrent" >"$WORK/a1.out" 2>"$WORK/a1.err" &
APID=$!; PIDS="$PIDS $APID"
ADDR=""
for i in $(seq 1 100); do
  ADDR=$(sed -n 's/^HTTP API listening on //p' "$WORK/a1.out" | head -1)
  [ -n "$ADDR" ] && break; sleep 0.05
done
SIGNEDBY=$(curl -s "http://$ADDR/torrents" | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d["torrents"][0].get("signed_by","") if d["torrents"] else "")')
[ "$SIGNEDBY" = "$PUBKEY" ] && ok "added signed torrent shows signed_by" || fail "signed_by: $SIGNEDBY want $PUBKEY"
grep -q "engine.torrent_signature_verified" "$WORK/a1.err" && ok "verified log line" || fail "verified log line"
kill -INT $APID; wait $APID

# --- restart nodeA → signed_by survives the session round-trip --------------
timeout 60 "$BIN" add --no-dht --port 0 --api-addr localhost:0 --data-dir "$WORK/nodeA" \
  --index-dir "$WORK/nodeA-idx" "$IH_PLAIN" >"$WORK/a1b.out" 2>"$WORK/a1b.err" &
APID=$!; PIDS="$PIDS $APID"
ADDR=""
for i in $(seq 1 100); do
  ADDR=$(sed -n 's/^HTTP API listening on //p' "$WORK/a1b.out" | head -1)
  [ -n "$ADDR" ] && break; sleep 0.05
done
SB2=$(curl -s "http://$ADDR/torrents" | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d["torrents"][0].get("signed_by","") if d["torrents"] else "")')
[ "$SB2" = "$PUBKEY" ] && ok "signed_by survives restart" || fail "restart signed_by: '$SB2'"
kill -INT $APID; wait $APID

# --- byte-flipped signature → add-anyway with empty SignedBy (D20) ----------
python3 - "$WORK/signed.torrent" "$WORK/flipped.torrent" <<'EOF'
import sys
raw = bytearray(open(sys.argv[1], "rb").read())
raw[-5] ^= 0x01  # inside the snet.sig value (before trailing 'e')
open(sys.argv[2], "wb").write(bytes(raw))
EOF
timeout 60 "$BIN" add --no-dht --port 0 --api-addr localhost:0 --data-dir "$WORK/nodeB" \
  --index-dir "$WORK/nodeB-idx" "$WORK/flipped.torrent" >"$WORK/a2.out" 2>"$WORK/a2.err" &
BPID=$!; PIDS="$PIDS $BPID"
ADDR=""
for i in $(seq 1 100); do
  ADDR=$(sed -n 's/^HTTP API listening on //p' "$WORK/a2.out" | head -1)
  [ -n "$ADDR" ] && break; sleep 0.05
done
ROW=$(curl -s "http://$ADDR/torrents" | python3 -c '
import json,sys
d=json.load(sys.stdin)
t=d["torrents"][0] if d["torrents"] else {}
print(t.get("infohash",""), t.get("signed_by","-EMPTY-") or "-EMPTY-")')
echo "$ROW" | grep -q "^$IH_PLAIN -EMPTY-$" && ok "bad-signature torrent added anyway with empty signed_by" || fail "bad-sig row: $ROW"
grep -q "engine.torrent_signature_rejected" "$WORK/a2.err" && ok "rejected warn logged" || fail "rejected warn logged"
kill -INT $BPID; wait $BPID

# --- create --sign --seed: creator badges itself AND actually serves --------
# --no-dht keeps the run hermetic (no public bootstrap, no UPnP); the leech
# finds the seeder via an x.pe hint on a fixed test port.
SEED_BT=42172
timeout 90 "$BIN" create -o "$WORK/seeded.torrent" --sign --seed --no-dht \
  --data-dir "$WORK/seeder" "$WORK/content/book.txt" >"$WORK/c3.out" 2>"$WORK/c3.err" &
CPID=$!; PIDS="$PIDS $CPID"
for i in $(seq 1 100); do grep -q "Seeding... (Ctrl-C to stop)" "$WORK/c3.out" && break; sleep 0.1; done
grep -q "Seeding... (Ctrl-C to stop)" "$WORK/c3.out" && ok "seed mode reaches Seeding line" || fail "no Seeding line: $(cat "$WORK/c3.out" "$WORK/c3.err")"
SEEDSB=$(python3 -c '
import json
d=json.load(open("'"$WORK"'/seeder/session.json"))
print(d["torrents"][0].get("signed_by","") if d["torrents"] else "")')
[ "$SEEDSB" = "$PUBKEY" ] && ok "creator's own node persists signed_by (J1 fix)" || fail "seeder signed_by: '$SEEDSB'"

# The create engine binds an ephemeral BT port; discover it from the session
# side is not possible — so re-seed on a fixed port via 'add' of the SIGNED
# torrent with the content pre-placed, then download from it. This proves the
# signed .torrent is fetchable end to end.
kill -INT $CPID; wait $CPID; RC=$?
[ "$RC" = "130" ] && ok "create --seed clean exit 130" || fail "create --seed rc=$RC"

mkdir -p "$WORK/relay"
cp "$WORK/content/book.txt" "$WORK/relay/book.txt"
timeout 90 "$BIN" add --no-dht --port $SEED_BT --api-addr localhost:0 \
  --data-dir "$WORK/relay" --index-dir "$WORK/relay-idx" "$WORK/seeded.torrent" \
  >"$WORK/r.out" 2>"$WORK/r.err" &
RPID=$!; PIDS="$PIDS $RPID"
for i in $(seq 1 100); do grep -q "Fetching metadata" "$WORK/r.out" && break; sleep 0.05; done
kill -INT $RPID; wait $RPID
# Restart: restore + VerifyData brings the relay to 100% seeding on the fixed port.
timeout 90 "$BIN" add --no-dht --port $SEED_BT --api-addr localhost:0 \
  --data-dir "$WORK/relay" --index-dir "$WORK/relay-idx" "$IH_PLAIN" \
  >"$WORK/r2.out" 2>"$WORK/r2.err" &
RPID=$!; PIDS="$PIDS $RPID"
MAGNET="magnet:?xt=urn:btih:$IH_PLAIN&dn=book.txt&x.pe=127.0.0.1:$SEED_BT"
timeout 60 "$BIN" add --no-dht --port 0 --api-addr localhost:0 \
  --data-dir "$WORK/leech" --index-dir "$WORK/leech-idx" "$MAGNET" \
  >"$WORK/l.out" 2>"$WORK/l.err" &
LPID=$!; PIDS="$PIDS $LPID"
GOT=""
for i in $(seq 1 300); do
  if [ -f "$WORK/leech/book.txt" ] && cmp -s "$WORK/leech/book.txt" "$WORK/content/book.txt"; then GOT=yes; break; fi
  sleep 0.1
done
[ "$GOT" = "yes" ] && ok "signed torrent downloads byte-identically from the seeding node" || fail "leech never completed"
kill -INT $LPID; wait $LPID
kill -INT $RPID; wait $RPID

# --- usage-error taxonomy ---------------------------------------------------
"$BIN" create "$WORK/content/book.txt" >/dev/null 2>"$WORK/u1.err"; RC=$?
[ "$RC" = "2" ] && grep -q "\-o <output.torrent> is required" "$WORK/u1.err" \
  && ok "-o required (exit 2)" || fail "-o required check"
"$BIN" create -o "$WORK/x.torrent" --identity /k "$WORK/content/book.txt" >/dev/null 2>"$WORK/u2.err"; RC=$?
[ "$RC" = "2" ] && grep -q "\-\-identity is only used with \-\-sign" "$WORK/u2.err" \
  && ok "--identity without --sign fails closed (exit 2)" || fail "--identity gate"
"$BIN" create -o "$WORK/x.torrent" --sign --identity "$WORK/nope.key" "$WORK/content/book.txt" >/dev/null 2>"$WORK/u3.err"; RC=$?
[ "$RC" = "1" ] && [ ! -e "$WORK/nope.key" ] && ok "explicit missing identity: exit 1, no key minted" || fail "explicit identity fail-closed (rc=$RC)"

echo
echo "PASS=$PASS FAIL=$FAIL"
# cleanup via trap
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
