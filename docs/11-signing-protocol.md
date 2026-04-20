# Draft BEP: Publisher-Signed `.torrent` Files (`snet.pubkey` / `snet.sig`)

| Field | Value |
|---|---|
| Title | Publisher-Signed Metainfo Files via Optional Top-Level Fields |
| Version | 1 |
| Last-Modified | 2026-04-13 |
| Author | The SwartzNet Authors |
| Status | Draft |
| Type | Standards Track |
| Created | 2026-04-13 |

## Abstract

This document specifies a convention for binding a long-lived
ed25519 publisher key to a BitTorrent `.torrent` file by adding
two optional fields to the file's top-level metainfo dictionary:
`snet.pubkey` (32 bytes of public key) and `snet.sig` (64 bytes
of signature). The signature payload is a fixed domain prefix
followed by the torrent's BEP-3 SHA-1 infohash, so verification
is a one-pass operation that requires no separate state and no
network round trip.

The convention is fully wire-compatible with every existing
BitTorrent client. `snet.pubkey` and `snet.sig` are unknown
top-level keys; vanilla clients (qBittorrent, Transmission,
libtorrent, anacrolix/torrent, etc.) skip them when parsing the
metainfo dictionary, and a signed `.torrent` downloads through
the swarm exactly like any other.

## Motivation

`.torrent` files have always been authorless. A peer downloading
content has no protocol-level way to ask "who minted this
metainfo?", which means:

- **Result poisoning** is cheap. An attacker can publish a
  metainfo dictionary whose `info.name` is "ubuntu-24.04.iso"
  but whose actual contents are unrelated. Search systems built
  on top of BitTorrent (Tribler, SwartzNet's own Layer-D DHT
  keyword index) have no way to know which results came from a
  trusted minter.
- **Reputation cannot accumulate**. A user who consistently
  publishes good torrents has no portable identity that survives
  across torrents. Centralized trackers solved this with user
  accounts; a P2P system needs an analogue.
- **Mirror provenance is unverifiable**. Re-uploaded `.torrent`
  files cannot prove they were minted by the original publisher
  even when the underlying content is byte-identical (same
  infohash).

This convention lets a publisher attach a portable, verifiable
identity to every `.torrent` file they create, **without
changing the infohash and without breaking interoperability with
any existing client**. SwartzNet uses it as the basis for its
publisher-trust feature (auto-confirm to the known-good Bloom
filter, gold-star rendering in search results, search-by-
publisher facet) but the convention itself is independent of
SwartzNet — any client implementing it can verify any signed
torrent.

## Rationale

### Why top-level fields and not info-dict fields

The infohash a peer announces is `SHA1(bencode(info_dict))`. If
we put signing fields *inside* the `info` dictionary, every
signed torrent would have a different infohash from its unsigned
twin even when the content is byte-identical. That is a
non-starter: it would split the swarm and turn signing from
"opt-in metadata" into "fork the network".

By putting the fields **outside** the `info` dictionary, the
infohash stays exactly what it would be for an unsigned
torrent. A peer can take a signed `.torrent`, strip the signing
fields, and hand the result to a vanilla client; both will
report the same infohash and join the same swarm.

### Why not modify a tracker / DHT response

Trackers and the DHT both speak about infohashes, not about
metainfo dictionaries. Embedding signatures in tracker responses
would (a) require every tracker to add a new field, (b) provide
no security benefit over adding the same field to the metainfo
itself, and (c) miss the attack the spec actually defends
against — a maliciously-crafted `.torrent` file passed around
out-of-band.

### Why ed25519

ed25519 is already the signature scheme mandated by BEP-44 for
DHT mutable items, so SwartzNet's persistent identity is already
an ed25519 keypair (see `internal/identity`). Reusing the same
key avoids a second key-management surface for the user. Public
keys are 32 bytes; signatures are 64 bytes; verification is fast
(~50 µs on commodity x86_64), and the construction is
maintained by the same code path as the rest of the project's
crypto.

### Why a domain prefix

The signature payload is `"SN-TORRENT-V1|" || <infohash>`, not
`<infohash>` alone. The domain prefix prevents the same key
being used in a future protocol where 20-byte values are signed
under different semantics. Without it, an attacker who could
trick a publisher into signing a 20-byte challenge in some
unrelated context would have a forgery for every torrent with
that 20-byte infohash. Domain separation is cheap insurance.

The prefix is a constant byte string with no length encoding —
the signature payload is always 14 + 20 = 34 bytes. The trailing
`|` separator is decorative; the verifier and signer only need
to agree on the exact bytes. Both endpoints derive the payload
through the helper documented in §Reference Implementation
below, so accidental divergence is structurally prevented.

### Why one signature, not a chain

V1 binds exactly one publisher key per torrent. Multi-signature
schemes (publisher + auditor + retailer countersigns) are not
supported and not foreseen. If the field set ever grows,
versioning is via the domain prefix: a future
`"SN-TORRENT-V2|"` payload would be a separate, additive
extension rather than a backwards-incompatible upgrade.

## Specification

### Wire format

A signed `.torrent` file is a bencoded dictionary that contains,
in addition to the standard BEP-3 fields (`info`, `announce`,
`announce-list`, `creation date`, etc.), the following two
top-level keys:

| Key | Type | Length | Required? |
|---|---|---|---|
| `snet.pubkey` | bencoded byte string | exactly 32 | yes if signed |
| `snet.sig` | bencoded byte string | exactly 64 | yes if signed |

Both keys MUST be present together. A torrent that contains one
without the other is malformed and MUST be treated as unsigned
(verifiers SHOULD log the inconsistency but MUST NOT raise it as
a hard error to the user).

The keys MAY appear in any order relative to the other top-level
keys; bencode dictionaries are sorted by key at marshal time, so
their lexical position is determined by the encoder.

### Signature payload

The signature is computed over the byte sequence:

```
payload = "SN-TORRENT-V1|" || infohash
```

where:

- `"SN-TORRENT-V1|"` is the literal 14-byte ASCII string `SN`,
  `-`, `T`, `O`, `R`, `R`, `E`, `N`, `T`, `-`, `V`, `1`, `|`
  (no quotes, no length prefix, no trailing NUL).
- `infohash` is the BEP-3 SHA-1 of the bencoded `info`
  dictionary (20 bytes, big-endian as written by SHA-1).

`payload` is therefore always exactly 34 bytes.

### Signing algorithm

A publisher with ed25519 private key `sk` signs an existing
`.torrent` file as follows:

```
1. raw  = read_file(path)
2. mi   = bencode_decode(raw)
3. info = mi["info"]                       ; bencoded bytes
4. ih   = sha1(info)                        ; 20 bytes
5. pl   = "SN-TORRENT-V1|" || ih            ; 34 bytes
6. sig  = ed25519_sign(sk, pl)              ; 64 bytes
7. pub  = ed25519_public_from_secret(sk)    ; 32 bytes
8. mi["snet.pubkey"] = pub
9. mi["snet.sig"]    = sig
10. atomic_write(path, bencode_encode(mi))
```

Step 8 / 9 overwrite any existing signing fields, so re-signing
a previously-signed torrent simply updates the publisher
identity (it does not require an explicit "remove old signature"
step).

### Verification algorithm

A verifier reads a `.torrent` file as follows:

```
1. raw  = read_file(path)
2. mi   = bencode_decode(raw)
3. info = mi["info"]
4. ih   = sha1(info)
5. if "snet.pubkey" not in mi or "snet.sig" not in mi:
       return ErrNotSigned
6. pub  = mi["snet.pubkey"]
7. sig  = mi["snet.sig"]
8. if len(pub) != 32 or len(sig) != 64:
       return ErrBadSignature
9. pl   = "SN-TORRENT-V1|" || ih
10. ok = ed25519_verify(pub, pl, sig)
11. return Signature{pub, sig, ih} if ok else ErrBadSignature
```

`ErrNotSigned` is a benign outcome — most third-party torrents
will never carry signing fields. `ErrBadSignature` indicates the
fields are present but fail verification, which is a firm signal
that something is wrong (tampered file, wrong key, truncated
download). UIs SHOULD distinguish the two cases when surfacing
the result to the user.

### Identity and key storage

A publisher MUST own a single long-lived ed25519 keypair. The
public key is the publisher's portable identity in this
network. The private key signs every published torrent and MUST
NOT be shared.

In SwartzNet, the keypair is the same one used for the
Layer-D DHT keyword index (`internal/identity`). Implementations
SHOULD persist the private key with restrictive filesystem
permissions (`0600` on POSIX) and refuse to load a key file with
laxer permissions. SwartzNet's loader at
`internal/identity/identity.go` rejects any mode other than
exactly `0600`.

### Trust model (out of scope)

This document specifies signature **production** and
**verification**. It does NOT specify how a verifier decides
*whether to trust* a particular publisher pubkey. That decision
is purely local. Implementations MAY:

- Maintain a user-managed allowlist (SwartzNet's
  `internal/trust` package).
- Use a "TOFU" (trust on first use) policy keyed on first-seen
  pubkey for a given content.
- Require manual approval per torrent.
- Treat all valid signatures as equivalent (signature == "this
  publisher claims authorship", with no implication of
  trustworthiness).

The convention is intentionally agnostic so different
deployments can pick a trust model that matches their use case.

## Backwards Compatibility

This convention is fully backwards compatible with every
BitTorrent client, tracker, and DHT node deployed today:

- **Vanilla clients** (qBittorrent, Transmission, libtorrent,
  anacrolix/torrent, rTorrent, Deluge, etc.) decode the
  metainfo dictionary into a known-key struct and silently drop
  unknown top-level keys per the bencode + BEP-3 conventions.
  Loading and downloading a signed `.torrent` produces no
  observable difference from loading the same file with the
  signing fields stripped.
- **Trackers** receive the infohash via announce, never the
  metainfo dictionary itself. Signing changes nothing on the
  tracker side.
- **DHT nodes** receive infohashes (BEP-5) and, optionally,
  metadata via BEP-9 ut_metadata. Signed metainfo passed
  through ut_metadata is delivered to the requesting peer
  byte-identical to what was originally bencoded; if the peer
  is a vanilla client, it ignores the signing fields, and if
  the peer is signing-aware, it can verify. No DHT node has
  to interpret the fields.

A signed `.torrent` file is therefore safe to publish to any
existing distribution channel (private trackers, public
mirrors, mailing-list attachments) without coordination.

### Forward compatibility

Future versions of this specification MAY add additional
top-level fields under the `snet.*` namespace (e.g.
`snet.metadata`, `snet.next_pubkey` for key rotation). Verifiers
MUST tolerate unknown `snet.*` keys; they MUST NOT make
verification depend on a key whose name they do not recognise.
This mirrors the LTEP unknown-key policy of BEP-10.

The domain prefix `"SN-TORRENT-V1|"` is the version anchor: a
future v2 signature would use `"SN-TORRENT-V2|"` as its
payload prefix. A v1-only verifier would compute the v1
payload, see the signature does not verify against v1, and
return `ErrBadSignature` (with the option of falling through to
"unsigned" semantics if the v2 fields are not understood).

## Reference Implementation

A complete reference implementation in Go ships in the
SwartzNet client at `internal/signing/`:

- `signing.go` — `SignBytes`, `VerifyBytes`, `SignFile`,
  `VerifyFile`, the `Signature` result type, and the
  `signingPayload` helper that both Sign and Verify call so the
  exact byte construction is shared by both endpoints.
- `signing_test.go` — round-trip, unsigned-detection,
  tampered-content, file-on-disk round-trip, hex encoding.

The wider integration spans:

- `cmd/swartznet/cmd_create.go` — CLI `swartznet create --sign`
  flag that loads the node's ed25519 identity and signs the
  output.
- `internal/engine/create.go` — `CreateTorrentOptions.SignWith`
  field; `CreateTorrentFile` calls `signing.SignBytes` between
  bencode marshal and atomic write.
- `internal/engine/engine.go` — `Handle.signedBy` populated at
  add-time via `VerifyFile` for `.torrent` adds; magnet adds
  always leave it empty (no metainfo bytes available before
  ut_metadata fetch).
- `internal/trust/trust.go` — user-managed publisher trust list
  (orthogonal to signing itself; scoped to the implementation's
  trust model).
- `internal/indexer/schema.go` — Bleve schema v3 stores
  `signed_by` per torrent doc; `internal/indexer/indexer.go`
  exposes a `SignedBy` filter on `SearchRequest`.

The reference implementation has 7 unit tests covering
sign/verify round-trip, error paths, hex encoding, plus engine
integration tests for the create-and-add flow. All run under
`go test -race`.

## Security Considerations

### Threat model

The convention defends against:

- **Tampering with metainfo bytes after publication.** Any
  modification to the `info` dict changes the SHA-1, which
  changes the signature payload, which makes the signature
  fail. A man-in-the-middle who serves a different `.torrent`
  for the same advertised pubkey is detectable.
- **Impersonation by attackers without the private key.** A
  forger needs the ed25519 private key to mint a valid
  signature. The construction inherits ed25519's standard
  unforgeability (~128-bit security).
- **Result-spam attribution gap.** When combined with a trust
  list, a verifier can distinguish "this torrent was minted by
  someone I trust" from "this torrent claims to be from a
  trusted publisher but is unsigned or signature-failed".

The convention does NOT defend against:

- **Anonymity.** A signed torrent advertises the publisher's
  long-lived pubkey to anyone who downloads the file. A
  publisher who wants their identity unlinkable across torrents
  must use ephemeral keys (and accept that the trust /
  reputation accrual that motivated signing in the first place
  is then unavailable).
- **Compromised private keys.** If a private key is stolen, the
  attacker can mint signatures indistinguishable from the
  legitimate publisher's. There is no key-revocation mechanism
  in v1; the publisher must rotate their identity (publish a
  new pubkey through whatever out-of-band channel originally
  established the trust relationship) and let downstream
  verifiers update their trust lists. A revocation field in a
  future version is a possibility but is deliberately not
  scoped here.
- **Lying about content.** A publisher can sign a torrent whose
  `info.name` is misleading. The signature attests to "I, the
  holder of this private key, minted this metainfo"; it does
  not attest to "the metainfo's claims about the content are
  accurate". Trust models that combine signature verification
  with reputation (download success, user flags) are the
  defence here.
- **Replay across protocols.** If the same ed25519 key is used
  to sign data under a different protocol with a different
  domain prefix, signatures are not transferable (that's what
  the `"SN-TORRENT-V1|"` prefix is for). If the key is reused
  under a protocol that signs raw 20-byte values without a
  domain prefix, an attacker could potentially extract a
  signature usable as a torrent signature. Implementations
  SHOULD use distinct keypairs per protocol, or SHOULD ensure
  every protocol that uses the key applies its own domain
  separation.

### Cryptographic agility

The convention is fixed on ed25519 with a 14-byte literal
domain prefix. There is no in-band negotiation of signature
algorithm or hash function. If the cryptographic landscape
shifts (ed25519 broken, SHA-1 needs replacing for the infohash
itself), the path forward is a new field set under a new
domain prefix (e.g. `snet.pubkey2`, `snet.sig2`,
`"SN-TORRENT-V2|"`). v1 verifiers and v2 verifiers can coexist
on the same network during a transition.

Note that the SHA-1 used here is BEP-3's infohash, not a
collision-resistance-critical use of SHA-1 in this
specification: we sign the infohash, not the content. A SHA-1
collision attacker who can craft two `info` dicts with the
same hash already controls the underlying torrent's identity
(same infohash means same swarm); they don't need a
signature break to do damage.

### Privacy of unsigned torrents

A torrent without `snet.pubkey` / `snet.sig` is unaffected by
this specification. There is no requirement that publishers
sign anything; UIs SHOULD treat unsigned torrents as
first-class citizens (download, seed, search) and SHOULD NOT
penalise them in default rankings. SwartzNet's reference UIs
display a `—` placeholder for unsigned torrents and a `✓
<pubkey-prefix>` badge for signed ones, with no value
judgment attached to either state by default.

## References

- BEP-3: The BitTorrent Protocol Specification — defines
  bencode, the metainfo dictionary, and the SHA-1 infohash.
- BEP-10: Extension Protocol (LTEP) — establishes the
  unknown-key tolerance pattern this convention follows.
- BEP-44: Storing Arbitrary Data in the DHT — uses the same
  ed25519 scheme; SwartzNet's identity package serves both
  this specification and BEP-44 from one keypair.
- RFC 8032: Edwards-Curve Digital Signature Algorithm (EdDSA)
  — the formal ed25519 spec.
- `docs/05-integration-design.md` — full SwartzNet
  architecture; §3 covers the layered design that publisher
  signing slots into.
- `docs/06-bep-sn_search-draft.md` — the peer-wire `sn_search`
  protocol that surfaces `signed_by` in result hits.
- `docs/07-bep-dht-keyword-index-draft.md` — the BEP-44
  keyword index that uses the same ed25519 keypair for its
  per-publisher namespacing.
- `docs/08-operations.md` — operator-facing documentation of
  trust-list management, search-by-publisher, and the GUI
  signature-verification dialog.

## Copyright

This document is placed in the public domain.
