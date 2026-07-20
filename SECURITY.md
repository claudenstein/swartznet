# Security Policy

SwartzNet is pre-1.0 research software. It speaks to untrusted peers over the
public BitTorrent network and the mainline DHT, so we take memory-safety,
resource-exhaustion, and protocol-parsing issues seriously.

## Supported versions

Only the latest tagged release and `main` receive security fixes while the
project is pre-1.0. There is no back-port guarantee for older tags.

## Reporting a vulnerability

**Please do not open a public issue for a security vulnerability.**

Use GitHub's private vulnerability reporting instead:

1. Go to the repository's **Security** tab → **Report a vulnerability**
   (`https://github.com/claudenstein/swartznet/security/advisories/new`).
2. Describe the issue, the affected component, and — if you can — a minimal
   reproduction. Untrusted-input parsers (the bencode decode paths, the
   `sn_search` LTEP handler, the BEP-44 item handling) are the areas of most
   interest.

We'll acknowledge the report, investigate, and coordinate a fix and disclosure
timeline with you.

## Scope notes

- **Not anonymity.** SwartzNet is not Tor and makes no network-level anonymity
  claim. Peer IPs are visible exactly as with any BitTorrent client. Layering a
  VPN or Tor transport is the user's responsibility. This is a documented
  non-goal, not a vulnerability — see
  [`docs/05-integration-design.md`](docs/05-integration-design.md) §9.
- **In scope:** memory-safety bugs, panics reachable from untrusted wire input,
  resource-amplification (unbounded allocation/CPU from small inputs), signature
  or reputation bypass, and index/query injection.
