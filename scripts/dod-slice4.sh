#!/bin/bash
# Slice 4 Definition-of-Done: Layer L (local full-text search) against the
# real dist/swartznet binary. Cumulative with dod-slice0/1/2/3.
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

# --- Build a content tree: a text file, a minimal PDF, and a minimal ZIM ----
CONTENT="$WORK/library"
mkdir -p "$CONTENT"
python3 - "$CONTENT" <<'EOF'
import os, sys, struct, zlib
root = sys.argv[1]
# Plain text, single chunk (< 2.5 KiB), with a unique marker.
open(os.path.join(root, "book.txt"), "w").write(
    "the archive preserves human knowledge. plaintextmarker unique token appears here.\n")

# Minimal one-page PDF containing a marker — mirrors the extractor package's
# known-good buildMinimalPDF (binary-comment header + newline-delimited text
# stream) so ledongthuc/pdf extracts the text.
def pdf(marker):
    objs = []
    objs.append(b"<< /Type /Catalog /Pages 2 0 R >>")
    objs.append(b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
    objs.append(b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
    stream = b"BT\n/F1 24 Tf\n72 720 Td\n(" + marker.encode() + b") Tj\nET\n"
    objs.append(b"<< /Length " + str(len(stream)).encode() + b" >>\nstream\n" + stream + b"endstream")
    objs.append(b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
    out = b"%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"
    offs = []
    for i, o in enumerate(objs, 1):
        offs.append(len(out))
        out += str(i).encode() + b" 0 obj\n" + o + b"\nendobj\n"
    xref = len(out)
    out += b"xref\n0 " + str(len(objs)+1).encode() + b"\n0000000000 65535 f \n"
    for off in offs:
        out += ("%010d 00000 n \n" % off).encode()
    out += b"trailer\n<< /Size " + str(len(objs)+1).encode() + b" /Root 1 0 R >>\nstartxref\n" + str(xref).encode() + b"\n%%EOF\n"
    return out
open(os.path.join(root, "paper.pdf"), "wb").write(pdf("pdfmarker unique token"))

# Minimal valid ZIM (major v5, single uncompressed text/plain article).
def zim(body):
    body = body.encode()
    magic = 0x44D495A
    mimelist = b"text/plain\x00\x00"
    ot = struct.pack("<II", 8, 8+len(body))
    cluster = b"\x01" + ot + body
    mimepos = 80
    urlpos = mimepos + len(mimelist)
    clusterptrpos = urlpos + 8
    entry = struct.pack("<H", 0) + b"\x00" + b"A" + struct.pack("<III", 0, 0, 0) + b"A/article\x00\x00"
    direntriespos = clusterptrpos + 8
    clusterpos = direntriespos + len(entry)
    checksumpos = clusterpos + len(cluster)
    h = struct.pack("<I", magic) + struct.pack("<HH", 5, 0) + b"\x00"*16
    h += struct.pack("<II", 1, 1)
    h += struct.pack("<QQQQ", urlpos, urlpos, clusterptrpos, mimepos)
    h += struct.pack("<II", 0, 0) + struct.pack("<Q", checksumpos)
    out = h + mimelist + struct.pack("<Q", direntriespos) + struct.pack("<Q", clusterpos) + entry + cluster + b"\x00"*16
    return out
open(os.path.join(root, "wiki.zim"), "wb").write(zim("zimmarker unique encyclopedia token in the article body"))
EOF
ok "content tree built (txt + pdf + zim)"

# --- create the torrent, then add-and-seed it with indexing on -------------
# The torrent's name is the content dir's basename ("library"), so default
# storage resolves files under <data-dir>/library/ — place them there BEFORE
# add so VerifyData completes and file-complete events drive indexing.
API=17654
ADDR="localhost:$API"
"$BIN" create -o "$WORK/lib.torrent" "$CONTENT" >"$WORK/c.out" 2>"$WORK/c.err"
[ $? -eq 0 ] && ok "torrent created" || fail "create: $(cat "$WORK/c.err")"
mkdir -p "$WORK/node"
cp -r "$CONTENT" "$WORK/node/library"

timeout 120 "$BIN" add --no-dht --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/node" --index-dir "$WORK/node-idx" "$WORK/lib.torrent" \
  >"$WORK/a.out" 2>"$WORK/a.err" &
APID=$!; PIDS="$PIDS $APID"
for i in $(seq 1 100); do
  curl -s "http://$ADDR/status" >/dev/null 2>&1 && break; sleep 0.1
done

# First wait for all three files to finish extracting (content_count stable),
# so the per-marker queries below are not racing the pipeline.
for i in $(seq 1 200); do
  CC=$(curl -s "http://$ADDR/index/stats" | python3 -c 'import json,sys; print(json.load(sys.stdin)["content_count"])' 2>/dev/null || echo 0)
  [ "${CC:-0}" -ge 3 ] && break
  sleep 0.15
done

# wait_hit <query> <extractor>: returns the first hit for that extractor.
wait_hit() {
  for i in $(seq 1 100); do
    got=$(curl -s -X POST -H 'Origin: http://localhost' -d "{\"q\":\"$1\",\"highlight\":true}" "http://$ADDR/search" \
      | python3 -c "
import json,sys
d=json.load(sys.stdin)
for h in d['local']['hits']:
    if h.get('extractor')=='$2':
        print(json.dumps(h)); break
" 2>/dev/null)
    [ -n "$got" ] && { echo "$got"; return 0; }
    sleep 0.15
  done
  return 1
}

HIT=$(wait_hit plaintextmarker plaintext) && ok "plaintext file indexed + searchable" || fail "plaintext not indexed"
echo "$HIT" | grep -q '<mark>plaintextmarker</mark>' && ok "search hit carries <mark> highlight" || fail "no <mark> in fragment: $HIT"

wait_hit pdfmarker pdf >/dev/null && ok "PDF indexed via live pipeline (extractor=pdf)" || fail "PDF not indexed"

ZHIT=$(wait_hit zimmarker zim) && ok "ZIM indexed via LIVE pipeline (the §6 fix)" || fail "ZIM not indexed (ReadSeekerAt shim regression?)"
echo "$ZHIT" | grep -q '"mime": *"application/x-zim"\|"mime":"application/x-zim"' && ok "ZIM hit mime=application/x-zim" || fail "zim mime wrong: $ZHIT"

# --- index stats ------------------------------------------------------------
"$BIN" index --api-addr "$ADDR" | grep -q "documents:" && ok "swartznet index shows stats" || fail "index stats"
CCOUNT=$(curl -s "http://$ADDR/index/stats" | python3 -c 'import json,sys; print(json.load(sys.stdin)["content_count"])')
[ "$CCOUNT" -ge 3 ] && ok "content_count >= 3 ($CCOUNT)" || fail "content_count = $CCOUNT"

# --- --signed-by exact TermQuery (metacharacters -> empty, never a parse err)
INJECT=$(curl -s -X POST -H 'Origin: http://localhost' \
  -d '{"q":"knowledge","signed_by":"+foo:bar* AND"}' "http://$ADDR/search" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["local"]["total"])')
[ "$INJECT" = "0" ] && ok "--signed-by metacharacters yield empty (exact TermQuery, no parse error)" || fail "signed_by injection total=$INJECT"

# --- per-torrent indexing toggle -------------------------------------------
IH=$(curl -s "http://$ADDR/torrents" | python3 -c 'import json,sys; print(json.load(sys.stdin)["torrents"][0]["infohash"])')
"$BIN" index --api-addr "$ADDR" "$IH" off | grep -q "indexing off: $IH" && ok "index toggle off" || fail "index toggle off"
"$BIN" index --api-addr "$ADDR" "$IH" on | grep -q "indexing on: $IH" && ok "index toggle on" || fail "index toggle on"

# --- schema rebuild on sentinel bump ---------------------------------------
# (Handled at the unit level; here just assert the index dir exists + reopens.)
[ -f "$WORK/node-idx/index_meta.json" ] && ok "bleve index dir present" || fail "index dir missing"

# --- Forget: docs gone, file kept ------------------------------------------
BEFORE=$(sha256sum "$WORK/node/library/book.txt" | cut -d' ' -f1)
curl -s -X DELETE -H 'Origin: http://localhost' "http://$ADDR/torrents/$IH?forget=1" | grep -q '"forgot":true' && ok "Forget request accepted" || fail "forget request"
sleep 0.5
GONE=$(curl -s -X POST -H 'Origin: http://localhost' -d '{"q":"plaintextmarker"}' "http://$ADDR/search" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["local"]["total"])')
[ "$GONE" = "0" ] && ok "Forget removed the torrent's docs from search" || fail "forget left $GONE hits"
AFTER=$(sha256sum "$WORK/node/library/book.txt" | cut -d' ' -f1)
[ "$BEFORE" = "$AFTER" ] && ok "Forget kept the downloaded file on disk" || fail "forget deleted the file"

kill -INT $APID; wait $APID

# --- --no-index daemon: search 200-empty, /index/stats 503 -----------------
timeout 30 "$BIN" add --no-dht --no-index --port 0 --api-addr localhost:0 \
  --data-dir "$WORK/ni" --index-dir "$WORK/ni-idx" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  >"$WORK/ni.out" 2>/dev/null &
NPID=$!; PIDS="$PIDS $NPID"
NADDR=""
for i in $(seq 1 100); do
  NADDR=$(sed -n 's/^HTTP API listening on //p' "$WORK/ni.out" | head -1)
  [ -n "$NADDR" ] && break; sleep 0.05
done
SC=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Origin: http://localhost' -d '{"q":"x"}' "http://$NADDR/search")
[ "$SC" = "200" ] && ok "--no-index: /search is 200-empty (Layer L off, not 503)" || fail "no-index search = $SC"
STC=$(curl -s -o /dev/null -w '%{http_code}' "http://$NADDR/index/stats")
[ "$STC" = "503" ] && ok "--no-index: /index/stats is 503" || fail "no-index stats = $STC"
kill -INT $NPID; wait $NPID

echo
echo "PASS=$PASS FAIL=$FAIL"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
