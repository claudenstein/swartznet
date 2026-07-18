package indexer

import "github.com/swartznet/swartznet/contracts/token"

// PickLookupToken chooses the single DHT lookup token for a free-text
// query: the most distinctive (longest, ties to first appearance) token
// of the CAPPED Tokenize output. The cap matters: publishers publish only
// the capped-8 keyword set of a torrent name, so a token outside that set
// could never be found on the DHT. This is the ONLY token chooser —
// Layer D must have no other code path deciding which keyword to query
// (pre-empts the legacy first-token defect, DECISIONS C16).
//
// An empty or fully-filtered query yields "".
func PickLookupToken(query string) string {
	return token.MostDistinctive(token.Tokenize(query))
}
