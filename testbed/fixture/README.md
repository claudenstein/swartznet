# Testbed fixture

Real content for Layer-B scenarios, so netem profiles measure
actual piece transfer instead of empty torrents.

## Files

- `content/book/chapter-1.txt`, `content/book/chapter-2.txt` —
  small deterministic human-readable text with a distinctive
  marker (`aethergram`) used by search assertions.
- `fixture.torrent` — pre-generated torrent for `content/book/`
  with `--piece-kib 16`.  Infohash is deterministic for the
  checked-in content and piece size.
- `INFOHASH` — one-line hex infohash of `fixture.torrent`,
  consumed by scenario scripts.

## Regenerating

If you modify `content/book/*`, regenerate the torrent:

```bash
rm testbed/fixture/fixture.torrent
go run ./cmd/swartznet create \
    -o testbed/fixture/fixture.torrent \
    --name testbed-fixture-book \
    --piece-kib 16 \
    --comment "SwartzNet testbed fixture" \
    testbed/fixture/content/book
# Update testbed/fixture/INFOHASH with the new infohash.
```

Commit all three files (content, `.torrent`, `INFOHASH`) together.
