// Package identity manages SwartzNet's persistent ed25519 keypair.
//
// Every SwartzNet node owns exactly one ed25519 keypair, generated on
// first run and persisted to ~/.local/share/swartznet/identity.key.
// The public key becomes the node's "publisher identity" for the
// BEP-44 keyword index in Layer D (M4): a (publisher_pubkey, keyword)
// pair uniquely identifies a DHT mutable item under that publisher's
// namespace, so any node that knows the pubkey can compute the same
// SHA1(pk||salt) target and look up the publisher's hits.
//
// We deliberately use a single long-lived keypair per node rather than
// rotating keys per-publish:
//
//   - Stable pubkeys mean other nodes can build a reputation history
//     against ours over time, which is the foundation for the M5
//     spam-resistance work.
//   - The integration design (docs/05-integration-design.md §13.6)
//     calls out per-publisher tracking as an open question; this
//     package puts the building block in place. Users who want
//     pseudo-anonymous publishing can rotate the file by hand.
//
// On-disk format is the raw 64-byte ed25519 private key (which embeds
// the 32-byte public key in its second half). Permissions are forced
// to 0600 on save and verified on load — a key file with relaxed
// permissions is treated as compromised and refused.
package identity
